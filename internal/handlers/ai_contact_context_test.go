package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// profileSession puts a contact in a chatbot session, which is how the AI
// context knows who it is talking to.
func profileSession(t *testing.T, orgID, contactID uuid.UUID) *models.ChatbotSession {
	t.Helper()
	return &models.ChatbotSession{
		OrganizationID: orgID,
		ContactID:      contactID,
	}
}

// A CRM holds things nobody would send to a language model. The allowlist is
// the whole point of the feature: without it, enabling the context once would
// ship every field an organization has ever added.
func TestContactProfileContext_OnlySendsAllowlistedFields(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557700001"), testutil.WithProfileName("Ayesha"))

	secret := &models.CustomFieldDefinition{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		EntityType: models.FieldEntityContact, Key: "national_id", Label: "National id",
		Type: models.FieldTypeText, Options: models.JSONBArray{}, Validation: models.JSONB{},
	}
	require.NoError(t, app.DB.Create(secret).Error)

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact,
		map[string]any{"company": "Acme", "national_id": "42101-1234567-8"}, nil)
	require.NoError(t, err)

	rendered := app.BuildContactProfileContextForTest(org.ID,
		profileSession(t, org.ID, contact.ID),
		models.JSONB{"fields": []any{"company"}})

	assert.Contains(t, rendered, "Acme")
	assert.NotContains(t, rendered, "42101-1234567-8",
		"a field nobody allowlisted must not reach the model")
}

// Naming no fields sends no fields. A context that quietly defaulted to "all"
// would be the same mistake with extra steps.
func TestContactProfileContext_NoAllowlistSendsNoFields(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557700002"), testutil.WithProfileName("Ayesha"))

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{"company": "Acme"}, nil)
	require.NoError(t, err)

	rendered := app.BuildContactProfileContextForTest(org.ID,
		profileSession(t, org.ID, contact.ID), models.JSONB{})

	assert.NotContains(t, rendered, "Acme")
	assert.Contains(t, rendered, "Ayesha", "the name is not a custom field")
}

// An organization that means "everything" has to say so.
func TestContactProfileContext_StarSendsEveryLiveField(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557700003"), testutil.WithProfileName("Ayesha"))

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{"company": "Acme"}, nil)
	require.NoError(t, err)

	rendered := app.BuildContactProfileContextForTest(org.ID,
		profileSession(t, org.ID, contact.ID), models.JSONB{"fields": []any{"*"}})

	assert.Contains(t, rendered, "Acme")
}

// The work we owe somebody is the most useful thing the model can know, and
// the thing a customer is most likely to be asking about.
func TestContactProfileContext_IncludesOpenFollowUps(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557700004"), testutil.WithProfileName("Ayesha"))

	_, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, TypeKey: models.TaskTypeFollowUp,
		Title: "Send the revised quote", OwnerID: &admin.ID, CreatedBy: &admin.ID,
		Source: models.TaskSourceManual, Location: time.UTC,
	})
	require.NoError(t, err)

	rendered := app.BuildContactProfileContextForTest(org.ID,
		profileSession(t, org.ID, contact.ID), models.JSONB{})

	assert.Contains(t, rendered, "Send the revised quote")
}

// A section switched off stays off.
func TestContactProfileContext_SectionsCanBeSwitchedOff(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557700005"), testutil.WithProfileName("Ayesha"),
		testutil.WithTags("VIP"))

	_, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, TypeKey: models.TaskTypeFollowUp,
		Title: "Send the revised quote", OwnerID: &admin.ID, CreatedBy: &admin.ID,
		Source: models.TaskSourceManual, Location: time.UTC,
	})
	require.NoError(t, err)

	rendered := app.BuildContactProfileContextForTest(org.ID,
		profileSession(t, org.ID, contact.ID),
		models.JSONB{"include_open_tasks": false, "include_tags": false})

	assert.NotContains(t, rendered, "Send the revised quote")
	assert.NotContains(t, rendered, "VIP")
	assert.Contains(t, rendered, "Ayesha")
}

// Without a contact there is nothing to say, and a heading with nothing under
// it is worse than silence.
func TestContactProfileContext_NoContactRendersNothing(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)

	assert.Empty(t, app.BuildContactProfileContextForTest(org.ID, nil, models.JSONB{}))
	assert.Empty(t, app.BuildContactProfileContextForTest(org.ID,
		&models.ChatbotSession{OrganizationID: org.ID}, models.JSONB{}))
}

// A context type nobody builds contributes nothing to the prompt, which reads
// to an operator as "the AI ignored my instructions".
func TestKnownContextType(t *testing.T) {
	assert.True(t, models.KnownContextType(models.ContextTypeStatic))
	assert.True(t, models.KnownContextType(models.ContextTypeAPI))
	assert.True(t, models.KnownContextType(models.ContextTypeContactProfile))
	assert.False(t, models.KnownContextType("telepathy"))
}

var _ = handlers.App{}
