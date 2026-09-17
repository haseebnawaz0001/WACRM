package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// taskTypeAdmin holds tasks:delete, which plan 04 makes the gate for editing
// the types themselves rather than the tasks of them.
func taskTypeAdmin(t *testing.T, app *handlers.App, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, orgID, "task-admin-"+uuid.NewString()[:8],
		[]string{"tasks:read", "tasks:write", "tasks:delete"})
	return testutil.CreateTestUser(t, app.DB, orgID, testutil.WithRoleID(&role.ID))
}

func taskTypeAgent(t *testing.T, app *handlers.App, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, orgID, "task-agent-"+uuid.NewString()[:8],
		[]string{"tasks:read", "tasks:write"})
	return testutil.CreateTestUser(t, app.DB, orgID, testutil.WithRoleID(&role.ID))
}

// Plan 04: an organization defines the kinds of follow-up it does. The five
// built-ins describe a shop, not a clinic or a lender, and without this every
// other kind of work collapses into "Other".
func TestCreateTaskType_AddsAnOrganizationsOwnKind(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{
		"label":                      "Book a fitting",
		"color":                      "purple",
		"default_due_offset_minutes": 120,
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateTaskType(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var created models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ? AND label = ?",
		org.ID, "Book a fitting").First(&created).Error)

	assert.Equal(t, "book_a_fitting", created.Key, "the key is derived so the form asks for one thing")
	assert.Equal(t, 120, created.DefaultDueOffsetMinutes)
	assert.False(t, created.IsSystem)
	assert.Greater(t, created.Position, 0, "a new type goes after the ones people already reach for")
}

// An agent may create tasks without being able to redefine what a task is.
func TestCreateTaskType_RefusesAnAgent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	agent := taskTypeAgent(t, app, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{"label": "Whatever"})
	testutil.SetAuthContext(req, org.ID, agent.ID)

	// requireAuth answers the request itself and hands back a sentinel, so
	// the return value is not the assertion here — the status is.
	_ = app.CreateTaskType(req)
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

// Two types under one key would make "create a call_back task" ambiguous for
// every automation that asks for one.
func TestCreateTaskType_RefusesADuplicateKey(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{"key": "call_back", "label": "Call back again"})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateTaskType(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))
}

// A built-in can be relabelled to fit the words an organization uses, but its
// key stays: the product creates tasks of these kinds by key.
func TestUpdateTaskType_RelabelsButKeepsTheKey(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	var builtIn models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?",
		org.ID, models.TaskTypeCallBack).First(&builtIn).Error)

	ok := testutil.NewJSONRequest(t, map[string]any{"label": "Ring the customer"})
	testutil.SetAuthContext(ok, org.ID, admin.ID)
	testutil.SetPathParam(ok, "id", builtIn.ID.String())
	require.NoError(t, app.UpdateTaskType(ok))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(ok))

	var relabelled models.TaskType
	require.NoError(t, app.DB.First(&relabelled, "id = ?", builtIn.ID).Error)
	assert.Equal(t, "Ring the customer", relabelled.Label)
	assert.Equal(t, models.TaskTypeCallBack, relabelled.Key)

	rename := testutil.NewJSONRequest(t, map[string]any{"key": "ring_them", "label": "Ring the customer"})
	testutil.SetAuthContext(rename, org.ID, admin.ID)
	testutil.SetPathParam(rename, "id", builtIn.ID.String())
	require.NoError(t, app.UpdateTaskType(rename))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(rename),
		"a key rename would leave automations creating a type that no longer answers")
}

// A built-in cannot be archived: the product creates tasks of these kinds
// itself, and a call outcome with nowhere to file its follow-up fails silently.
func TestUpdateTaskType_RefusesToArchiveABuiltIn(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	var builtIn models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?",
		org.ID, models.TaskTypeFollowUp).First(&builtIn).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"archived": true})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", builtIn.ID.String())

	require.NoError(t, app.UpdateTaskType(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// An archived type stays out of the create form but remains visible to the
// settings page, which has to be able to bring it back.
func TestListTaskTypes_HidesArchivedUnlessAsked(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	custom := &models.TaskType{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		Key: "site_visit", Label: "Site visit", Icon: "map-pin", Color: "green",
		DefaultDueOffsetMinutes: 1440, Position: 60,
	}
	require.NoError(t, app.DB.Create(custom).Error)

	archive := testutil.NewJSONRequest(t, map[string]any{"archived": true})
	testutil.SetAuthContext(archive, org.ID, admin.ID)
	testutil.SetPathParam(archive, "id", custom.ID.String())
	require.NoError(t, app.UpdateTaskType(archive))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(archive))

	assert.NotContains(t, listedTaskTypeKeys(t, app, org.ID, admin.ID, false), "site_visit")
	assert.Contains(t, listedTaskTypeKeys(t, app, org.ID, admin.ID, true), "site_visit")
}

// Reordering arrives as one list so a failed request cannot leave the order
// half-applied.
func TestReorderTaskTypes_AppliesTheWholeOrder(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)

	var types []models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ?", org.ID).
		Order("position").Find(&types).Error)
	require.GreaterOrEqual(t, len(types), 3)

	reversed := []string{types[2].ID.String(), types[1].ID.String(), types[0].ID.String()}
	req := testutil.NewJSONRequest(t, map[string]any{"ids": reversed})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ReorderTaskTypes(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var first models.TaskType
	require.NoError(t, app.DB.First(&first, "id = ?", types[2].ID).Error)
	var last models.TaskType
	require.NoError(t, app.DB.First(&last, "id = ?", types[0].ID).Error)
	assert.Less(t, first.Position, last.Position)
}

// A type with history is archived, not deleted: retyping or orphaning finished
// work rewrites what the organization actually did.
func TestDeleteTaskType_RefusesOneInUseAndKeepsBuiltIns(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := taskTypeAdmin(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	custom := &models.TaskType{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		Key: "deliver_sample", Label: "Deliver sample", Icon: "box", Color: "blue",
		DefaultDueOffsetMinutes: 1440, Position: 70,
	}
	require.NoError(t, app.DB.Create(custom).Error)

	// Unused: it goes.
	del := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(del, org.ID, admin.ID)
	testutil.SetPathParam(del, "id", custom.ID.String())
	require.NoError(t, app.DeleteTaskType(del))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(del))

	// Used: it stays.
	used := &models.TaskType{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		Key: "chase_document", Label: "Chase document", Icon: "file", Color: "amber",
		DefaultDueOffsetMinutes: 1440, Position: 80,
	}
	require.NoError(t, app.DB.Create(used).Error)
	require.NoError(t, app.DB.Create(&models.Task{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		ContactID: contact.ID, TypeID: used.ID, Title: "Chase the signed form",
		Status: models.TaskOpen, OwnerID: admin.ID, CreatedByID: &admin.ID,
	}).Error)

	inUse := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(inUse, org.ID, admin.ID)
	testutil.SetPathParam(inUse, "id", used.ID.String())
	require.NoError(t, app.DeleteTaskType(inUse))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(inUse))

	// Built-in: never.
	var builtIn models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?",
		org.ID, models.TaskTypeOther).First(&builtIn).Error)
	system := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(system, org.ID, admin.ID)
	testutil.SetPathParam(system, "id", builtIn.ID.String())
	require.NoError(t, app.DeleteTaskType(system))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(system))
}

func listedTaskTypeKeys(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, includeArchived bool) []string {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, orgID, userID)
	if includeArchived {
		testutil.SetQueryParam(req, "include_archived", "true")
	}
	require.NoError(t, app.ListTaskTypes(req))

	var body struct {
		TaskTypes []models.TaskType `json:"task_types"`
	}
	testutil.ParseEnvelopeResponse(t, req, &body)

	keys := make([]string, 0, len(body.TaskTypes))
	for _, tt := range body.TaskTypes {
		keys = append(keys, tt.Key)
	}
	return keys
}
