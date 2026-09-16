package customfields_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedOrg(t *testing.T, db *gorm.DB) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, customfields.SeedOrganization(db, org.ID))
	return org
}

func defFor(t *testing.T, db *gorm.DB, orgID uuid.UUID, key string) models.CustomFieldDefinition {
	t.Helper()
	defs, err := customfields.New(db).DefinitionsByKey(context.Background(), orgID, models.FieldEntityContact)
	require.NoError(t, err)
	def, ok := defs[key]
	require.True(t, ok, "field %s should exist", key)
	return def
}

// --- Definition validation ---

func TestValidateDefinition_RejectsBadKeys(t *testing.T) {
	for _, key := range []string{"", "Has Spaces", "UPPER", "1leading_digit", "has-dash", "has.dot"} {
		err := customfields.ValidateDefinition(&models.CustomFieldDefinition{
			Key: key, Label: "X", Type: models.FieldTypeText,
		})
		assert.Error(t, err, "key %q should be rejected", key)
	}

	assert.NoError(t, customfields.ValidateDefinition(&models.CustomFieldDefinition{
		Key: "annual_revenue_2026", Label: "Revenue", Type: models.FieldTypeText,
	}))
}

func TestValidateDefinition_DropdownNeedsUsableOptions(t *testing.T) {
	err := customfields.ValidateDefinition(&models.CustomFieldDefinition{
		Key: "stage", Label: "Stage", Type: models.FieldTypeDropdown,
	})
	assert.ErrorContains(t, err, "at least one option")

	err = customfields.ValidateDefinition(&models.CustomFieldDefinition{
		Key: "stage", Label: "Stage", Type: models.FieldTypeDropdown,
		Options: models.JSONBArray{
			map[string]any{"value": "a", "label": "A"},
			map[string]any{"value": "a", "label": "Also A"},
		},
	})
	assert.ErrorContains(t, err, "duplicate option")
}

// --- Value coercion ---

func TestCoerce_Email(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "email", Type: models.FieldTypeEmail}

	v, err := customfields.Coerce(def, "  Alice@Example.COM ")
	require.NoError(t, err)
	require.NotNil(t, v.Text)
	assert.Equal(t, "alice@example.com", *v.Text,
		"addresses are lower-cased so two spellings of one mailbox match")

	_, err = customfields.Coerce(def, "not-an-email")
	assert.Error(t, err)
}

// A phone typed with spaces must match the same number stored from a webhook,
// or duplicate detection and filters silently miss it.
func TestCoerce_PhoneIsNormalised(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "mobile", Type: models.FieldTypePhone}

	v, err := customfields.Coerce(def, "+92 321 123-4567")
	require.NoError(t, err)
	require.NotNil(t, v.Text)
	assert.Equal(t, "923211234567", *v.Text)

	_, err = customfields.Coerce(def, "no digits here")
	assert.Error(t, err)
}

func TestCoerce_NumberRespectsRange(t *testing.T) {
	def := models.CustomFieldDefinition{
		Key: "score", Type: models.FieldTypeNumber,
		Validation: models.JSONB{"min": float64(0), "max": float64(100)},
	}

	v, err := customfields.Coerce(def, "42.5")
	require.NoError(t, err)
	require.NotNil(t, v.Number)
	assert.InDelta(t, 42.5, *v.Number, 0.0001)

	_, err = customfields.Coerce(def, 101)
	assert.ErrorContains(t, err, "at most")

	_, err = customfields.Coerce(def, -1)
	assert.ErrorContains(t, err, "at least")

	_, err = customfields.Coerce(def, "abc")
	assert.ErrorContains(t, err, "not a number")
}

func TestCoerce_DateAcceptsCommonShapes(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "renewal", Type: models.FieldTypeDate}

	for _, in := range []string{"2026-09-16", "2026-09-16T13:45:00Z"} {
		v, err := customfields.Coerce(def, in)
		require.NoError(t, err, "should accept %q", in)
		require.NotNil(t, v.Date)
		assert.Equal(t, "2026-09-16", v.Date.Format("2006-01-02"),
			"the time of day is dropped so the day cannot shift across timezones")
	}

	_, err := customfields.Coerce(def, "16/09/2026")
	assert.Error(t, err)
}

func TestCoerce_DropdownRejectsValuesOutsideTheOptions(t *testing.T) {
	def := models.CustomFieldDefinition{
		Key: "stage", Type: models.FieldTypeDropdown,
		Options: models.JSONBArray{map[string]any{"value": "lead", "label": "Lead"}},
	}

	v, err := customfields.Coerce(def, "lead")
	require.NoError(t, err)
	require.NotNil(t, v.Option)
	assert.Equal(t, "lead", *v.Option)

	_, err = customfields.Coerce(def, "invented")
	assert.ErrorContains(t, err, "not one of the allowed options")
}

// Clearing a field is a normal edit and the only way to express it, so blank
// input must not be an error.
func TestCoerce_BlankClearsTheValue(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "company", Type: models.FieldTypeText}

	v, err := customfields.Coerce(def, "")
	require.NoError(t, err)
	assert.True(t, v.IsEmpty())

	v, err = customfields.Coerce(def, nil)
	require.NoError(t, err)
	assert.True(t, v.IsEmpty())
}

// --- Seeding ---

func TestSeedOrganization_AddsTheBuiltInFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	defs, err := customfields.New(db).DefinitionsByKey(context.Background(), org.ID, models.FieldEntityContact)
	require.NoError(t, err)

	for _, key := range []string{
		models.FieldKeyEmail, models.FieldKeyCompany, models.FieldKeyAddress,
		models.FieldKeySource, models.FieldKeyLifecycleStage,
	} {
		def, ok := defs[key]
		require.True(t, ok, "%s should be seeded", key)
		assert.True(t, def.IsSystem, "%s is referenced by product features", key)
	}
}

// Re-running must not duplicate fields or undo an org's renames — a later
// release adding a built-in field re-runs this over existing orgs.
func TestSeedOrganization_IsIdempotentAndKeepsCustomisations(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	email := defFor(t, db, org.ID, models.FieldKeyEmail)
	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).Where("id = ?", email.ID).
		Update("label", "Work email").Error)

	require.NoError(t, customfields.SeedOrganization(db, org.ID))

	var count int64
	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyEmail).
		Count(&count).Error)
	assert.EqualValues(t, 1, count, "re-seeding must not duplicate a field")

	reloaded := defFor(t, db, org.ID, models.FieldKeyEmail)
	assert.Equal(t, "Work email", reloaded.Label, "an org's rename must survive re-seeding")
}

// --- Storing values ---

func TestSetValues_StoresAndReadsBack(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	svc := customfields.New(db)

	changed, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{
			models.FieldKeyEmail:          "Buyer@Example.com",
			models.FieldKeyCompany:        "Acme",
			models.FieldKeyLifecycleStage: "lead",
		}, nil)
	require.NoError(t, err)
	assert.Len(t, changed, 3)

	values, err := svc.Values(context.Background(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "buyer@example.com", values[models.FieldKeyEmail])
	assert.Equal(t, "Acme", values[models.FieldKeyCompany])
	assert.Equal(t, "lead", values[models.FieldKeyLifecycleStage])
}

// One row per (entity, field): editing from the chat panel and the profile page
// must converge rather than create a second value.
func TestSetValues_UpsertsRatherThanDuplicating(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	svc := customfields.New(db)

	for _, company := range []string{"First", "Second", "Third"} {
		_, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
			models.FieldEntityContact, map[string]any{models.FieldKeyCompany: company}, nil)
		require.NoError(t, err)
	}

	var count int64
	require.NoError(t, db.Model(&models.CustomFieldValue{}).
		Where("entity_id = ?", contact.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	values, err := svc.Values(context.Background(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Third", values[models.FieldKeyCompany])
}

// The changed list drives field-level activity, so an edit that changes nothing
// must report nothing rather than filling the timeline with noise.
func TestSetValues_ReportsOnlyRealChanges(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	svc := customfields.New(db)

	_, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	changed, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)
	assert.Empty(t, changed, "re-setting the same value is not a change")

	changed, err = svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Globex"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{models.FieldKeyCompany}, changed)
}

func TestSetValues_ClearingRemovesTheValue(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	svc := customfields.New(db)

	_, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	changed, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: ""}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{models.FieldKeyCompany}, changed)

	values, err := svc.Values(context.Background(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.NotContains(t, values, models.FieldKeyCompany)
}

// Silently dropping a field the caller believed they set is the kind of failure
// nobody notices until the data is missing.
func TestSetValues_RejectsUnknownFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	_, err := customfields.New(db).SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{"not_a_field": "x"}, nil)
	assert.ErrorContains(t, err, "no such field")
}

func TestSetValues_RejectsInvalidValues(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	_, err := customfields.New(db).SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyEmail: "nope"}, nil)
	assert.Error(t, err)

	_, err = customfields.New(db).SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyLifecycleStage: "invented"}, nil)
	assert.ErrorContains(t, err, "allowed options")
}

func TestSetValues_RejectsArchivedFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	company := defFor(t, db, org.ID, models.FieldKeyCompany)
	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).Where("id = ?", company.ID).
		Update("archived_at", "now()").Error)

	_, err := customfields.New(db).SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	assert.ErrorContains(t, err, "archived")
}

// The list and chat panel need a whole page of contacts at once; fetching per
// row is the N+1 that made the contacts list slow.
func TestValuesFor_LoadsManyContactsInOneQuery(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	svc := customfields.New(db)

	var ids []uuid.UUID
	for i, company := range []string{"Acme", "Globex", "Initech"} {
		contact := testutil.CreateTestContactWith(t, db, org.ID,
			testutil.WithPhoneNumber(fmt.Sprintf("1555600%04d", i)))
		ids = append(ids, contact.ID)
		_, err := svc.SetValues(db.WithContext(context.Background()), org.ID, contact.ID,
			models.FieldEntityContact, map[string]any{models.FieldKeyCompany: company}, nil)
		require.NoError(t, err)
	}

	byContact, err := svc.ValuesFor(context.Background(), org.ID, ids, models.FieldEntityContact)
	require.NoError(t, err)
	require.Len(t, byContact, 3)
	assert.Equal(t, "Acme", byContact[ids[0]][models.FieldKeyCompany])
	assert.Equal(t, "Initech", byContact[ids[2]][models.FieldKeyCompany])
}

func TestValues_AreScopedToTheOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	mine := seedOrg(t, db)
	other := seedOrg(t, db)

	contact := testutil.CreateTestContact(t, db, mine.ID)
	svc := customfields.New(db)
	_, err := svc.SetValues(db.WithContext(context.Background()), mine.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	values, err := svc.Values(context.Background(), other.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Empty(t, values)
}

// Plan 10, S11: a date field stores a calendar day, not an instant.
//
// The column is a bare date, so Postgres truncates whatever it is given in
// whatever zone the session happens to use. A time.Time carries both a zone and
// a time of day, so a renewal date set from a machine five hours ahead of UTC
// landed on the previous day — and the reminder built on it then fired a day
// early, with nothing anywhere recording that the day had moved.
func TestCoerce_DateKeepsTheDayItWasGiven(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "renewal_date", Type: models.FieldTypeDate}

	ahead := time.FixedZone("UTC+5", 5*60*60)
	// 01:35 on the 24th where the user is; 20:35 on the 23rd in UTC.
	given := time.Date(2026, 9, 24, 1, 35, 0, 0, ahead)

	v, err := customfields.Coerce(def, given)
	require.NoError(t, err)
	require.NotNil(t, v.Date)
	assert.Equal(t, "2026-09-24", v.Date.Format("2006-01-02"),
		"the day the user chose must survive being stored")
	assert.Equal(t, time.UTC, v.Date.Location(),
		"stored at UTC midnight so no session zone can move it")
}

// The string form has always normalised; this pins the two paths together.
func TestCoerce_DateStringAndTimeAgree(t *testing.T) {
	def := models.CustomFieldDefinition{Key: "renewal_date", Type: models.FieldTypeDate}

	fromString, err := customfields.Coerce(def, "2026-09-24")
	require.NoError(t, err)
	fromTime, err := customfields.Coerce(def, time.Date(2026, 9, 24, 23, 59, 0, 0, time.UTC))
	require.NoError(t, err)

	require.NotNil(t, fromString.Date)
	require.NotNil(t, fromTime.Date)
	assert.True(t, fromString.Date.Equal(*fromTime.Date))
}
