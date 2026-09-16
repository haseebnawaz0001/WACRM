package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// seedFieldOrg creates an organization with the built-in contact fields.
func seedFieldOrg(t *testing.T, app *handlers.App) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	return org
}

func adminFor(t *testing.T, app *handlers.App, org *models.Organization) *models.User {
	t.Helper()
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	return testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
}

func decodeField(t *testing.T, body []byte) handlers.ContactFieldResponse {
	t.Helper()
	var result struct {
		Data struct {
			Field handlers.ContactFieldResponse `json:"field"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Field
}

func TestListContactFields_ReturnsSeededFields(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ListContactFields(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Fields []handlers.ContactFieldResponse `json:"fields"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.GreaterOrEqual(t, len(result.Data.Fields), 5)
}

func TestCreateContactField(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, map[string]any{
		"key": "account_tier", "label": "Account tier", "type": "dropdown",
		"options": []any{
			map[string]any{"value": "free", "label": "Free"},
			map[string]any{"value": "paid", "label": "Paid"},
		},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateContactField(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	field := decodeField(t, testutil.GetResponseBody(req))
	assert.Equal(t, "account_tier", field.Key)
	assert.False(t, field.IsSystem, "an org-defined field is not a system field")
}

func TestCreateContactField_RejectsBadKeyAndDuplicates(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	bad := testutil.NewJSONRequest(t, map[string]any{
		"key": "Not A Key", "label": "Nope", "type": "text",
	})
	testutil.SetAuthContext(bad, org.ID, admin.ID)
	require.NoError(t, app.CreateContactField(bad))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(bad))

	// "company" is already seeded.
	dupe := testutil.NewJSONRequest(t, map[string]any{
		"key": models.FieldKeyCompany, "label": "Company again", "type": "text",
	})
	testutil.SetAuthContext(dupe, org.ID, admin.ID)
	require.NoError(t, app.CreateContactField(dupe))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(dupe))
}

// A key rename would silently break every segment, automation and template
// that refers to it, and a type change would strand existing values in the
// wrong column.
func TestUpdateContactField_KeyAndTypeAreImmutable(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	var def models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyCompany).
		First(&def).Error)

	rename := testutil.NewJSONRequest(t, map[string]any{"key": "organisation", "label": "Company"})
	testutil.SetAuthContext(rename, org.ID, admin.ID)
	rename.RequestCtx.SetUserValue("id", def.ID.String())
	require.NoError(t, app.UpdateContactField(rename))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(rename))

	retype := testutil.NewJSONRequest(t, map[string]any{"type": "number", "label": "Company"})
	testutil.SetAuthContext(retype, org.ID, admin.ID)
	retype.RequestCtx.SetUserValue("id", def.ID.String())
	require.NoError(t, app.UpdateContactField(retype))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(retype))
}

func TestUpdateContactField_RelabelAndReorder(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	var def models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyCompany).
		First(&def).Error)

	req := testutil.NewJSONRequest(t, map[string]any{
		"label": "Organisation", "position": 5, "show_in_list": false,
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", def.ID.String())

	require.NoError(t, app.UpdateContactField(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	field := decodeField(t, testutil.GetResponseBody(req))
	assert.Equal(t, "Organisation", field.Label)
	assert.Equal(t, 5, field.Position)
	assert.False(t, field.ShowInList)
	assert.Equal(t, models.FieldKeyCompany, field.Key, "the key is unchanged")
}

// Built-in fields are referenced by key from segments and automation, so
// deleting one breaks those rather than just tidying a form.
func TestDeleteContactField_RefusesSystemFields(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	var def models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyLifecycleStage).
		First(&def).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", def.ID.String())

	require.NoError(t, app.DeleteContactField(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// Deleting a field must take its values with it: rows whose definition is gone
// are data nothing can interpret.
func TestDeleteContactField_RemovesValuesToo(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	def := &models.CustomFieldDefinition{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		EntityType: models.FieldEntityContact, Key: "temp_note", Label: "Temp note",
		Type: models.FieldTypeText, Options: models.JSONBArray{}, Validation: models.JSONB{},
	}
	require.NoError(t, app.DB.Create(def).Error)

	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{"temp_note": "remember this"}, nil)
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", def.ID.String())

	require.NoError(t, app.DeleteContactField(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var values int64
	require.NoError(t, app.DB.Model(&models.CustomFieldValue{}).
		Where("field_id = ?", def.ID).Count(&values).Error)
	assert.Zero(t, values)
}

// An agent can read field definitions (to render values) but must not reshape
// the record.
func TestContactFields_AgentCanReadButNotWrite(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	agent := createSystemAgentUser(t, app, org)

	read := testutil.NewGETRequest(t)
	testutil.SetAuthContext(read, org.ID, agent.ID)
	require.NoError(t, app.ListContactFields(read))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(read))

	// A denied handler reports an error after sending the envelope, so the
	// status is what the assertion looks at.
	write := testutil.NewJSONRequest(t, map[string]any{
		"key": "sneaky", "label": "Sneaky", "type": "text",
	})
	testutil.SetAuthContext(write, org.ID, agent.ID)
	_ = app.CreateContactField(write)
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(write))

	var def models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", org.ID, models.FieldKeyCompany).
		First(&def).Error)

	del := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(del, org.ID, agent.ID)
	del.RequestCtx.SetUserValue("id", def.ID.String())
	_ = app.DeleteContactField(del)
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(del))
}

// Field definitions are per-organization; one org must not be able to edit
// another's schema.
func TestContactFields_AreScopedToTheOrganization(t *testing.T) {
	app := newTestApp(t)
	mine := seedFieldOrg(t, app)
	other := seedFieldOrg(t, app)
	admin := adminFor(t, app, mine)

	var theirs models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", other.ID, models.FieldKeyCompany).
		First(&theirs).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"label": "Hijacked"})
	testutil.SetAuthContext(req, mine.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", theirs.ID.String())

	require.NoError(t, app.UpdateContactField(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}
