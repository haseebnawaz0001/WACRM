package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// MaxBulkTasks caps one bulk request (plan 04).
//
// Two hundred is what a person can meaningfully select and still understand
// what they are about to do. Larger selections belong to a filter and a
// background job, not to a click.
const MaxBulkTasks = 200

// GetTask returns one task.
//
// The panels that show a task have it already; this is for the deep link —
// a notification or a timeline entry naming a task somebody now wants to read.
func (a *App) GetTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionRead)
	if err != nil {
		return err
	}
	taskID, err := parsePathUUID(r, "id", "task")
	if err != nil {
		return nil
	}

	task, err := a.Tasks().Get(context.Background(), orgID, taskID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	}
	if !a.canSeeTask(orgID, userID, task) {
		// The same answer as a task that does not exist: confirming one is
		// there, on a contact they cannot open, is itself the leak.
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	}

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}

// DeleteTask removes a task.
//
// Cancelling records that somebody decided not to do the work, which is worth
// keeping; deleting is for the task that should never have existed — a
// mistyped follow-up, a duplicate. It is a soft delete, so the record survives
// for an audit even once it leaves every list.
func (a *App) DeleteTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionDelete)
	if err != nil {
		return err
	}
	taskID, err := parsePathUUID(r, "id", "task")
	if err != nil {
		return nil
	}

	task, err := a.Tasks().Get(context.Background(), orgID, taskID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	}
	if !a.canSeeTask(orgID, userID, task) {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	}

	if err := a.DB.Delete(&models.Task{}, "id = ? AND organization_id = ?", taskID, orgID).Error; err != nil {
		a.Log.Error("Failed to delete task", "error", err, "task_id", taskID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete task", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, taskID, models.AuditActionDeleted,
		map[string]any{"title": task.Title, "status": task.Status}, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

// BulkTaskRequest is a list of ids and one thing to do with them.
type BulkTaskRequest struct {
	IDs    []uuid.UUID `json:"ids"`
	Action string      `json:"action"`
	// OwnerID is the new owner for a reassign.
	OwnerID *uuid.UUID `json:"owner_id"`
	// DueAt is the new deadline for a reschedule, RFC3339.
	DueAt string `json:"due_at"`
}

// BulkTasks applies one action to several tasks (plan 04).
//
// Clearing a morning's follow-ups one request at a time is the kind of work
// people stop doing, and a task list nobody tidies stops being believed.
//
// Failures are reported per task rather than failing the batch: one task that
// somebody else completed a second earlier must not undo the other forty-nine.
func (a *App) BulkTasks(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}

	var req BulkTaskRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if len(req.IDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "ids is required", nil, "")
	}
	if len(req.IDs) > MaxBulkTasks {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity,
			"Too many tasks in one request", map[string]any{"max": MaxBulkTasks}, "")
	}

	// Deleting is a stronger permission than editing, and the bulk endpoint is
	// not a way around that.
	if req.Action == "delete" &&
		!a.HasPermission(userID, models.ResourceTasks, models.ActionDelete, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden,
			"You do not have permission to delete tasks", nil, "")
	}

	var dueAt *time.Time
	if req.Action == "reschedule" {
		parsed, parseErr := time.Parse(time.RFC3339, req.DueAt)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
				"reschedule needs a due_at in RFC3339", nil, "")
		}
		dueAt = &parsed
	}
	if req.Action == "reassign" && req.OwnerID == nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "reassign needs an owner_id", nil, "")
	}

	actor := crmevents.UserActor(userID, "")
	svc := a.Tasks()
	ctx := context.Background()

	applied := 0
	failures := map[string]string{}

	for _, id := range req.IDs {
		task, getErr := svc.Get(ctx, orgID, id)
		if getErr != nil || !a.canSeeTask(orgID, userID, task) {
			failures[id.String()] = "not found"
			continue
		}

		var actionErr error
		switch req.Action {
		case "complete":
			_, actionErr = svc.Complete(ctx, orgID, id, actor)
		case "cancel":
			_, actionErr = svc.Cancel(ctx, orgID, id, actor)
		case "reassign":
			_, actionErr = svc.Reassign(ctx, orgID, id, *req.OwnerID, actor)
		case "reschedule":
			_, actionErr = svc.Update(ctx, orgID, id, tasks.UpdateInput{DueAt: dueAt}, actor)
		case "delete":
			actionErr = a.DB.Delete(&models.Task{}, "id = ? AND organization_id = ?", id, orgID).Error
		default:
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Unknown bulk action", nil, "")
		}

		if actionErr != nil {
			failures[id.String()] = actionErr.Error()
			continue
		}
		applied++
	}

	a.logAudit(orgID, userID, models.ResourceTasks, uuid.Nil, models.AuditActionUpdated, nil,
		map[string]any{"action": req.Action, "requested": len(req.IDs), "applied": applied})

	return r.SendEnvelope(map[string]any{
		"applied":  applied,
		"failed":   len(failures),
		"failures": failures,
	})
}

// canSeeTask applies the task's contact scope to the viewer.
//
// A task is about a contact, so seeing one means being allowed to see that
// contact — otherwise the task list becomes a way to read the names and
// follow-ups of customers the contact list deliberately hides (plan 10, S9).
func (a *App) canSeeTask(orgID, userID uuid.UUID, task *models.Task) bool {
	if task == nil {
		return false
	}
	if task.OwnerID == userID {
		return true
	}
	var count int64
	query := a.scopeAssignedContact(
		a.DB.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", task.ContactID, orgID),
		userID, orgID)
	if err := query.Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}
