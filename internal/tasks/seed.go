package tasks

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// BuiltInTypes are the task types every organization starts with (plan 04).
//
// The default due offsets differ on purpose: a call back is same-day work, a
// quote is not. Starting everything at "tomorrow" makes the deadline noise
// rather than information.
func BuiltInTypes() []models.TaskType {
	return []models.TaskType{
		{Key: models.TaskTypeCallBack, Label: "Call back", Icon: "phone", Color: "blue",
			DefaultDueOffsetMinutes: 60, IsSystem: true, Position: 10},
		{Key: models.TaskTypeSendQuote, Label: "Send quote", Icon: "file-text", Color: "purple",
			DefaultDueOffsetMinutes: 1440, IsSystem: true, Position: 20},
		{Key: models.TaskTypeCheckIn, Label: "Check in", Icon: "message-circle", Color: "green",
			DefaultDueOffsetMinutes: 4320, IsSystem: true, Position: 30},
		{Key: models.TaskTypeFollowUp, Label: "Follow up", Icon: "repeat", Color: "amber",
			DefaultDueOffsetMinutes: 1440, IsSystem: true, Position: 40},
		{Key: models.TaskTypeOther, Label: "Other", Icon: "check-square", Color: "gray",
			DefaultDueOffsetMinutes: 1440, IsSystem: true, Position: 50},
	}
}

// SeedOrganization inserts any built-in task type the organization is missing.
// Safe to re-run: existing types keep whatever the org has renamed them to.
func SeedOrganization(tx *gorm.DB, orgID uuid.UUID) error {
	var existing []string
	if err := tx.Model(&models.TaskType{}).
		Where("organization_id = ?", orgID).Pluck("key", &existing).Error; err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, key := range existing {
		have[key] = true
	}

	for _, t := range BuiltInTypes() {
		if have[t.Key] {
			continue
		}
		row := t
		row.ID = uuid.New()
		row.OrganizationID = orgID
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
