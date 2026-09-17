// Package tasks owns follow-up work: what was promised, who owes it and when
// (plan 04).
//
// A shared inbox can only answer "what has arrived?". Everything an agent
// commits to outside that — call back tomorrow, send the quote on Friday —
// lived in their head or a private note, so it was invisible to everyone else
// and lost when they were away.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// ErrNotFound is returned when a task does not exist in the organization.
var ErrNotFound = errors.New("tasks: not found")

// ErrNotOpen is returned when an edit is attempted on a closed task. Editing
// one silently would rewrite history: the record says what was promised, and a
// finished promise is not re-negotiable. Reopen it first.
var ErrNotOpen = errors.New("tasks: task is not open")

// ErrTitleRequired is returned when an edit would leave a task unnamed.
var ErrTitleRequired = errors.New("tasks: title is required")

// DefaultReminderLead is how far before the deadline the owner is reminded
// when no explicit reminder is set.
const DefaultReminderLead = 15 * time.Minute

// Service creates and updates tasks.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// CreateInput describes a task to create.
type CreateInput struct {
	OrgID     uuid.UUID
	ContactID uuid.UUID

	// TypeKey selects the task type by key; TypeID takes precedence if set.
	TypeKey string
	TypeID  *uuid.UUID

	Title       string
	Description string

	// OwnerID defaults to the contact's owner, then to the creator: a task
	// with nobody responsible is a task nobody does.
	OwnerID   *uuid.UUID
	CreatedBy *uuid.UUID

	Priority string
	// DueAt defaults to now plus the type's default offset.
	DueAt    *time.Time
	AllDay   bool
	RemindAt *time.Time

	Source           string
	ConversationID   *uuid.UUID
	MessageID        *uuid.UUID
	DealID           *uuid.UUID
	CallLogID        *uuid.UUID
	AutomationRuleID *uuid.UUID

	// Location is the owner's timezone, used to place an all-day deadline at
	// the end of their day rather than the server's.
	Location *time.Location
}

// Create adds a task.
func (s *Service) Create(ctx context.Context, in CreateInput) (*models.Task, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("tasks: a task needs a title")
	}

	var contact models.Contact
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", in.ContactID, in.OrgID).
		First(&contact).Error; err != nil {
		return nil, fmt.Errorf("tasks: contact not found")
	}

	taskType, err := s.resolveType(ctx, in)
	if err != nil {
		return nil, err
	}

	owner, err := s.resolveOwner(ctx, in, contact)
	if err != nil {
		return nil, err
	}

	due := s.resolveDue(in, taskType)
	remind := in.RemindAt
	if remind == nil {
		// A reminder before the deadline is the point of a due date; without
		// one the owner only learns about it once it is already late.
		lead := due.Add(-DefaultReminderLead)
		remind = &lead
	}

	task := &models.Task{
		BaseModel:        models.BaseModel{ID: uuid.New()},
		OrganizationID:   in.OrgID,
		ContactID:        in.ContactID,
		ConversationID:   in.ConversationID,
		MessageID:        in.MessageID,
		DealID:           in.DealID,
		CallLogID:        in.CallLogID,
		TypeID:           taskType.ID,
		Title:            in.Title,
		Description:      in.Description,
		OwnerID:          owner,
		CreatedByID:      in.CreatedBy,
		Priority:         priorityOrNormal(in.Priority),
		Status:           models.TaskOpen,
		DueAt:            due,
		AllDay:           in.AllDay,
		RemindAt:         remind,
		Source:           sourceOrManual(in.Source),
		AutomationRuleID: in.AutomationRuleID,
	}

	// The task and its event are written together, so a task can never exist
	// without the timeline entry that announces it.
	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return s.publish(tx, task, "task.created", actorFor(in.CreatedBy))
	}); err != nil {
		return nil, err
	}

	// Attach the type we already resolved, so the caller can render the task
	// without a second query for something this function just looked up.
	task.Type = taskType
	return task, nil
}

// resolveType finds the task type by id or key.
func (s *Service) resolveType(ctx context.Context, in CreateInput) (*models.TaskType, error) {
	q := s.DB.WithContext(ctx).Where("organization_id = ?", in.OrgID)
	if in.TypeID != nil {
		q = q.Where("id = ?", *in.TypeID)
	} else {
		key := in.TypeKey
		if key == "" {
			key = models.TaskTypeFollowUp
		}
		q = q.Where("key = ?", key)
	}

	var taskType models.TaskType
	if err := q.First(&taskType).Error; err != nil {
		return nil, fmt.Errorf("tasks: unknown task type")
	}
	if taskType.ArchivedAt != nil {
		return nil, fmt.Errorf("tasks: task type %q is archived", taskType.Key)
	}
	return &taskType, nil
}

// resolveOwner decides who owes the work.
//
// The contact's owner comes first so follow-up stays with the person who knows
// the customer; the creator is the fallback. Assigning to nobody is not an
// option — an unowned task is never done.
func (s *Service) resolveOwner(ctx context.Context, in CreateInput, contact models.Contact) (uuid.UUID, error) {
	candidates := []*uuid.UUID{in.OwnerID, contact.AssignedUserID, in.CreatedBy}
	for _, candidate := range candidates {
		if candidate == nil || *candidate == uuid.Nil {
			continue
		}
		var user models.User
		if err := s.DB.WithContext(ctx).
			Where("id = ? AND organization_id = ? AND is_active = true", *candidate, in.OrgID).
			First(&user).Error; err == nil {
			return user.ID, nil
		}
	}
	return uuid.Nil, fmt.Errorf("tasks: no active owner available for this task")
}

// resolveDue picks the deadline.
func (s *Service) resolveDue(in CreateInput, taskType *models.TaskType) time.Time {
	due := time.Now().UTC().Add(time.Duration(taskType.DefaultDueOffsetMinutes) * time.Minute)
	if in.DueAt != nil {
		due = in.DueAt.UTC()
	}

	if in.AllDay {
		// End of the owner's day, not the server's: "due today" means their
		// today, and a server in another zone would move the deadline.
		loc := in.Location
		if loc == nil {
			loc = time.UTC
		}
		local := due.In(loc)
		due = time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, loc).UTC()
	}
	return due
}

// Complete marks a task done.
func (s *Service) Complete(ctx context.Context, orgID, taskID uuid.UUID, actor crmevents.Actor) (*models.Task, error) {
	return s.close(ctx, orgID, taskID, models.TaskCompleted, "task.completed", actor)
}

// Cancel marks a task cancelled.
func (s *Service) Cancel(ctx context.Context, orgID, taskID uuid.UUID, actor crmevents.Actor) (*models.Task, error) {
	return s.close(ctx, orgID, taskID, models.TaskCancelled, "task.cancelled", actor)
}

func (s *Service) close(ctx context.Context, orgID, taskID uuid.UUID, status, eventType string, actor crmevents.Actor) (*models.Task, error) {
	var task models.Task

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND organization_id = ?", taskID, orgID).First(&task).Error; err != nil {
			return ErrNotFound
		}
		if task.Status != models.TaskOpen {
			// Closing an already-closed task is a no-op rather than an error:
			// two clicks on Complete should not produce a failure.
			return nil
		}

		now := time.Now().UTC()
		updates := map[string]any{"status": status}
		if status == models.TaskCompleted {
			updates["completed_at"] = now
			updates["completed_by_id"] = actor.ID
		} else {
			updates["cancelled_at"] = now
		}

		if err := tx.Model(&models.Task{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		return s.publish(tx, &task, eventType, actor)
	})

	if err != nil {
		return nil, err
	}
	return &task, nil
}

// Reassign moves a task to a different owner.
func (s *Service) Reassign(ctx context.Context, orgID, taskID, newOwner uuid.UUID, actor crmevents.Actor) (*models.Task, error) {
	var user models.User
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ? AND is_active = true", newOwner, orgID).
		First(&user).Error; err != nil {
		return nil, fmt.Errorf("tasks: the new owner is not an active member of this organization")
	}

	var task models.Task
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND organization_id = ?", taskID, orgID).First(&task).Error; err != nil {
			return ErrNotFound
		}
		if err := tx.Model(&models.Task{}).Where("id = ?", taskID).
			Update("owner_id", newOwner).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		return s.publish(tx, &task, "task.updated", actor)
	})

	if err != nil {
		return nil, err
	}
	return &task, nil
}

// ListOpts filters a task query.
type ListOpts struct {
	// OwnerID restricts to one owner ("mine").
	OwnerID *uuid.UUID
	// ContactID restricts to one contact's tasks.
	ContactID *uuid.UUID
	// DealID restricts to the follow-ups attached to one deal (plan 07).
	DealID *uuid.UUID
	// Status defaults to open.
	Status string
	// OverdueOnly returns only open tasks past their deadline.
	OverdueOnly bool
	// DueBefore returns tasks due before a moment ("today", "this week").
	DueBefore *time.Time
	Limit     int
	Offset    int
}

// List returns tasks, soonest deadline first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, opts ListOpts) ([]models.Task, int64, error) {
	q := s.DB.WithContext(ctx).Model(&models.Task{}).
		Where("tasks.organization_id = ?", orgID).
		Where(liveContactOnly("tasks"))

	status := opts.Status
	if status == "" {
		status = models.TaskOpen
	}
	if status != "all" {
		q = q.Where("tasks.status = ?", status)
	}
	if opts.OwnerID != nil {
		q = q.Where("tasks.owner_id = ?", *opts.OwnerID)
	}
	if opts.ContactID != nil {
		q = q.Where("tasks.contact_id = ?", *opts.ContactID)
	}
	if opts.DealID != nil {
		q = q.Where("tasks.deal_id = ?", *opts.DealID)
	}
	if opts.OverdueOnly {
		// Overdue is derived, never stored: a stored flag would be wrong
		// between the moment a task lapses and the next job tick.
		q = q.Where("tasks.status = ? AND tasks.due_at < ?", models.TaskOpen, time.Now().UTC())
	}
	if opts.DueBefore != nil {
		q = q.Where("tasks.due_at < ?", *opts.DueBefore)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var out []models.Task
	if err := q.Preload("Type").Preload("Contact").
		Order("tasks.due_at ASC").
		Offset(opts.Offset).Limit(limit).
		Find(&out).Error; err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Get returns one task.
func (s *Service) Get(ctx context.Context, orgID, taskID uuid.UUID) (*models.Task, error) {
	var task models.Task
	if err := s.DB.WithContext(ctx).Preload("Type").Preload("Contact").
		Where("id = ? AND organization_id = ?", taskID, orgID).First(&task).Error; err != nil {
		return nil, ErrNotFound
	}
	return &task, nil
}

// publish records a task event in the caller's transaction.
func (s *Service) publish(tx *gorm.DB, task *models.Task, eventType string, actor crmevents.Actor) error {
	if !crmevents.IsKnown(eventType) {
		return nil
	}
	event := crmevents.New(task.OrganizationID, eventType, actor, map[string]any{
		"task_id":  task.ID.String(),
		"title":    task.Title,
		"owner_id": task.OwnerID.String(),
		"due_at":   task.DueAt.UTC(),
		"status":   task.Status,
	}).ForContact(task.ContactID).About(crmevents.SubjectTask, task.ID)

	return crmevents.PublishTx(tx, event)
}

func actorFor(userID *uuid.UUID) crmevents.Actor {
	if userID == nil {
		return crmevents.SystemActor()
	}
	return crmevents.UserActor(*userID, "")
}

func priorityOrNormal(p string) string {
	switch p {
	case models.TaskPriorityLow, models.TaskPriorityHigh:
		return p
	}
	return models.TaskPriorityNormal
}

func sourceOrManual(s string) string {
	switch s {
	case models.TaskSourceAutomation, models.TaskSourceAPI, models.TaskSourceChatbot,
		models.TaskSourceCall:
		return s
	}
	return models.TaskSourceManual
}

// liveContactOnly hides work attached to a deleted contact (plan 10, S2).
//
// Deleting a contact keeps their tasks and deals rather than destroying
// someone's to-do list, but a follow-up for a customer who no longer exists is
// not work anybody can do, and it should not sit in a list being counted,
// chased and reported on. Restoring the contact brings them back, which is why
// the rows are filtered rather than deleted.
func liveContactOnly(table string) string {
	return "EXISTS (SELECT 1 FROM contacts c WHERE c.id = " + table +
		".contact_id AND c.deleted_at IS NULL)"
}

// DueCounts is what a person can act on right now.
type DueCounts struct {
	// Overdue tasks have passed their deadline.
	Overdue int64 `json:"overdue"`
	// DueToday are due before the end of the day in the owner's timezone.
	DueToday int64 `json:"due_today"`
}

// Total is the sidebar badge: what is late plus what is due today (plan 04).
//
// Deliberately not "all open tasks". A badge showing forty because somebody
// planned forty follow-ups over the next month is a badge people learn to
// ignore; a badge showing three because three are actually due is a prompt.
func (d DueCounts) Total() int64 { return d.Overdue + d.DueToday }

// DueFor counts one owner's overdue and due-today tasks.
//
// One query with two filtered aggregates rather than two round trips: this
// runs on every page load to fill a badge, and a badge is not worth two
// queries.
func (s *Service) DueFor(ctx context.Context, orgID, ownerID uuid.UUID, loc *time.Location) (DueCounts, error) {
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().UTC()
	// End of today in the owner's timezone: "due today" is a statement about
	// their calendar, not the server's.
	local := now.In(loc)
	endOfDay := time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, loc).UTC()

	var out DueCounts
	err := s.DB.WithContext(ctx).Model(&models.Task{}).
		Select(`count(*) FILTER (WHERE tasks.due_at < ?) AS overdue,
		        count(*) FILTER (WHERE tasks.due_at >= ? AND tasks.due_at <= ?) AS due_today`,
			now, now, endOfDay).
		Where("tasks.organization_id = ? AND tasks.owner_id = ? AND tasks.status = ?",
			orgID, ownerID, models.TaskOpen).
		Where(liveContactOnly("tasks")).
		Scan(&out).Error
	return out, err
}

// UpdateInput is what a person may change about an existing task.
//
// Every field is a pointer so "leave it alone" and "set it to empty" are
// different instructions. A form that always sends every field would otherwise
// blank a description the user never touched.
type UpdateInput struct {
	Title       *string
	Description *string
	Priority    *string
	DueAt       *time.Time
	AllDay      *bool
	RemindAt    *time.Time
	// ClearRemind removes the reminder, which a nil RemindAt cannot express.
	ClearRemind bool
}

// Update edits a task: renaming it, rescheduling it, changing its priority.
//
// Rescheduling clears the reminder claim, so a task moved from this morning to
// next week reminds its owner again rather than staying silent because the
// notifier already fired once for the old deadline.
func (s *Service) Update(ctx context.Context, orgID, taskID uuid.UUID, in UpdateInput, actor crmevents.Actor) (*models.Task, error) {
	var task models.Task

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND organization_id = ?", taskID, orgID).First(&task).Error; err != nil {
			return ErrNotFound
		}
		if task.Status != models.TaskOpen {
			return ErrNotOpen
		}

		updates := map[string]any{}
		if in.Title != nil {
			title := strings.TrimSpace(*in.Title)
			if title == "" {
				return ErrTitleRequired
			}
			updates["title"] = title
		}
		if in.Description != nil {
			updates["description"] = *in.Description
		}
		if in.Priority != nil {
			updates["priority"] = *in.Priority
		}
		if in.AllDay != nil {
			updates["all_day"] = *in.AllDay
		}
		if in.DueAt != nil {
			updates["due_at"] = in.DueAt.UTC()
			// A new deadline is a new promise: the reminder and the overdue
			// notice both get to fire again.
			updates["reminder_sent_at"] = nil
			updates["overdue_notified_at"] = nil
		}
		switch {
		case in.ClearRemind:
			updates["remind_at"] = nil
		case in.RemindAt != nil:
			updates["remind_at"] = in.RemindAt.UTC()
			updates["reminder_sent_at"] = nil
		}

		if len(updates) == 0 {
			return nil
		}

		if err := tx.Model(&models.Task{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		return s.publish(tx, &task, "task.updated", actor)
	})

	if err != nil {
		return nil, err
	}
	return &task, nil
}

// Reopen puts a completed or cancelled task back on someone's list.
//
// Completing a task by mistake is a click; without a way back the only remedy
// was to create a second task, which loses the history of the first.
func (s *Service) Reopen(ctx context.Context, orgID, taskID uuid.UUID, actor crmevents.Actor) (*models.Task, error) {
	var task models.Task

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND organization_id = ?", taskID, orgID).First(&task).Error; err != nil {
			return ErrNotFound
		}
		if task.Status == models.TaskOpen {
			// Already open: reopening twice is a no-op, not a failure.
			return nil
		}

		if err := tx.Model(&models.Task{}).Where("id = ?", taskID).
			Updates(map[string]any{
				"status":          models.TaskOpen,
				"completed_at":    nil,
				"completed_by_id": nil,
				"cancelled_at":    nil,
				// The notices belong to the closed life of this task; a
				// reopened one has not been chased yet.
				"reminder_sent_at":    nil,
				"overdue_notified_at": nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		return s.publish(tx, &task, "task.updated", actor)
	})

	if err != nil {
		return nil, err
	}
	return &task, nil
}
