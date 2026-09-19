package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func decodeContact(t *testing.T, body []byte) handlers.ContactResponse {
	t.Helper()
	var result struct {
		Data handlers.ContactResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data
}

func TestUpdateContact_SetsCustomFieldValues(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558100001"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{
			models.FieldKeyCompany:        "Acme",
			models.FieldKeyLifecycleStage: "lead",
		},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.UpdateContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	got := decodeContact(t, testutil.GetResponseBody(req))
	assert.Equal(t, "Acme", got.Fields[models.FieldKeyCompany])
	assert.Equal(t, "lead", got.Fields[models.FieldKeyLifecycleStage])
}

// Setting only fields is a complete edit; requiring a column change too would
// make the chat panel unable to save a field on its own.
func TestUpdateContact_FieldsAloneAreAValidEdit(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558110001"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{models.FieldKeyCompany: "Globex"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.UpdateContact(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

// A rejected field value must not leave the column changes applied.
func TestUpdateContact_InvalidFieldRollsBackTheWholeEdit(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15558120001"), testutil.WithProfileName("Original"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"profile_name": "Renamed",
		"fields":       map[string]any{models.FieldKeyEmail: "not-an-email"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.UpdateContact(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))

	var reloaded models.Contact
	require.NoError(t, app.DB.First(&reloaded, "id = ?", contact.ID).Error)
	assert.Equal(t, "Original", reloaded.ProfileName,
		"a rejected field value must not leave half the edit applied")
}

// The timeline should say which field changed, not just "contact updated".
func TestUpdateContact_RecordsFieldLevelActivity(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558130001"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{models.FieldKeyCompany: "Initech"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())
	require.NoError(t, app.UpdateContact(req))

	var events []models.CRMEventOutbox
	require.NoError(t, app.DB.Where("organization_id = ? AND contact_id = ? AND type = ?",
		org.ID, contact.ID, "contact.field_changed").Find(&events).Error)

	require.Len(t, events, 1)
	assert.Equal(t, models.FieldKeyCompany, events[0].Data["field"])
}

// The change carries what the value was and what it became. With only the
// field's key, an automation narrowed to "lifecycle becomes customer" had
// nothing to compare and the lifecycle funnel — which reads the new value from
// the activity log — never counted a stage somebody set by hand.
func TestUpdateContact_FieldChangeCarriesTheOldAndNewValue(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558130002"))

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyLifecycleStage: models.LifecycleLead}, nil)
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{models.FieldKeyLifecycleStage: models.LifecycleCustomer},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())
	require.NoError(t, app.UpdateContact(req))

	var changed models.CRMEventOutbox
	require.NoError(t, app.DB.Where("contact_id = ? AND type = ?", contact.ID, "contact.field_changed").
		First(&changed).Error)
	assert.Equal(t, models.LifecycleLead, changed.Data["from"])
	assert.Equal(t, models.LifecycleCustomer, changed.Data["to"])

	var stage models.CRMEventOutbox
	require.NoError(t, app.DB.Where("contact_id = ? AND type = ?", contact.ID, "contact.lifecycle_stage_changed").
		First(&stage).Error)
	assert.Equal(t, models.LifecycleCustomer, stage.Data["stage"])
	assert.Equal(t, models.LifecycleLead, stage.Data["from"])
}

// Re-saving an unchanged value must not fill the timeline with noise.
func TestUpdateContact_UnchangedFieldRecordsNothing(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558140001"))

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{models.FieldKeyCompany: "Acme"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())
	require.NoError(t, app.UpdateContact(req))

	var count int64
	require.NoError(t, app.DB.Model(&models.CRMEventOutbox{}).
		Where("contact_id = ? AND type = ?", contact.ID, "contact.field_changed").
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestGetContact_ReturnsFieldValues(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558150001"))

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.GetContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	got := decodeContact(t, testutil.GetResponseBody(req))
	assert.Equal(t, "Acme", got.Fields[models.FieldKeyCompany])
}

func TestUpdateContact_RejectsUnknownFieldKeys(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15558160001"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"fields": map[string]any{"invented_field": "x"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.UpdateContact(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req),
		"silently dropping a field the caller set would lose data unnoticed")
}
