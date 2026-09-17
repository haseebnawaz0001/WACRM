package handlers

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// taskTypeKeyPattern is the same slug rule custom field keys use: the key is
// what automations, chatbot nodes and the API refer to, so it has to survive
// being written into a config by hand.
var taskTypeKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// taskTypeRequest is the editable shape of a task type.
//
// Pointers for the optional parts so "leave it alone" and "set it to zero"
// are different instructions — a due offset of 0 means "due immediately",
// which is a real answer and not the same as an absent field.
type taskTypeRequest struct {
	Key                     string `json:"key"`
	Label                   string `json:"label"`
	Icon                    string `json:"icon"`
	Color                   string `json:"color"`
	DefaultDueOffsetMinutes *int   `json:"default_due_offset_minutes"`
	Position                *int   `json:"position"`
	Archived                *bool  `json:"archived"`
}

// ListTaskTypes returns the organization's task types for the create form.
//
// Archived types are included when asked for, because the settings page has to
// show what it can un-archive; the task form asks without the flag and sees
// only what can still be chosen.
func (a *App) ListTaskTypes(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceTasks, models.ActionRead)
	if err != nil {
		return err
	}

	query := a.DB.Where("organization_id = ?", orgID)
	if string(r.RequestCtx.QueryArgs().Peek("include_archived")) != "true" {
		query = query.Where("archived_at IS NULL")
	}

	var types []models.TaskType
	if err := query.Order("position, label").Find(&types).Error; err != nil {
		a.Log.Error("Failed to list task types", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load task types", nil, "")
	}

	return r.SendEnvelope(map[string]any{"task_types": types})
}

// CreateTaskType adds a kind of follow-up the organization does (plan 04).
//
// The five built-ins cover a shop; they do not cover a clinic booking a
// procedure or a lender chasing a document. Without this the defaults were the
// whole vocabulary, and every other kind of work became "Other", which makes
// the type column useless for reporting the moment it is used.
func (a *App) CreateTaskType(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionDelete)
	if err != nil {
		return err
	}

	var req taskTypeRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	req.Key = strings.TrimSpace(req.Key)
	req.Label = strings.TrimSpace(req.Label)
	if req.Key == "" {
		req.Key = slugifyKey(req.Label, 50)
	}
	if err := validateTaskType(req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	var existing int64
	if err := a.DB.Model(&models.TaskType{}).
		Where("organization_id = ? AND key = ?", orgID, req.Key).
		Count(&existing).Error; err == nil && existing > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "A task type with this key already exists", nil, "")
	}

	taskType := &models.TaskType{
		BaseModel:               models.BaseModel{ID: uuid.New()},
		OrganizationID:          orgID,
		Key:                     req.Key,
		Label:                   req.Label,
		Icon:                    firstNonEmpty(req.Icon, "check-square"),
		Color:                   firstNonEmpty(req.Color, "gray"),
		DefaultDueOffsetMinutes: 1440,
	}
	if req.DefaultDueOffsetMinutes != nil {
		taskType.DefaultDueOffsetMinutes = *req.DefaultDueOffsetMinutes
	}
	if req.Position != nil {
		taskType.Position = *req.Position
	} else {
		taskType.Position = a.nextTaskTypePosition(orgID)
	}

	if err := a.DB.Create(taskType).Error; err != nil {
		a.Log.Error("Failed to create task type", "error", err, "key", req.Key)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create task type", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, taskType.ID,
		models.AuditActionCreated, nil, taskTypeAuditSnapshot(taskType))
	return r.SendEnvelope(map[string]any{"task_type": taskType})
}

// UpdateTaskType edits a type's presentation and its default deadline.
//
// The key is immutable for the same reason a custom field's is: automations
// and chatbot nodes create tasks by key, and renaming one would leave them
// pointing at a type that no longer answers.
func (a *App) UpdateTaskType(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionDelete)
	if err != nil {
		return err
	}

	id, err := parsePathUUID(r, "id", "task type")
	if err != nil {
		return nil
	}

	var taskType models.TaskType
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&taskType).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task type not found", nil, "")
	}

	var req taskTypeRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if req.Key != "" && req.Key != taskType.Key {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"A task type's key cannot change: automations and chatbot flows create tasks by key.", nil, "")
	}

	before := taskTypeAuditSnapshot(&taskType)

	if label := strings.TrimSpace(req.Label); label != "" {
		taskType.Label = label
	}
	if req.Icon != "" {
		taskType.Icon = req.Icon
	}
	if req.Color != "" {
		taskType.Color = req.Color
	}
	if req.DefaultDueOffsetMinutes != nil {
		taskType.DefaultDueOffsetMinutes = *req.DefaultDueOffsetMinutes
	}
	if req.Position != nil {
		taskType.Position = *req.Position
	}
	if req.Archived != nil {
		if *req.Archived {
			if taskType.IsSystem {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
					"Built-in task types cannot be archived: the product creates tasks of these kinds itself.", nil, "")
			}
			now := time.Now().UTC()
			taskType.ArchivedAt = &now
		} else {
			taskType.ArchivedAt = nil
		}
	}

	if err := validateTaskType(taskTypeRequest{
		Key: taskType.Key, Label: taskType.Label,
		DefaultDueOffsetMinutes: &taskType.DefaultDueOffsetMinutes,
	}); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	if err := a.DB.Save(&taskType).Error; err != nil {
		a.Log.Error("Failed to update task type", "error", err, "id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update task type", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, taskType.ID,
		models.AuditActionUpdated, before, taskTypeAuditSnapshot(&taskType))
	return r.SendEnvelope(map[string]any{"task_type": taskType})
}

// ReorderTaskTypes sets the order the types appear in, in one request.
//
// Saving positions one at a time leaves the list in whatever half-ordered
// state a failed request produced, and two people reordering at once can
// interleave. The whole order arrives together and is written in a
// transaction.
func (a *App) ReorderTaskTypes(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionDelete)
	if err != nil {
		return err
	}

	var req struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if len(req.IDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "ids is required", nil, "")
	}

	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		for i, id := range req.IDs {
			// Scoping the update to the organization means an id from
			// somewhere else moves nothing rather than reordering a stranger's
			// settings.
			if err := tx.Model(&models.TaskType{}).
				Where("id = ? AND organization_id = ?", id, orgID).
				Update("position", (i+1)*10).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		a.Log.Error("Failed to reorder task types", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to reorder task types", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, uuid.Nil,
		models.AuditActionUpdated, nil, map[string]any{"order": req.IDs})
	return r.SendEnvelope(map[string]any{"message": "Task types reordered"})
}

// DeleteTaskType removes a type nothing has used.
//
// A type with tasks against it is archived instead: deleting it would either
// orphan those tasks or silently retype them, and a completed task that
// changes kind after the fact makes the history a lie.
func (a *App) DeleteTaskType(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionDelete)
	if err != nil {
		return err
	}

	id, err := parsePathUUID(r, "id", "task type")
	if err != nil {
		return nil
	}

	var taskType models.TaskType
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&taskType).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Task type not found", nil, "")
	}
	if taskType.IsSystem {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"Built-in task types cannot be deleted. Relabel one instead to fit how you work.", nil, "")
	}

	var used int64
	if err := a.DB.Model(&models.Task{}).
		Where("organization_id = ? AND type_id = ?", orgID, taskType.ID).
		Count(&used).Error; err != nil {
		a.Log.Error("Failed to count tasks of type", "error", err, "id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete task type", nil, "")
	}
	if used > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict,
			"This type is used by tasks. Archive it instead to stop offering it while keeping their history.",
			map[string]any{"tasks": used}, "")
	}

	if err := a.DB.Delete(&models.TaskType{}, "id = ?", taskType.ID).Error; err != nil {
		a.Log.Error("Failed to delete task type", "error", err, "id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete task type", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceTasks, taskType.ID,
		models.AuditActionDeleted, taskTypeAuditSnapshot(&taskType), nil)
	return r.SendEnvelope(map[string]any{"message": "Task type deleted"})
}

// nextTaskTypePosition puts a new type at the end of the list rather than the
// front, where it would displace the ones people already reach for.
func (a *App) nextTaskTypePosition(orgID uuid.UUID) int {
	var highest *int
	if err := a.DB.Model(&models.TaskType{}).
		Where("organization_id = ?", orgID).
		Select("max(position)").Scan(&highest).Error; err != nil || highest == nil {
		return 10
	}
	return *highest + 10
}

func validateTaskType(req taskTypeRequest) error {
	switch {
	case req.Label == "":
		return errors.New("label is required")
	case len(req.Label) > 100:
		return errors.New("label must be 100 characters or fewer")
	case !taskTypeKeyPattern.MatchString(req.Key):
		return errors.New("key must start with a letter and contain only lowercase letters, numbers and underscores")
	case len(req.Key) > 50:
		return errors.New("key must be 50 characters or fewer")
	}
	// A year is already absurd for a follow-up, and a negative offset would
	// create tasks that are overdue the moment they exist.
	if req.DefaultDueOffsetMinutes != nil &&
		(*req.DefaultDueOffsetMinutes < 0 || *req.DefaultDueOffsetMinutes > 525600) {
		return errors.New("the default deadline must be between 0 minutes and a year")
	}
	return nil
}

// slugifyKey derives a stable key from a label, so a settings form can ask for
// one thing instead of two. Shared with contact fields, which allow a longer
// key, hence the limit being a parameter.
func slugifyKey(label string, maxLen int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('_')
		}
	}
	key := strings.Trim(b.String(), "_")
	for strings.Contains(key, "__") {
		key = strings.ReplaceAll(key, "__", "_")
	}
	if key != "" && (key[0] < 'a' || key[0] > 'z') {
		key = "t_" + key
	}
	if len(key) > maxLen {
		key = key[:maxLen]
	}
	return key
}

func taskTypeAuditSnapshot(t *models.TaskType) map[string]any {
	return map[string]any{
		"key":                        t.Key,
		"label":                      t.Label,
		"icon":                       t.Icon,
		"color":                      t.Color,
		"default_due_offset_minutes": t.DefaultDueOffsetMinutes,
		"position":                   t.Position,
		"archived":                   t.ArchivedAt != nil,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
