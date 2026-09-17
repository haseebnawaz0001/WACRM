package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// makeTask inserts an open task for a contact.
func makeTask(t *testing.T, app *handlers.App, orgID, contactID, ownerID uuid.UUID, title string) *models.Task {
	t.Helper()
	var taskType models.TaskType
	require.NoError(t, app.DB.Where("organization_id = ?", orgID).First(&taskType).Error)

	task := &models.Task{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		ContactID:      contactID,
		TypeID:         taskType.ID,
		Title:          title,
		Status:         models.TaskOpen,
		OwnerID:        ownerID,
		CreatedByID:    &ownerID,
		DueAt:          time.Now().Add(24 * time.Hour),
	}
	require.NoError(t, app.DB.Create(task).Error)
	return task
}

// Plan 04 specifies a bulk endpoint. Clearing a morning's follow-ups one
// request at a time is work people stop doing, and a task list nobody tidies
// stops being believed.
func TestBulkTasks_CompletesManyAndReportsPerTask(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	first := makeTask(t, app, org.ID, contact.ID, user.ID, "Call back")
	second := makeTask(t, app, org.ID, contact.ID, user.ID, "Send the quote")
	missing := uuid.New()

	req := testutil.NewJSONRequest(t, map[string]any{
		"ids":    []string{first.ID.String(), second.ID.String(), missing.String()},
		"action": "complete",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.BulkTasks(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var body struct {
		Applied int               `json:"applied"`
		Failed  int               `json:"failed"`
		Failure map[string]string `json:"failures"`
	}
	testutil.ParseEnvelopeResponse(t, req, &body)

	assert.Equal(t, 2, body.Applied)
	assert.Equal(t, 1, body.Failed,
		"one bad id must not undo the tasks that were fine")

	var completed int64
	require.NoError(t, app.DB.Model(&models.Task{}).
		Where("organization_id = ? AND status = ?", org.ID, models.TaskCompleted).
		Count(&completed).Error)
	assert.EqualValues(t, 2, completed)
}

// Deleting is a stronger permission than editing, and the bulk endpoint is not
// a way around that.
func TestBulkTasks_RefusesDeleteWithoutThePermission(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "task-writer-"+uuid.NewString()[:8],
		[]string{"tasks:read", "tasks:write"})
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	task := makeTask(t, app, org.ID, contact.ID, user.ID, "Delete me")

	req := testutil.NewJSONRequest(t, map[string]any{
		"ids": []string{task.ID.String()}, "action": "delete",
	})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.BulkTasks(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

// A task is about a contact, so reading one means being allowed to read that
// contact — otherwise the task endpoints become a way to read the names and
// follow-ups of customers the contact list deliberately hides (plan 10, S9).
func TestGetTask_HidesATaskOnAnUnreachableContact(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	owner := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))

	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "task-agent-"+uuid.NewString()[:8],
		[]string{"tasks:read", "tasks:write"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", owner.ID).Error)
	task := makeTask(t, app, org.ID, contact.ID, owner.ID, "Private follow-up")

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", task.ID.String())
	require.NoError(t, app.GetTask(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req),
		"the same answer as a task that does not exist")
}

// Plan 02 wants the profile to answer "have we spoken before, and how did it
// end?" — which needs the contact's conversations, not just the live one.
func TestListContactConversations_ReturnsTheHistory(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	for i, status := range []models.ConversationStatus{models.ConversationResolved, models.ConversationOpen} {
		require.NoError(t, app.DB.Create(&models.Conversation{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: org.ID,
			ContactID:      contact.ID,
			Status:         status,
			Handling:       models.HandlingNone,
			OpenedAt:       time.Now().Add(time.Duration(-i) * time.Hour),
		}).Error)
	}

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())
	require.NoError(t, app.ListContactConversations(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var body struct {
		Conversations []map[string]any `json:"conversations"`
		Total         int              `json:"total"`
	}
	testutil.ParseEnvelopeResponse(t, req, &body)
	assert.Equal(t, 2, body.Total, "a resolved conversation is still history")
}

// Plan 01: a metadata key an integration has been writing for years is real
// data. Promoting it made a field and copied the values; the only alternative
// was re-entering them by hand, so in practice nobody did.
func TestPromoteMetadata_CreatesTheFieldAndCopiesValues(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))

	withValue := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550401"))
	require.NoError(t, app.DB.Model(withValue).
		Update("metadata", models.JSONB{"loyalty_tier": "gold"}).Error)
	without := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550402"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"metadata_key": "loyalty_tier",
		"field":        map[string]any{"label": "Loyalty tier"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.PromoteMetadata(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req),
		string(testutil.GetResponseBody(req)))

	var def models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND key = ?", org.ID, "loyalty_tier").
		First(&def).Error)
	assert.Equal(t, "Loyalty tier", def.Label)

	var value models.CustomFieldValue
	require.NoError(t, app.DB.Where("field_id = ? AND entity_id = ?", def.ID, withValue.ID).
		First(&value).Error)
	require.NotNil(t, value.ValueText)
	assert.Equal(t, "gold", *value.ValueText)

	var forOther int64
	require.NoError(t, app.DB.Model(&models.CustomFieldValue{}).
		Where("field_id = ? AND entity_id = ?", def.ID, without.ID).Count(&forOther).Error)
	assert.EqualValues(t, 0, forOther, "a contact without the key gets no empty row")

	// The integration that writes the metadata keeps working: deleting what it
	// wrote would break the thing that produced the data.
	var after models.Contact
	require.NoError(t, app.DB.First(&after, "id = ?", withValue.ID).Error)
	assert.Equal(t, "gold", after.Metadata["loyalty_tier"])
}

// Reordering arrives whole so a failed request cannot leave the list
// half-applied.
func TestReorderContactFields_AppliesTheWholeOrder(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))

	var defs []models.CustomFieldDefinition
	require.NoError(t, app.DB.Where("organization_id = ? AND entity_type = ?",
		org.ID, models.FieldEntityContact).Order("position").Find(&defs).Error)
	require.GreaterOrEqual(t, len(defs), 3)

	reversed := []string{defs[2].ID.String(), defs[1].ID.String(), defs[0].ID.String()}
	req := testutil.NewJSONRequest(t, map[string]any{"ids": reversed})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ReorderContactFields(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var first, last models.CustomFieldDefinition
	require.NoError(t, app.DB.First(&first, "id = ?", defs[2].ID).Error)
	require.NoError(t, app.DB.First(&last, "id = ?", defs[0].ID).Error)
	assert.Less(t, first.Position, last.Position)
}
