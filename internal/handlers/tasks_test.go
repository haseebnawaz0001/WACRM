package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// taskOrg creates an organization with the built-in task types seeded.
func taskOrg(t *testing.T, app *handlers.App) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	return org
}

func decodeTask(t *testing.T, body []byte) handlers.TaskResponse {
	t.Helper()
	var result struct {
		Data struct {
			Task handlers.TaskResponse `json:"task"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Task
}

func decodeTaskList(t *testing.T, body []byte) []handlers.TaskResponse {
	t.Helper()
	var result struct {
		Data struct {
			Tasks []handlers.TaskResponse `json:"tasks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Tasks
}

func TestCreateTask(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554200001"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(),
		"type_key":   models.TaskTypeCallBack,
		"title":      "Call the customer back",
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateTask(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	task := decodeTask(t, testutil.GetResponseBody(req))
	assert.Equal(t, "Call the customer back", task.Title)
	assert.Equal(t, models.TaskOpen, task.Status)
	assert.Equal(t, models.TaskTypeCallBack, task.TypeKey)
	assert.False(t, task.Overdue)
	assert.Equal(t, admin.ID.String(), task.OwnerID)
}

func TestCreateTask_RejectsBadInput(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554210001"))

	noTitle := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "title": "",
	})
	testutil.SetAuthContext(noTitle, org.ID, admin.ID)
	require.NoError(t, app.CreateTask(noTitle))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(noTitle))

	badContact := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": uuid.New().String(), "title": "Orphan",
	})
	testutil.SetAuthContext(badContact, org.ID, admin.ID)
	require.NoError(t, app.CreateTask(badContact))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(badContact))
}

// A task list that opens on everyone's work is a list nobody reads.
func TestListTasks_DefaultsToMine(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554220001"))

	svc := app.Tasks()
	mine, err := svc.Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Mine", CreatedBy: &admin.ID,
	})
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Theirs",
		OwnerID: &other.ID, CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ListTasks(req))

	list := decodeTaskList(t, testutil.GetResponseBody(req))
	require.Len(t, list, 1)
	assert.Equal(t, mine.ID.String(), list[0].ID)
}

// A contact's task panel shows everyone's tasks for that contact, not just the
// viewer's — otherwise a colleague's follow-up looks like it does not exist.
func TestListTasks_ByContactShowsEveryOwner(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554230001"))

	svc := app.Tasks()
	_, err := svc.Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Mine", CreatedBy: &admin.ID,
	})
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Theirs",
		OwnerID: &other.ID, CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetQueryParam(req, "contact_id", contact.ID.String())
	require.NoError(t, app.ListTasks(req))

	assert.Len(t, decodeTaskList(t, testutil.GetResponseBody(req)), 2)
}

func TestListTasks_OverdueView(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554240001"))

	overdue, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Late", CreatedBy: &admin.ID,
	})
	require.NoError(t, err)
	require.NoError(t, app.DB.Model(&models.Task{}).Where("id = ?", overdue.ID).
		Update("due_at", time.Now().UTC().Add(-time.Hour)).Error)

	_, err = app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Upcoming", CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetQueryParam(req, "view", "overdue")
	require.NoError(t, app.ListTasks(req))

	list := decodeTaskList(t, testutil.GetResponseBody(req))
	require.Len(t, list, 1)
	assert.Equal(t, overdue.ID.String(), list[0].ID)
	assert.True(t, list[0].Overdue, "overdue is computed on read")
}

func TestCompleteTask(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554250001"))

	task, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Do it", CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", task.ID.String())

	require.NoError(t, app.CompleteTask(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	done := decodeTask(t, testutil.GetResponseBody(req))
	assert.Equal(t, models.TaskCompleted, done.Status)
	assert.False(t, done.Overdue, "a completed task is not overdue")
}

func TestListTaskTypes(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ListTaskTypes(req))

	var result struct {
		Data struct {
			TaskTypes []models.TaskType `json:"task_types"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Len(t, result.Data.TaskTypes, 5)
}

// --- Notifier job ---

// A due date nobody is told about is a due date nobody meets.
func TestTaskNotifier_RemindsAndFlagsOverdue(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	owner := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554260001"))

	task, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Late task", OwnerID: &owner.ID,
	})
	require.NoError(t, err)
	require.NoError(t, app.DB.Model(&models.Task{}).Where("id = ?", task.ID).
		Updates(map[string]any{
			"due_at":    time.Now().UTC().Add(-time.Hour),
			"remind_at": time.Now().UTC().Add(-2 * time.Hour),
		}).Error)

	runJob(t, app, "task_due_notifier")

	count, err := app.Notify().UnreadCount(context.Background(), org.ID, owner.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "the owner is told it is due and that it lapsed")

	var reloaded models.Task
	require.NoError(t, app.DB.First(&reloaded, "id = ?", task.ID).Error)
	assert.NotNil(t, reloaded.ReminderSentAt)
	assert.NotNil(t, reloaded.OverdueNotifiedAt)
}

// The job runs every minute; a task must be reminded once, not sixty times an
// hour.
func TestTaskNotifier_NotifiesOnlyOnce(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	owner := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554270001"))

	task, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Repeatable", OwnerID: &owner.ID,
	})
	require.NoError(t, err)
	require.NoError(t, app.DB.Model(&models.Task{}).Where("id = ?", task.ID).
		Updates(map[string]any{
			"due_at":    time.Now().UTC().Add(-time.Hour),
			"remind_at": time.Now().UTC().Add(-2 * time.Hour),
		}).Error)

	runJob(t, app, "task_due_notifier")
	runJob(t, app, "task_due_notifier")
	runJob(t, app, "task_due_notifier")

	count, err := app.Notify().UnreadCount(context.Background(), org.ID, owner.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "repeated ticks must not repeat the notification")
}

// A completed task is not overdue, so it must not be chased.
func TestTaskNotifier_IgnoresClosedTasks(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	owner := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554280001"))

	task, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Already done", OwnerID: &owner.ID,
	})
	require.NoError(t, err)
	require.NoError(t, app.DB.Model(&models.Task{}).Where("id = ?", task.ID).
		Updates(map[string]any{
			"due_at":    time.Now().UTC().Add(-time.Hour),
			"remind_at": time.Now().UTC().Add(-2 * time.Hour),
			"status":    models.TaskCompleted,
		}).Error)

	runJob(t, app, "task_due_notifier")

	count, err := app.Notify().UnreadCount(context.Background(), org.ID, owner.ID)
	require.NoError(t, err)
	assert.Zero(t, count)
}

// Being given work you do not know about is the same as not being given it.
func TestReassignTask_TellsTheNewOwner(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	agent := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	created, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, TypeKey: models.TaskTypeFollowUp,
		Title: "Chase the documents", OwnerID: &admin.ID, CreatedBy: &admin.ID,
		Source: models.TaskSourceManual, Location: time.UTC,
	})
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, map[string]any{"owner_id": agent.ID.String()})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", created.ID.String())

	require.NoError(t, app.ReassignTask(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	notes := notificationsFor(t, app, agent.ID, models.NotificationTaskAssigned)
	require.Len(t, notes, 1)
	assert.Equal(t, "Chase the documents", notes[0].Body)
}

// Taking a task yourself is not news.
func TestReassignTask_TakingItYourselfIsNotANotification(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	created, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, TypeKey: models.TaskTypeFollowUp,
		Title: "Chase the documents", OwnerID: &other.ID, CreatedBy: &admin.ID,
		Source: models.TaskSourceManual, Location: time.UTC,
	})
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, map[string]any{"owner_id": admin.ID.String()})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", created.ID.String())

	require.NoError(t, app.ReassignTask(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	assert.Empty(t, notificationsFor(t, app, admin.ID, models.NotificationTaskAssigned))
}
