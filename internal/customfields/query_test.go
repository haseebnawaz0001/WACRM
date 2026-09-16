package customfields_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// registryFor builds a query registry carrying the org's custom fields.
func registryFor(t *testing.T, db *gorm.DB, orgID uuid.UUID) *contactquery.Registry {
	t.Helper()
	defs, err := customfields.New(db).Definitions(context.Background(), orgID, models.FieldEntityContact)
	require.NoError(t, err)

	r := contactquery.NewRegistry()
	customfields.RegisterFields(r, defs)
	return r
}

// filterContacts runs a filter over an org's contacts.
func filterContacts(t *testing.T, db *gorm.DB, orgID uuid.UUID, f contactquery.Filter) map[uuid.UUID]bool {
	t.Helper()
	viewer := contactquery.Viewer{OrgID: orgID, UserID: uuid.New(), CanSeeAllContacts: true, Location: time.UTC}

	q, err := contactquery.Apply(db.Model(&models.Contact{}), registryFor(t, db, orgID), viewer, f)
	require.NoError(t, err)

	var rows []models.Contact
	require.NoError(t, q.Find(&rows).Error)

	out := make(map[uuid.UUID]bool, len(rows))
	for _, c := range rows {
		out[c.ID] = true
	}
	return out
}

// contactWithFields creates a contact carrying the given field values.
func contactWithFields(t *testing.T, db *gorm.DB, orgID uuid.UUID, phone string, values map[string]any) *models.Contact {
	t.Helper()
	contact := testutil.CreateTestContactWith(t, db, orgID, testutil.WithPhoneNumber(phone))
	_, err := customfields.New(db).SetValues(db.WithContext(context.Background()), orgID, contact.ID,
		models.FieldEntityContact, values, nil)
	require.NoError(t, err)
	return contact
}

func TestRegisterFields_NamespacesCustomFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)
	r := registryFor(t, db, org.ID)

	// The built-in contacts.source column and the custom "source" field must
	// not collide.
	_, ok := r.Lookup("source")
	assert.True(t, ok, "the core contacts.source column stays registered")

	_, ok = r.Lookup("field.source")
	assert.True(t, ok, "the custom field is namespaced under field.")

	stage, ok := r.Lookup("field.lifecycle_stage")
	require.True(t, ok)
	assert.Equal(t, contactquery.TypeOption, stage.Type)
	assert.NotEmpty(t, stage.Options, "a dropdown exposes its options to the builder")
}

func TestFilter_OnDropdownField(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	lead := contactWithFields(t, db, org.ID, "15552100001",
		map[string]any{models.FieldKeyLifecycleStage: "lead"})
	customer := contactWithFields(t, db, org.ID, "15552100002",
		map[string]any{models.FieldKeyLifecycleStage: "customer"})
	none := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15552100003"))

	got := filterContacts(t, db, org.ID, contactquery.Node{
		Field: "field.lifecycle_stage", Operator: "in", Value: []any{"lead"},
	})
	assert.True(t, got[lead.ID])
	assert.False(t, got[customer.ID])
	assert.False(t, got[none.ID])
}

func TestFilter_OnTextField(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	acme := contactWithFields(t, db, org.ID, "15552110001",
		map[string]any{models.FieldKeyCompany: "Acme Corporation"})
	globex := contactWithFields(t, db, org.ID, "15552110002",
		map[string]any{models.FieldKeyCompany: "Globex"})

	got := filterContacts(t, db, org.ID, contactquery.Node{
		Field: "field.company", Operator: "contains", Value: "acme",
	})
	assert.True(t, got[acme.ID], "text matching is case-insensitive")
	assert.False(t, got[globex.ID])
}

// "Is empty" asks about the absence of a value, which is the absence of a row;
// compiling it as a column comparison would match nothing at all.
func TestFilter_IsEmptyMatchesContactsWithNoValue(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	withCompany := contactWithFields(t, db, org.ID, "15552120001",
		map[string]any{models.FieldKeyCompany: "Acme"})
	without := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15552120002"))

	got := filterContacts(t, db, org.ID, contactquery.Node{
		Field: "field.company", Operator: "is_empty",
	})
	assert.True(t, got[without.ID])
	assert.False(t, got[withCompany.ID])
}

// Several field rules in one filter must not multiply contact rows, which is
// why the compiler uses EXISTS rather than a join.
func TestFilter_CombinesSeveralFieldRulesWithoutDuplicating(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	match := contactWithFields(t, db, org.ID, "15552130001", map[string]any{
		models.FieldKeyCompany:        "Acme",
		models.FieldKeyLifecycleStage: "lead",
		models.FieldKeyEmail:          "buyer@acme.com",
	})
	partial := contactWithFields(t, db, org.ID, "15552130002", map[string]any{
		models.FieldKeyCompany:        "Acme",
		models.FieldKeyLifecycleStage: "churned",
	})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: uuid.New(), CanSeeAllContacts: true, Location: time.UTC}
	filter := contactquery.Node{Op: "and", Rules: []contactquery.Node{
		{Field: "field.company", Operator: "equals", Value: "Acme"},
		{Field: "field.lifecycle_stage", Operator: "in", Value: []any{"lead"}},
		{Field: "field.email", Operator: "is_not_empty"},
	}}

	q, err := contactquery.Apply(db.Model(&models.Contact{}), registryFor(t, db, org.ID), viewer, filter)
	require.NoError(t, err)

	var rows []models.Contact
	require.NoError(t, q.Find(&rows).Error)

	require.Len(t, rows, 1, "three field rules must not multiply the contact row")
	assert.Equal(t, match.ID, rows[0].ID)
	assert.NotEqual(t, partial.ID, rows[0].ID)
}

func TestFilter_MixesCustomAndCoreFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	tagged := contactWithFields(t, db, org.ID, "15552140001",
		map[string]any{models.FieldKeyLifecycleStage: "lead"})
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", tagged.ID).
		Update("tags", models.JSONBArray{"VIP"}).Error)

	untagged := contactWithFields(t, db, org.ID, "15552140002",
		map[string]any{models.FieldKeyLifecycleStage: "lead"})

	got := filterContacts(t, db, org.ID, contactquery.Node{Op: "and", Rules: []contactquery.Node{
		{Field: "field.lifecycle_stage", Operator: "in", Value: []any{"lead"}},
		{Field: "tags", Operator: "contains_any", Value: []any{"VIP"}},
	}})

	assert.True(t, got[tagged.ID])
	assert.False(t, got[untagged.ID])
}

// An archived field is still readable on records that carry it, but must not be
// offered as a filter — a builder should not construct queries on a retired field.
func TestRegisterFields_SkipsArchivedFields(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	company := defFor(t, db, org.ID, models.FieldKeyCompany)
	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).Where("id = ?", company.ID).
		Update("archived_at", time.Now().UTC()).Error)

	r := registryFor(t, db, org.ID)
	_, ok := r.Lookup("field.company")
	assert.False(t, ok)
}

func TestFilter_OnNumberField(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := seedOrg(t, db)

	def := &models.CustomFieldDefinition{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		EntityType: models.FieldEntityContact, Key: "score", Label: "Score",
		Type: models.FieldTypeNumber, Options: models.JSONBArray{}, Validation: models.JSONB{},
	}
	require.NoError(t, db.Create(def).Error)

	high := contactWithFields(t, db, org.ID, "15552150001", map[string]any{"score": 90})
	low := contactWithFields(t, db, org.ID, "15552150002", map[string]any{"score": 10})

	got := filterContacts(t, db, org.ID, contactquery.Node{
		Field: "field.score", Operator: "gt", Value: 50,
	})
	assert.True(t, got[high.ID])
	assert.False(t, got[low.ID], "numbers compare numerically, not as text")
}

func TestFilter_IsScopedToTheOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	mine := seedOrg(t, db)
	other := seedOrg(t, db)

	ours := contactWithFields(t, db, mine.ID, "15552160001",
		map[string]any{models.FieldKeyLifecycleStage: "lead"})
	theirs := contactWithFields(t, db, other.ID, "15552160002",
		map[string]any{models.FieldKeyLifecycleStage: "lead"})

	got := filterContacts(t, db, mine.ID, contactquery.Node{
		Field: "field.lifecycle_stage", Operator: "in", Value: []any{"lead"},
	})
	assert.True(t, got[ours.ID])
	assert.False(t, got[theirs.ID])
}
