package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// TaskResponse is the API shape of a task.
type TaskResponse struct {
	ID             string     `json:"id"`
	ContactID      string     `json:"contact_id"`
	ContactName    string     `json:"contact_name,omitempty"`
	ConversationID string     `json:"conversation_id,omitempty"`
	TypeID         string     `json:"type_id"`
	TypeKey        string     `json:"type_key,omitempty"`
	TypeLabel      string     `json:"type_label,omitempty"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	OwnerID        string     `json:"owner_id"`
	Priority       string     `json:"priority"`
	Status         string     `json:"status"`
	DueAt          time.Time  `json:"due_at"`
	AllDay         bool       `json:"all_day"`
	RemindAt       *time.Time `json:"remind_at,omitempty"`
	// Overdue is computed on read, never stored: a stored flag would be wrong
	// between the moment a task lapses and the next job tick.
	Overdue     bool       `json:"overdue"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Source      string     `json:"source"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toTaskResponse(t models.Task) TaskResponse {
	out := TaskResponse{
		ID:          t.ID.String(),
		ContactID:   t.ContactID.String(),
		TypeID:      t.TypeID.String(),
		Title:       t.Title,
		Description: t.Description,
		OwnerID:     t.OwnerID.String(),
		Priority:    t.Priority,
		Status:      t.Status,
		DueAt:       t.DueAt,
		AllDay:      t.AllDay,
		RemindAt:    t.RemindAt,
		Overdue:     t.IsOverdue(time.Now().UTC()),
		CompletedAt: t.CompletedAt,
		Source:      t.Source,
		CreatedAt:   t.CreatedAt,
	}
	if t.ConversationID != nil {
		out.ConversationID = t.ConversationID.String()
	}
	if t.Type != nil {
		out.TypeKey = t.Type.Key
		out.TypeLabel = t.Type.Label
	}
	if t.Contact != nil {
		out.ContactName = t.Contact.ProfileName
	}
	return out
}

// Tasks returns the task service.
func (a *App) Tasks() *tasks.Service { return tasks.New(a.DB) }

type createTaskRequest struct {
	ContactID      string     `json:"contact_id"`
	TypeKey        string     `json:"type_key"`
	TypeID         string     `json:"type_id"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	OwnerID        string     `json:"owner_id"`
	Priority       string     `json:"priority"`
	DueAt          *time.Time `json:"due_at"`
	AllDay         bool       `json:"all_day"`
	RemindAt       *time.Time `json:"remind_at"`
	ConversationID string     `json:"conversation_id"`
	MessageID      string     `json:"message_id"`
}

// ListTasks returns tasks for the caller's chosen view.
func (a *App) ListTasks(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionRead)
	if err != nil {
		return err
	}

	opts := tasks.ListOpts{
		Status: strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("status"))),
	}

	// "mine" is the default view: a task list that opens on everyone's work is
	// a list nobody reads.
	view := strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("view")))
	if view == "" {
		view = "mine"
	}
	switch view {
	case "mine":
		opts.OwnerID = &userID
	case "all":
		// Everything the caller may see.
	case "overdue":
		opts.OwnerID = &userID
		opts.OverdueOnly = true
	default:
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Unknown task view", nil, "")
	}

	if raw := string(r.RequestCtx.QueryArgs().Peek("contact_id")); raw != "" {
		contactID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
		}
		opts.ContactID = &contactID
		// A contact's task panel shows everyone's tasks for that contact, not
		// just the viewer's.
		opts.OwnerID = nil
	}

	if raw := string(r.RequestCtx.QueryArgs().Peek("deal_id")); raw != "" {
		dealID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
		}
		opts.DealID = &dealID
		// A deal's follow-ups belong to the deal, whoever owns them.
		opts.OwnerID = nil
	}

	if raw := string(r.RequestCtx.QueryArgs().Peek("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			opts.Limit = n
		}
	}
	if raw := string(r.RequestCtx.QueryArgs().Peek("offset")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n >= 0 {
			opts.Offset = n
		}
	}

	rows, total, err := a.Tasks().List(context.Background(), orgID, opts)
	if err != nil {
		a.Log.Error("Failed to list tasks", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load tasks", nil, "")
	}

	items := make([]TaskResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTaskResponse(row))
	}
	// The counts the sidebar badge needs, alongside the page the caller asked
	// for. A separate endpoint would mean a second round trip on every load of
	// a screen that has already fetched the list.
	due, dueErr := a.Tasks().DueFor(context.Background(), orgID, userID, a.OrgLocation(orgID))
	if dueErr != nil {
		a.Log.Error("Failed to count due tasks", "error", dueErr, "user_id", userID)
	}

	return r.SendEnvelope(map[string]any{
		"tasks":     items,
		"total":     total,
		"view":      view,
		"overdue":   due.Overdue,
		"due_today": due.DueToday,
		"due_count": due.Total(),
	})
}

// CreateTask adds a follow-up.
func (a *App) CreateTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}

	var req createTaskRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	contactID, err := uuid.Parse(req.ContactID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	in := tasks.CreateInput{
		OrgID:       orgID,
		ContactID:   contactID,
		TypeKey:     req.TypeKey,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		DueAt:       req.DueAt,
		AllDay:      req.AllDay,
		RemindAt:    req.RemindAt,
		CreatedBy:   &userID,
		Source:      models.TaskSourceManual,
		Location:    a.OrgLocation(orgID),
	}
	if req.TypeID != "" {
		if parsed, parseErr := uuid.Parse(req.TypeID); parseErr == nil {
			in.TypeID = &parsed
		}
	}
	if req.OwnerID != "" {
		if parsed, parseErr := uuid.Parse(req.OwnerID); parseErr == nil {
			in.OwnerID = &parsed
		}
	}
	if req.ConversationID != "" {
		if parsed, parseErr := uuid.Parse(req.ConversationID); parseErr == nil {
			in.ConversationID = &parsed
		}
	}
	if req.MessageID != "" {
		if parsed, parseErr := uuid.Parse(req.MessageID); parseErr == nil {
			in.MessageID = &parsed
		}
	}

	task, err := a.Tasks().Create(context.Background(), in)
	if err != nil {
		// These errors name what is wrong with the request, so they are worth
		// returning rather than flattening into a 500.
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, task.ID, models.AuditActionCreated, nil,
		map[string]any{"title": task.Title, "due_at": task.DueAt, "owner_id": task.OwnerID})

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}

// CompleteTask marks a task done.
//
// The permission check is written out here rather than shared with CancelTask:
// the route-permission guard reads handler bodies, and a check hidden behind a
// helper looks to it exactly like no check at all.
func (a *App) CompleteTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}
	return a.closeTask(r, orgID, userID, true)
}

// CancelTask marks a task cancelled.
func (a *App) CancelTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}
	return a.closeTask(r, orgID, userID, false)
}

func (a *App) closeTask(r *fastglue.Request, orgID, userID uuid.UUID, complete bool) error {
	taskID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid task id", nil, "")
	}

	actor := crmActorForUser(userID)
	var task *models.Task
	if complete {
		task, err = a.Tasks().Complete(context.Background(), orgID, taskID, actor)
	} else {
		task, err = a.Tasks().Cancel(context.Background(), orgID, taskID, actor)
	}
	if err != nil {
		if err == tasks.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
		}
		a.Log.Error("Failed to close task", "error", err, "task_id", taskID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update task", nil, "")
	}

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}

// ReassignTask moves a task to another owner.
func (a *App) ReassignTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}

	taskID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid task id", nil, "")
	}

	var req struct {
		OwnerID string `json:"owner_id"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	owner, err := uuid.Parse(req.OwnerID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid owner id", nil, "")
	}

	task, err := a.Tasks().Reassign(context.Background(), orgID, taskID, owner, crmActorForUser(userID))
	if err != nil {
		if err == tasks.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	// Being given work you do not know about is the same as not being given it
	// (plan 00, F5). Taking a task yourself is not news.
	if owner != userID {
		if err := a.Notify().Send(context.Background(), notify.Input{
			OrgID:   orgID,
			UserIDs: []uuid.UUID{owner},
			Type:    models.NotificationTaskAssigned,
			Title:   "Task assigned to you",
			Body:    task.Title,
			Link:    "/tasks?task=" + task.ID.String(),
			Entity:  notify.Entity{Type: "task", ID: &task.ID},
		}); err != nil {
			// The task has moved; refusing the request would leave the caller
			// believing it had not.
			a.Log.Error("Failed to notify the new task owner", "error", err,
				"task_id", task.ID, "owner_id", owner)
		}
	}

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}

// UpdateTaskRequest is the editable shape of a task.
//
// Pointers throughout so "leave it alone" and "clear it" are different
// instructions: a form that always sends every field would otherwise blank a
// description nobody touched.
type UpdateTaskRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Priority    *string `json:"priority"`
	DueAt       *string `json:"due_at"`
	AllDay      *bool   `json:"all_day"`
	RemindAt    *string `json:"remind_at"`
	// ClearRemind removes the reminder. A null remind_at cannot say this,
	// because null is also how "unchanged" arrives.
	ClearRemind bool `json:"clear_remind"`
}

// UpdateTask edits an open task — renaming it, rescheduling it, changing its
// priority (plan 04).
//
// Rescheduling was the gap that mattered: a follow-up moved from today to next
// week could only be cancelled and recreated, which lost its history and its
// place on the contact's timeline.
func (a *App) UpdateTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}

	taskID, err := parsePathUUID(r, "id", "task")
	if err != nil {
		return nil
	}

	var req UpdateTaskRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	in := tasks.UpdateInput{
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		AllDay:      req.AllDay,
		ClearRemind: req.ClearRemind,
	}

	// Timestamps arrive as strings; an unparseable one is the caller's bug and
	// deserves a 400 rather than being silently ignored, which would look like
	// the reschedule simply did not happen.
	if req.DueAt != nil {
		due, parseErr := time.Parse(time.RFC3339, *req.DueAt)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "due_at must be an RFC3339 timestamp", nil, "")
		}
		in.DueAt = &due
	}
	if req.RemindAt != nil {
		remind, parseErr := time.Parse(time.RFC3339, *req.RemindAt)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "remind_at must be an RFC3339 timestamp", nil, "")
		}
		in.RemindAt = &remind
	}

	task, err := a.Tasks().Update(context.Background(), orgID, taskID, in, crmActorForUser(userID))
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	case errors.Is(err, tasks.ErrNotOpen):
		return r.SendErrorEnvelope(fasthttp.StatusConflict,
			"This task is closed. Reopen it before editing.", nil, "")
	case errors.Is(err, tasks.ErrTitleRequired):
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "title is required", nil, "")
	case err != nil:
		a.Log.Error("Failed to update task", "error", err, "task_id", taskID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update task", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, task.ID, models.AuditActionUpdated, nil,
		map[string]any{"title": task.Title, "due_at": task.DueAt})

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}

// ReopenTask puts a closed task back on its owner's list.
//
// Completing one by mistake is a single click; without this the only remedy was
// a second task, which loses the first one's history.
func (a *App) ReopenTask(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionWrite)
	if err != nil {
		return err
	}

	taskID, err := parsePathUUID(r, "id", "task")
	if err != nil {
		return nil
	}

	task, err := a.Tasks().Reopen(context.Background(), orgID, taskID, crmActorForUser(userID))
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task not found", nil, "")
	case err != nil:
		a.Log.Error("Failed to reopen task", "error", err, "task_id", taskID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to reopen task", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, task.ID, models.AuditActionUpdated, nil,
		map[string]any{"status": task.Status})

	return r.SendEnvelope(map[string]any{"task": toTaskResponse(*task)})
}
