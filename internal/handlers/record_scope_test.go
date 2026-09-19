package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Plan 10, S9: a record keyed by id answers to the same scope as the list it
// appears in. The deal board and the task list hid other people's customers
// from an agent without contacts:read, but the by-id routes looked records up
// by organization alone — so the id was the only thing between an agent and a
// colleague's deal, and the "everyone" task view skipped the scope entirely.

// An agent with deal access but no contacts:read sees their own deals and
// deals on contacts they own — not a colleague's deal on a contact they cannot
// open.
func TestDeals_ByIdRoutesAnswerToTheBoardsScope(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "deal-agent",
		[]string{"deals:read", "deals:write", "deals:delete"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	hidden := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557100001"))
	theirs, err := deals.New(app.DB).Create(t.Context(), deals.CreateInput{
		OrgID: org.ID, ContactID: hidden.ID, Title: "Someone else's", OwnerID: &admin.ID, CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	mine, err := deals.New(app.DB).Create(t.Context(), deals.CreateInput{
		OrgID: org.ID, ContactID: hidden.ID, Title: "Mine", OwnerID: &agent.ID, CreatedBy: &admin.ID,
	})
	require.NoError(t, err)

	get := func(dealID uuid.UUID) int {
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, agent.ID)
		testutil.SetPathParam(req, "id", dealID.String())
		require.NoError(t, app.GetDeal(req))
		return testutil.GetResponseStatusCode(req)
	}
	assert.Equal(t, fasthttp.StatusNotFound, get(theirs.ID), "hidden from the board, so hidden by id")
	assert.Equal(t, fasthttp.StatusOK, get(mine.ID))

	del := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(del, org.ID, agent.ID)
	testutil.SetPathParam(del, "id", theirs.ID.String())
	require.NoError(t, app.DeleteDeal(del))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(del))

	var still int64
	require.NoError(t, app.DB.Model(&models.Deal{}).Where("id = ?", theirs.ID).Count(&still).Error)
	assert.EqualValues(t, 1, still, "a deal the agent cannot see must not be deletable by id")

	// Creating a deal on a contact they cannot open is refused exactly as a
	// contact that does not exist is.
	create := testutil.NewJSONRequest(t, map[string]any{"contact_id": hidden.ID.String(), "title": "Sneaky"})
	testutil.SetAuthContext(create, org.ID, agent.ID)
	require.NoError(t, app.CreateDeal(create))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(create))
	var sneaky int64
	require.NoError(t, app.DB.Model(&models.Deal{}).Where("title = ?", "Sneaky").Count(&sneaky).Error)
	assert.Zero(t, sneaky)
}

func TestTasks_EveryoneViewAndMutationsAnswerToTheContactScope(t *testing.T) {
	app := newTestApp(t)
	org := taskOrg(t, app)
	admin := adminFor(t, app, org)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "task-agent",
		[]string{"tasks:read", "tasks:write"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	hidden := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557200001"))
	due := time.Now().UTC().Add(24 * time.Hour)
	theirs, err := tasks.New(app.DB).Create(t.Context(), tasks.CreateInput{
		OrgID: org.ID, ContactID: hidden.ID, Title: "Call about the renewal",
		OwnerID: &admin.ID, CreatedBy: &admin.ID, DueAt: &due,
	})
	require.NoError(t, err)
	mine, err := tasks.New(app.DB).Create(t.Context(), tasks.CreateInput{
		OrgID: org.ID, ContactID: hidden.ID, Title: "My follow-up",
		OwnerID: &agent.ID, CreatedBy: &admin.ID, DueAt: &due,
	})
	require.NoError(t, err)

	list := testutil.NewGETRequest(t)
	testutil.SetAuthContext(list, org.ID, agent.ID)
	list.RequestCtx.QueryArgs().Set("view", "all")
	require.NoError(t, app.ListTasks(list))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(list))

	ids := map[string]bool{}
	for _, task := range decodeTaskList(t, testutil.GetResponseBody(list)) {
		ids[task.ID] = true
	}
	assert.True(t, ids[mine.ID.String()], "their own task is always theirs to see")
	assert.False(t, ids[theirs.ID.String()], "a colleague's task on a contact they cannot open")

	complete := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(complete, org.ID, agent.ID)
	testutil.SetPathParam(complete, "id", theirs.ID.String())
	require.NoError(t, app.CompleteTask(complete))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(complete))

	var after models.Task
	require.NoError(t, app.DB.First(&after, theirs.ID).Error)
	assert.Equal(t, models.TaskOpen, after.Status)
}

func TestExportReport_AnswersToTheReportsOwnPermissions(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "exporter",
		[]string{"reports:read", "reports:export"})
	analyst := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	for _, key := range []string{"pipeline-funnel", "tasks-by-agent"} {
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, analyst.ID)
		testutil.SetPathParam(req, "key", key)
		require.NoError(t, app.ExportReport(req))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req),
			"%s is refused on screen without its own permission, so it is refused as a CSV too", key)
	}
}
