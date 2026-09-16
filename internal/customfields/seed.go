package customfields

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// BuiltInFields are the contact fields every organization starts with
// (plan 01).
//
// They are ordinary definitions rather than columns, so an org can relabel,
// reorder or hide them like any other field. They are marked IsSystem because
// product features refer to them by key — lifecycle stage drives segments and
// automation — so deleting one would break those rather than just removing a
// column from a form.
func BuiltInFields() []models.CustomFieldDefinition {
	return []models.CustomFieldDefinition{
		{
			Key: models.FieldKeyEmail, Label: "Email", Type: models.FieldTypeEmail,
			GroupLabel: "Contact", Position: 10,
			ShowInList: true, ShowInChatPanel: true, IsSystem: true,
		},
		{
			Key: models.FieldKeyCompany, Label: "Company", Type: models.FieldTypeText,
			GroupLabel: "Company", Position: 20,
			ShowInList: true, ShowInChatPanel: true, IsSystem: true,
			Validation: models.JSONB{"max_length": 255},
		},
		{
			Key: models.FieldKeyAddress, Label: "Address", Type: models.FieldTypeText,
			GroupLabel: "Contact", Position: 30,
			ShowInChatPanel: true, IsSystem: true,
			Validation: models.JSONB{"max_length": 500, "multiline": true},
		},
		{
			Key: models.FieldKeySource, Label: "Source", Type: models.FieldTypeDropdown,
			GroupLabel: "Lifecycle", Position: 40,
			ShowInList: true, ShowInChatPanel: true, IsSystem: true,
			Options: models.JSONBArray{
				map[string]any{"value": "inbound", "label": "Inbound", "color": "blue"},
				map[string]any{"value": "import", "label": "Import", "color": "gray"},
				map[string]any{"value": "campaign", "label": "Campaign", "color": "purple"},
				map[string]any{"value": "api", "label": "API", "color": "gray"},
				map[string]any{"value": "manual", "label": "Manual", "color": "gray"},
				map[string]any{"value": "call", "label": "Call", "color": "green"},
			},
		},
		{
			Key: models.FieldKeyLifecycleStage, Label: "Lifecycle stage", Type: models.FieldTypeDropdown,
			GroupLabel: "Lifecycle", Position: 50,
			ShowInList: true, ShowInChatPanel: true, IsSystem: true,
			Options: models.JSONBArray{
				map[string]any{"value": models.LifecycleNew, "label": "New", "color": "gray"},
				map[string]any{"value": models.LifecycleLead, "label": "Lead", "color": "blue"},
				map[string]any{"value": models.LifecycleQualified, "label": "Qualified", "color": "purple"},
				map[string]any{"value": models.LifecycleCustomer, "label": "Customer", "color": "green"},
				map[string]any{"value": models.LifecycleChurned, "label": "Churned", "color": "red"},
			},
		},
	}
}

// SeedOrganization inserts any built-in field the organization is missing.
//
// It is written to be safe to re-run: a new built-in field added in a later
// release reaches existing orgs on the next migration, and the ones already
// present keep whatever the org has since renamed or reordered them to.
func SeedOrganization(tx *gorm.DB, orgID uuid.UUID) error {
	var existing []string
	if err := tx.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ?", orgID, models.FieldEntityContact).
		Pluck("key", &existing).Error; err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, key := range existing {
		have[key] = true
	}

	for _, field := range BuiltInFields() {
		if have[field.Key] {
			continue
		}
		def := field
		def.ID = uuid.New()
		def.OrganizationID = orgID
		def.EntityType = models.FieldEntityContact
		if def.Validation == nil {
			def.Validation = models.JSONB{}
		}
		if def.Options == nil {
			def.Options = models.JSONBArray{}
		}
		if err := tx.Create(&def).Error; err != nil {
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
