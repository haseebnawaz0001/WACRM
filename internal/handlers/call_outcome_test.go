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

// seedCallLog writes a finished call for a contact.
func seedCallLog(t *testing.T, app *handlers.App, orgID, contactID uuid.UUID, agentID *uuid.UUID) *models.CallLog {
	t.Helper()
	ended := time.Now().UTC()
	log := models.CallLog{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		WhatsAppAccount: "acct",
		ContactID:       contactID,
		WhatsAppCallID:  "wacid-" + uuid.NewString()[:8],
		CallerPhone:     "+15550001111",
		Direction:       models.CallDirectionIncoming,
		Status:          models.CallStatusCompleted,
		AgentID:         agentID,
		EndedAt:         &ended,
	}
	require.NoError(t, app.DB.Create(&log).Error)
	return &log
}

func outcomeRequest(t *testing.T, app *handlers.App, orgID, userID, logID uuid.UUID, body map[string]any) *fasthttp.RequestCtx {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, orgID, userID)
	testutil.SetPathParam(req, "id", logID.String())
	require.NoError(t, app.RecordCallOutcome(req))
	return req.RequestCtx
}

func decodeOutcome(t *testing.T, ctx *fasthttp.RequestCtx) map[string]any {
	t.Helper()
	var result struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(ctx.Response.Body(), &result))
	return result.Data
}

// The telephony records that a call was answered for forty seconds. It cannot
// record that the customer wanted to cancel, which is the only part anybody
// reads a week later.
func TestRecordCallOutcome_SavesWhatTheAgentSays(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	callLog := seedCallLog(t, app, org.ID, contact.ID, &admin.ID)

	ctx := outcomeRequest(t, app, org.ID, admin.ID, callLog.ID, map[string]any{
		"disposition": models.DispositionResolved,
		"notes":       "Wanted to cancel; talked them through the renewal instead.",
	})
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())

	var saved models.CallLog
	require.NoError(t, app.DB.First(&saved, "id = ?", callLog.ID).Error)
	assert.Equal(t, models.DispositionResolved, saved.Disposition)
	assert.Contains(t, saved.Notes, "renewal")
}

// A disposition nobody recognises is worse than none: it is reported on, and a
// free-text column would make the report meaningless.
func TestRecordCallOutcome_RejectsAnUnknownDisposition(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	callLog := seedCallLog(t, app, org.ID, contact.ID, &admin.ID)

	ctx := outcomeRequest(t, app, org.ID, admin.ID, callLog.ID, map[string]any{
		"disposition": "they_were_rude",
	})
	assert.Equal(t, fasthttp.StatusBadRequest, ctx.Response.StatusCode())
}

// Recording the outcome is when somebody knows there is a follow-up, so it is
// where the follow-up is created. The task carries the call, so the two read as
// one thread rather than two unrelated rows.
func TestRecordCallOutcome_CreatesTheFollowUpCarryingTheCall(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	callLog := seedCallLog(t, app, org.ID, contact.ID, &admin.ID)

	ctx := outcomeRequest(t, app, org.ID, admin.ID, callLog.ID, map[string]any{
		"disposition":    models.DispositionCallBack,
		"follow_up":      true,
		"follow_up_note": "Call back about the renewal",
	})
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())

	data := decodeOutcome(t, ctx)
	require.NotEmpty(t, data["task_id"], "the follow-up should be reported back")

	var task models.Task
	require.NoError(t, app.DB.First(&task, "id = ?", data["task_id"]).Error)
	assert.Equal(t, "Call back about the renewal", task.Title)
	assert.Equal(t, contact.ID, task.ContactID)
	assert.Equal(t, admin.ID, task.OwnerID, "whoever took the call owes the call back")
	require.NotNil(t, task.CallLogID)
	assert.Equal(t, callLog.ID, *task.CallLogID)
	assert.Equal(t, models.TaskSourceCall, task.Source)
}

// An agent working from their task list should not have to close the same
// follow-up twice.
func TestRecordCallOutcome_ClosesTheTaskTheCallWasMadeFor(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, tasks.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	callLog := seedCallLog(t, app, org.ID, contact.ID, &admin.ID)

	existing, err := app.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, TypeKey: models.TaskTypeCallBack,
		Title: "Call them back", OwnerID: &admin.ID, CreatedBy: &admin.ID,
		Source: models.TaskSourceManual, Location: time.UTC,
	})
	require.NoError(t, err)

	ctx := outcomeRequest(t, app, org.ID, admin.ID, callLog.ID, map[string]any{
		"disposition":      models.DispositionResolved,
		"complete_task_id": existing.ID.String(),
	})
	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode())
	assert.Equal(t, true, decodeOutcome(t, ctx)["task_completed"])

	var reloaded models.Task
	require.NoError(t, app.DB.First(&reloaded, "id = ?", existing.ID).Error)
	assert.Equal(t, models.TaskCompleted, reloaded.Status)
}

// Somebody else's call is not theirs to classify.
func TestRecordCallOutcome_AnAgentCannotClassifyAnotherAgentsCall(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agentRole := testutil.CreateAgentRole(t, app.DB, org.ID)
	mine := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&agentRole.ID))
	theirs := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&agentRole.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	callLog := seedCallLog(t, app, org.ID, contact.ID, &theirs.ID)

	ctx := outcomeRequest(t, app, org.ID, mine.ID, callLog.ID, map[string]any{
		"disposition": models.DispositionResolved,
	})
	assert.Equal(t, fasthttp.StatusNotFound, ctx.Response.StatusCode())
}
