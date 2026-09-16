package deals

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// DefaultPipelineName is the pipeline every organization starts with.
const DefaultPipelineName = "Sales"

// SeedOrganization gives an organization a default pipeline if it has none.
//
// A board is only useful once it has columns, and asking someone to design a
// sales process before they can create their first deal is a poor first run.
// Re-running is safe: an organization that already has a pipeline (renamed,
// restaged, or both) is left exactly as it is.
func SeedOrganization(tx *gorm.DB, orgID uuid.UUID) error {
	var existing int64
	if err := tx.Model(&models.Pipeline{}).
		Where("organization_id = ?", orgID).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	pipeline := models.Pipeline{
		BaseModel:           models.BaseModel{ID: uuid.New()},
		OrganizationID:      orgID,
		Name:                DefaultPipelineName,
		ObjectLabelSingular: "Deal",
		ObjectLabelPlural:   "Deals",
		Currency:            "USD",
		IsDefault:           true,
	}
	if err := tx.Create(&pipeline).Error; err != nil {
		return err
	}

	for _, stage := range DefaultStages() {
		row := stage
		row.ID = uuid.New()
		row.OrganizationID = orgID
		row.PipelineID = pipeline.ID
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// SeedAllOrganizations seeds every organization.
func SeedAllOrganizations(tx *gorm.DB) error {
	var orgIDs []uuid.UUID
	if err := tx.Model(&models.Organization{}).Pluck("id", &orgIDs).Error; err != nil {
		return err
	}
	for _, orgID := range orgIDs {
		if err := SeedOrganization(tx, orgID); err != nil {
			return err
		}
	}
	return nil
}
