package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// CRM dashboard widgets for organizations that already exist (plan 10, 4.7).
//
// SeedDefaultWidgets skips any organization that already holds a widget, which
// is right for a first run and wrong for every run after it: every existing
// organization has the six original widgets, so the CRM ones — the conversation
// backlog, the follow-ups a person owes, the value on the board — would have
// arrived only for organizations created after the feature shipped. Everybody
// else would have had to know the widgets existed and build them by hand.
//
// Each widget is matched by name within the organization, so running this twice
// adds nothing, and an administrator who deletes one does not get it back.

// crmWidget is one widget this migration adds.
type crmWidget struct {
	Name        string
	Description string
	DataSource  string
	DisplayType string
	ChartType   string
	Metric      string
	Field       string
	GroupBy     string
	Color       string
	Filters     models.JSONBArray
	GridX       int
	GridY       int
	GridW       int
	GridH       int
}

// crmDefaultWidgets is what a CRM dashboard is for.
//
// The two personal ones filter on "me", which the widget query resolves against
// whoever is looking rather than whoever saved it — otherwise a shared widget
// called "My open follow-ups" would show one person's tasks to everybody.
func crmDefaultWidgets() []crmWidget {
	return []crmWidget{
		{
			Name:        "Open conversations",
			Description: "Conversations by status",
			DataSource:  "conversations",
			DisplayType: "chart",
			ChartType:   "bar",
			Metric:      "count",
			GroupBy:     "status",
			GridX:       0, GridY: 11, GridW: 6, GridH: 8,
		},
		{
			Name:        "My open follow-ups",
			Description: "Tasks assigned to you that are still open",
			DataSource:  "tasks",
			DisplayType: "number",
			Metric:      "count",
			Color:       "blue",
			Filters: models.JSONBArray{
				map[string]any{"field": "owner_id", "operator": "equals", "value": "me"},
				map[string]any{"field": "status", "operator": "equals", "value": models.TaskOpen},
			},
			GridX: 6, GridY: 11, GridW: 3, GridH: 3,
		},
		{
			Name:        "Pipeline value",
			Description: "Value of the deals opened in this period",
			DataSource:  "deals",
			DisplayType: "number",
			Metric:      "sum",
			Field:       "value",
			Color:       "green",
			Filters: models.JSONBArray{
				map[string]any{"field": "status", "operator": "equals", "value": models.DealOpen},
			},
			GridX: 9, GridY: 11, GridW: 3, GridH: 3,
		},
		{
			Name:        "Deals by stage",
			Description: "Where the open deals are sitting",
			DataSource:  "deals",
			DisplayType: "chart",
			ChartType:   "bar",
			Metric:      "count",
			GroupBy:     "stage_id",
			Filters: models.JSONBArray{
				map[string]any{"field": "status", "operator": "equals", "value": models.DealOpen},
			},
			GridX: 6, GridY: 14, GridW: 6, GridH: 8,
		},
	}
}

// seedCRMDefaultWidgets adds the CRM widgets to every organization that does
// not already have them.
func seedCRMDefaultWidgets(tx *gorm.DB) error {
	var orgs []models.Organization
	if err := tx.Select("id").Find(&orgs).Error; err != nil {
		return fmt.Errorf("migrations: load organizations: %w", err)
	}

	for _, org := range orgs {
		owner, err := widgetOwner(tx, org.ID)
		if err != nil {
			return err
		}
		if owner == uuid.Nil {
			// An organization with no user has nobody to own a widget, and
			// one will be seeded when its first admin is created.
			continue
		}
		if err := addCRMWidgets(tx, org.ID, owner); err != nil {
			return err
		}
	}
	return nil
}

// widgetOwner picks who a seeded widget belongs to: the organization's longest
// standing member. The widgets are shared, so this decides attribution rather
// than visibility.
func widgetOwner(tx *gorm.DB, orgID uuid.UUID) (uuid.UUID, error) {
	var id string
	err := tx.Raw(`
		SELECT u.id::text FROM users u
		JOIN user_organizations uo
		  ON uo.user_id = u.id AND uo.organization_id = ? AND uo.deleted_at IS NULL
		WHERE u.deleted_at IS NULL
		ORDER BY u.created_at
		LIMIT 1`, orgID).Scan(&id).Error
	if err != nil {
		return uuid.Nil, fmt.Errorf("migrations: find widget owner for %s: %w", orgID, err)
	}
	if id == "" {
		return uuid.Nil, nil
	}

	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("migrations: widget owner %q for %s: %w", id, orgID, err)
	}
	return parsed, nil
}

// addCRMWidgets creates the widgets this organization is missing.
func addCRMWidgets(tx *gorm.DB, orgID, ownerID uuid.UUID) error {
	for i, spec := range crmDefaultWidgets() {
		// Unscoped, so a widget an administrator deleted stays deleted. A
		// scoped count cannot see it and would put it back on every run.
		var existing int64
		if err := tx.Unscoped().Model(&models.Widget{}).
			Where("organization_id = ? AND name = ?", orgID, spec.Name).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("migrations: count widget %s: %w", spec.Name, err)
		}
		if existing > 0 {
			continue
		}

		widget := models.Widget{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: orgID,
			UserID:         &ownerID,
			Name:           spec.Name,
			Description:    spec.Description,
			DataSource:     spec.DataSource,
			Metric:         spec.Metric,
			Field:          spec.Field,
			DisplayType:    spec.DisplayType,
			ChartType:      spec.ChartType,
			GroupByField:   spec.GroupBy,
			Filters:        spec.Filters,
			ShowChange:     spec.DisplayType == "number",
			Color:          spec.Color,
			Size:           "small",
			DisplayOrder:   100 + i,
			GridX:          spec.GridX,
			GridY:          spec.GridY,
			GridW:          spec.GridW,
			GridH:          spec.GridH,
			IsShared:       true,
			IsDefault:      true,
		}
		if err := tx.Create(&widget).Error; err != nil {
			return fmt.Errorf("migrations: create widget %s: %w", spec.Name, err)
		}
	}
	return nil
}
