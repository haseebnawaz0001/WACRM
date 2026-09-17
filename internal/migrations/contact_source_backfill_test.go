package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const sourceBackfill = "2026_09_27_backfill_contact_source_field"

// The source attribute is kept twice: the contacts.source column that segments
// filter on, and the built-in field the CRM reports read. Only the column was
// ever written, so every existing contact counted as "unknown" in "new
// contacts by source" while a segment on the same attribute matched it.
func TestBackfillContactSourceField_CopiesTheColumnOntoTheField(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	imported := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("14155550301"))
	require.NoError(t, db.Model(imported).Update("source", "import").Error)

	unknown := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("14155550302"))
	require.NoError(t, db.Model(unknown).Update("source", "").Error)

	// A value no option offers stays on the column alone: copying it would
	// produce a field value the editor cannot show and no filter can select.
	invented := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("14155550303"))
	require.NoError(t, db.Model(invented).Update("source", "carrier_pigeon").Error)

	require.NoError(t, db.Transaction(migrationByName(t, sourceBackfill)))

	assert.Equal(t, "import", sourceFieldOf(t, db, org.ID, imported.ID))
	assert.Empty(t, sourceFieldOf(t, db, org.ID, unknown.ID))
	assert.Empty(t, sourceFieldOf(t, db, org.ID, invented.ID))
}

// Running it twice must not double-write, and a value somebody has since
// corrected by hand must win over the column.
func TestBackfillContactSourceField_IsIdempotentAndKeepsEdits(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("14155550304"))
	require.NoError(t, db.Model(contact).Update("source", "import").Error)

	require.NoError(t, db.Transaction(migrationByName(t, sourceBackfill)))

	// Somebody corrects it.
	require.NoError(t, db.Exec(`
		UPDATE custom_field_values SET value_option = 'manual'
		WHERE entity_id = ?`, contact.ID).Error)

	require.NoError(t, db.Transaction(migrationByName(t, sourceBackfill)))

	assert.Equal(t, "manual", sourceFieldOf(t, db, org.ID, contact.ID))

	var rows int64
	require.NoError(t, db.Raw(`
		SELECT count(*) FROM custom_field_values v
		JOIN custom_field_definitions d ON d.id = v.field_id AND d.key = 'source'
		WHERE v.entity_id = ?`, contact.ID).Scan(&rows).Error)
	assert.EqualValues(t, 1, rows)
}

// An organization seeded before a source option existed has to gain it, or the
// backfill would skip exactly the contacts that option describes.
func TestBackfillContactSourceField_TopsUpMissingOptions(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			org.ID, models.FieldEntityContact, models.FieldKeySource).
		Update("options", models.JSONBArray{
			map[string]any{"value": "manual", "label": "Manual"},
			map[string]any{"value": "walk_in", "label": "Walk in"},
		}).Error)

	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("14155550305"))
	require.NoError(t, db.Model(contact).Update("source", "inbound").Error)

	require.NoError(t, db.Transaction(migrationByName(t, sourceBackfill)))

	var def models.CustomFieldDefinition
	require.NoError(t, db.Where("organization_id = ? AND entity_type = ? AND key = ?",
		org.ID, models.FieldEntityContact, models.FieldKeySource).First(&def).Error)

	assert.True(t, def.HasOption("inbound"), "the missing built-in option is added")
	assert.True(t, def.HasOption("walk_in"), "the organization's own option is kept")
	assert.Equal(t, "inbound", sourceFieldOf(t, db, org.ID, contact.ID))
}

// sourceFieldOf reads a contact's stored source field value.
func sourceFieldOf(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID) string {
	t.Helper()
	var got *string
	require.NoError(t, db.Raw(`
		SELECT v.value_option
		FROM custom_field_values v
		JOIN custom_field_definitions d ON d.id = v.field_id
		WHERE d.organization_id = ? AND d.key = ? AND v.entity_id = ?`,
		orgID, models.FieldKeySource, contactID).Scan(&got).Error)
	if got == nil {
		return ""
	}
	return *got
}

// Plan 01 lists website, referral and other as sources an organization records
// by hand. They shipped after the first top-up had already run everywhere, so
// they need a migration of their own — an option an org never receives is one
// its people cannot choose.
func TestTopUpSourceOptions_AddsTheLaterBuiltIns(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			org.ID, models.FieldEntityContact, models.FieldKeySource).
		Update("options", models.JSONBArray{
			map[string]any{"value": "manual", "label": "Manual"},
		}).Error)

	require.NoError(t, db.Transaction(migrationByName(t, "2026_09_28_top_up_source_options")))

	var def models.CustomFieldDefinition
	require.NoError(t, db.Where("organization_id = ? AND entity_type = ? AND key = ?",
		org.ID, models.FieldEntityContact, models.FieldKeySource).First(&def).Error)

	for _, option := range []string{"website", "referral", "other", "inbound", "manual"} {
		assert.True(t, def.HasOption(option), "the source field should offer %q", option)
	}
}
