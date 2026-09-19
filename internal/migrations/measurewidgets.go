package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Widgets built on measures, for organizations that already have a dashboard.
//
// The measures answer the questions the CRM raises — who is waiting for a
// reply, which follow-ups are late, what was won, which automations failed —
// and a dashboard that existed before them would never show one unless
// somebody knew to build it. The new widgets are added below whatever the
// organization already has, in their own arrangement, so nobody's layout is
// moved.
//
// The four CRM widgets seeded earlier are moved onto measures too, while they
// are still the seeded widget (default, unedited source), so the new builder
// can edit them. "Pipeline value" also starts meaning what its name says —
// the value of the open deals on the board, rather than of deals opened in
// the period that happened to still be open.

// crmWidgetMeasures maps each seeded CRM widget to the measure it becomes.
var crmWidgetMeasures = map[string]struct {
	source string
	spec   models.DefaultWidget
}{
	"Open conversations": {"conversations", models.DefaultWidget{
		Measure: "conversations_open", View: "bar", Split: "status"}},
	"My open follow-ups": {"tasks", models.DefaultWidget{
		Measure: "tasks_open", View: "number", Snapshot: true,
		Filters: models.JSONBArray{map[string]any{"field": "owner", "operator": "equals", "value": "me"}}}},
	"Pipeline value": {"deals", models.DefaultWidget{
		Measure: "deals_open_value", View: "number", Snapshot: true}},
	"Deals by stage": {"deals", models.DefaultWidget{
		Measure: "deals_open", View: "bar", Split: "stage"}},
}

func seedMeasureWidgets(tx *gorm.DB) error {
	var orgs []models.Organization
	if err := tx.Select("id").Find(&orgs).Error; err != nil {
		return fmt.Errorf("migrations: load organizations: %w", err)
	}
	for _, org := range orgs {
		if err := moveCRMWidgetsOntoMeasures(tx, org.ID); err != nil {
			return err
		}
		owner, err := widgetOwner(tx, org.ID)
		if err != nil {
			return err
		}
		if owner == uuid.Nil {
			continue
		}
		if err := addMeasureWidgets(tx, org.ID, owner); err != nil {
			return err
		}
	}
	return nil
}

func moveCRMWidgetsOntoMeasures(tx *gorm.DB, orgID uuid.UUID) error {
	for name, target := range crmWidgetMeasures {
		built := target.spec.Widget()
		err := tx.Model(&models.Widget{}).
			Where("organization_id = ? AND name = ? AND is_default = true AND data_source = ?", orgID, name, target.source).
			Updates(map[string]any{
				"data_source":    built.DataSource,
				"metric":         built.Metric,
				"field":          "",
				"display_type":   built.DisplayType,
				"chart_type":     built.ChartType,
				"group_by_field": built.GroupByField,
				"filters":        built.Filters,
				"config":         built.Config,
				"show_change":    built.ShowChange,
			}).Error
		if err != nil {
			return fmt.Errorf("migrations: move widget %s onto a measure: %w", name, err)
		}
	}
	return nil
}

func addMeasureWidgets(tx *gorm.DB, orgID, ownerID uuid.UUID) error {
	var bottom int
	if err := tx.Raw(`SELECT COALESCE(MAX(grid_y + grid_h), 0) FROM widgets
		WHERE organization_id = ? AND deleted_at IS NULL`, orgID).Scan(&bottom).Error; err != nil {
		return fmt.Errorf("migrations: find the bottom of the dashboard: %w", err)
	}
	var order int
	if err := tx.Raw(`SELECT COALESCE(MAX(display_order), 0) FROM widgets
		WHERE organization_id = ?`, orgID).Scan(&order).Error; err != nil {
		return fmt.Errorf("migrations: find the last widget: %w", err)
	}

	for _, spec := range models.MeasureDefaultWidgets() {
		// Unscoped, so a widget someone deleted stays deleted.
		var existing int64
		if err := tx.Unscoped().Model(&models.Widget{}).
			Where("organization_id = ? AND name = ?", orgID, spec.Name).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("migrations: count widget %s: %w", spec.Name, err)
		}
		if existing > 0 {
			continue
		}
		order++
		widget := spec.Widget()
		widget.ID = uuid.New()
		widget.OrganizationID = orgID
		widget.UserID = &ownerID
		widget.DisplayOrder = order
		widget.GridY += bottom
		if err := tx.Create(&widget).Error; err != nil {
			return fmt.Errorf("migrations: create widget %s: %w", spec.Name, err)
		}
	}
	return nil
}
