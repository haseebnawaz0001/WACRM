package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// The call outcome (plan 10, 4.4).
//
// The telephony records what happened to the connection: answered, forty
// seconds, hung up by the caller. None of that says what the call was about or
// what happens next, and a week later that is the only part anybody wants. It
// lived nowhere, so the follow-up lived in the agent's head.
//
// Recording the outcome is also where the follow-up is created, because that is
// the moment somebody knows there is one. The task carries the call it came
// from, so the two read as one thread on the contact's timeline.

// CallOutcomeRequest is what an agent says happened.
type CallOutcomeRequest struct {
	Disposition string `json:"disposition"`
	Notes       string `json:"notes"`

	// FollowUp creates a call-back task when true.
	FollowUp bool `json:"follow_up"`
	// FollowUpAt is when it is due. Absent means the task type's own default,
	// which is what "call them back" usually means.
	FollowUpAt *time.Time `json:"follow_up_at"`
	// FollowUpNote becomes the task title when set.
	FollowUpNote string `json:"follow_up_note"`

	// CompleteTaskID completes the call-back this call was made for, so an
	// agent who works from their task list does not have to close it twice.
	CompleteTaskID string `json:"complete_task_id"`
}

// RecordCallOutcome saves what an agent says happened on a call.
func (a *App) RecordCallOutcome(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	logID, err := parsePathUUID(r, "id", "call log")
	if err != nil {
		return nil
	}

	var req CallOutcomeRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	req.Disposition = strings.TrimSpace(req.Disposition)
	if !models.KnownDisposition(req.Disposition) {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"Unknown call disposition", nil, "")
	}

	// The agent who took the call may record its outcome. Anybody else needs
	// the permission to see every call in the first place.
	query := a.DB.Where("id = ? AND organization_id = ?", logID, orgID)
	if !a.HasPermission(userID, models.ResourceCallLogs, models.ActionRead, orgID) {
		query = query.Where("agent_id = ?", userID)
	}

	var callLog models.CallLog
	if err := query.First(&callLog).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Call log not found", nil, "")
	}

	if err := a.DB.Model(&callLog).Updates(map[string]any{
		"disposition": req.Disposition,
		"notes":       req.Notes,
	}).Error; err != nil {
		a.Log.Error("Failed to record call outcome", "error", err, "call_log_id", logID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to record the outcome", nil, "")
	}

	response := map[string]any{"recorded": true}

	if id := strings.TrimSpace(req.CompleteTaskID); id != "" {
		completed, completeErr := a.completeCallTask(orgID, userID, id)
		if completeErr != nil {
			// The outcome is saved; failing the whole request over the task
			// would lose what the agent typed.
			a.Log.Error("Failed to complete the call-back task", "error", completeErr, "task_id", id)
		}
		response["task_completed"] = completed
	}

	if req.FollowUp {
		task, taskErr := a.createCallBackTask(orgID, userID, callLog, req)
		if taskErr != nil {
			a.Log.Error("Failed to create the call-back task", "error", taskErr, "call_log_id", logID)
			response["follow_up_error"] = taskErr.Error()
		} else if task != nil {
			response["task_id"] = task.ID.String()
		}
	}

	return r.SendEnvelope(response)
}

// completeCallTask closes the follow-up this call was made for.
func (a *App) completeCallTask(orgID, userID uuid.UUID, rawID string) (bool, error) {
	taskID, err := uuid.Parse(rawID)
	if err != nil {
		return false, err
	}
	if _, err := a.Tasks().Complete(context.Background(), orgID, taskID, crmActorForUser(userID)); err != nil {
		return false, err
	}
	return true, nil
}

// createCallBackTask records the follow-up the agent asked for.
func (a *App) createCallBackTask(orgID, userID uuid.UUID, callLog models.CallLog,
	req CallOutcomeRequest) (*models.Task, error) {

	title := strings.TrimSpace(req.FollowUpNote)
	if title == "" {
		title = "Call back"
	}

	// The call's own agent owns the follow-up when there is one: they are the
	// person who just spoke to the customer. Otherwise the usual resolution
	// order applies, which lands on the contact's owner.
	var owner *uuid.UUID
	if callLog.AgentID != nil {
		owner = callLog.AgentID
	}

	return a.Tasks().Create(context.Background(), tasks.CreateInput{
		OrgID:     orgID,
		ContactID: callLog.ContactID,
		TypeKey:   models.TaskTypeCallBack,
		Title:     title,
		OwnerID:   owner,
		CreatedBy: &userID,
		DueAt:     req.FollowUpAt,
		Source:    models.TaskSourceCall,
		CallLogID: &callLog.ID,
		Location:  a.OrgLocation(orgID),
	})
}
