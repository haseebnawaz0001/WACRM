package handlers_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// contactWriter is a user who may create contacts.
func contactWriter(t *testing.T, db *gorm.DB, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, db, orgID, "contact-writer-"+uuid.NewString()[:8],
		[]string{"contacts:read", "contacts:write"})
	return testutil.CreateTestUser(t, db, orgID, testutil.WithRoleID(&role.ID))
}

// Plan 01 and plan 10 (S2): a contact created by hand is a contact like any
// other. Creating one used to insert the row directly, so it began life with
// no source, no lifecycle stage and no contact.created event — meaning no
// timeline entry, no webhook and no automation trigger for every contact an
// organization typed in itself.
func TestCreateContact_RecordsSourceStageAndEvent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := contactWriter(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{
		"phone_number": "14155551234",
		"profile_name": "Ada Lovelace",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.CreateContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var contact models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?",
		org.ID, "14155551234").First(&contact).Error)

	assert.Equal(t, contacts.SourceManual, contact.Source,
		"a contact typed in by a person came from the product's own UI")

	values, err := customfields.New(app.DB).Values(context.Background(), org.ID,
		contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, models.LifecycleNew, values[models.FieldKeyLifecycleStage])
	assert.Equal(t, contacts.SourceManual, values[models.FieldKeySource],
		"the reports read the field, not the column")

	var events int64
	require.NoError(t, app.DB.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND type = ? AND subject_id = ?",
			org.ID, "contact.created", contact.ID).
		Count(&events).Error)
	assert.EqualValues(t, 1, events,
		"without the event there is no timeline entry, no webhook and no automation trigger")
}

// An API key is not a person. Plan 01 splits the two so an organization can
// tell records its integrations created from ones its staff typed in.
func TestCreateContact_APIKeyRecordsTheAPISource(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := contactWriter(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{"phone_number": "14155551235"})
	testutil.SetAuthContext(req, org.ID, user.ID)
	req.RequestCtx.SetUserValue(middleware.ContextKeyAuthMethod, middleware.AuthMethodAPIKey)

	require.NoError(t, app.CreateContact(req))

	var contact models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?",
		org.ID, "14155551235").First(&contact).Error)
	assert.Equal(t, contacts.SourceAPI, contact.Source)
}

// The handler kept its own restore branch, which undid a deletion somebody
// meant — the exact behaviour the lifecycle service refuses (plan 10, X4).
func TestCreateContact_DoesNotRestoreADeletedContact(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := contactWriter(t, app.DB, org.ID)

	existing := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155551236"))
	require.NoError(t, contacts.New(app.DB).Delete(context.Background(), org.ID,
		existing.ID, contacts.ReasonUser, crmevents.UserActor(user.ID, "")))

	req := testutil.NewJSONRequest(t, map[string]any{
		"phone_number": "14155551236",
		"profile_name": "Somebody Else",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var deleted models.Contact
	require.NoError(t, app.DB.Unscoped().Where("id = ?", existing.ID).First(&deleted).Error)
	assert.True(t, deleted.DeletedAt.Valid, "the deletion must stand")

	var fresh models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?",
		org.ID, "14155551236").First(&fresh).Error)
	assert.NotEqual(t, existing.ID, fresh.ID,
		"the number writing in again is a new record, flagged as a duplicate for review")
}

// A live contact on the same number is a conflict, not a second record.
func TestCreateContact_RefusesADuplicate(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := contactWriter(t, app.DB, org.ID)

	testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("14155551237"))

	req := testutil.NewJSONRequest(t, map[string]any{"phone_number": "14155551237"})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateContact(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))
}

// Plan 01: create accepts custom fields, applies their defaults and enforces
// the ones an organization made required.
func TestCreateContact_AppliesFieldDefaultsAndRequiredRules(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := contactWriter(t, app.DB, org.ID)

	require.NoError(t, app.DB.Create(&models.CustomFieldDefinition{
		OrganizationID: org.ID,
		EntityType:     models.FieldEntityContact,
		Key:            "plan_tier",
		Label:          "Plan tier",
		Type:           models.FieldTypeText,
		DefaultValue:   models.JSONB{"value": "free"},
		Options:        models.JSONBArray{},
		Validation:     models.JSONB{},
	}).Error)
	require.NoError(t, app.DB.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyCompany).
		Update("is_required", true).Error)

	// Required field missing.
	req := testutil.NewJSONRequest(t, map[string]any{"phone_number": "14155551238"})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateContact(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req),
		"a required field an organization set has to be enforced where records are typed in")

	// Supplied — and the untouched field takes its default.
	ok := testutil.NewJSONRequest(t, map[string]any{
		"phone_number": "14155551238",
		"fields":       map[string]any{models.FieldKeyCompany: "Analytical Engines"},
	})
	testutil.SetAuthContext(ok, org.ID, user.ID)
	require.NoError(t, app.CreateContact(ok))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(ok))

	var contact models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?",
		org.ID, "14155551238").First(&contact).Error)

	values, err := customfields.New(app.DB).Values(context.Background(), org.ID,
		contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Analytical Engines", values[models.FieldKeyCompany])
	assert.Equal(t, "free", values["plan_tier"],
		"a default that only exists in the editor is not a default")
}
