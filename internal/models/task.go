package models

import (
	"time"

	"github.com/google/uuid"
)

// Task statuses (plan 04).
const (
	TaskOpen      = "open"
	TaskCompleted = "completed"
	TaskCancelled = "cancelled"
)

// Task priorities.
const (
	TaskPriorityLow    = "low"
	TaskPriorityNormal = "normal"
	TaskPriorityHigh   = "high"
)

// Where a task came from.
const (
	TaskSourceManual     = "manual"
	TaskSourceAutomation = "automation"
	TaskSourceAPI        = "api"
	TaskSourceChatbot    = "chatbot"
	// TaskSourceCall is a follow-up created from a call's outcome.
	TaskSourceCall = "call"
)

// Built-in task type keys, seeded for every organization.
const (
	TaskTypeCallBack  = "call_back"
	TaskTypeSendQuote = "send_quote"
	TaskTypeCheckIn   = "check_in"
	TaskTypeFollowUp  = "follow_up"
	TaskTypeOther     = "other"
)

// TaskType is an organization-configurable kind of task (plan 04).
//
// Types carry a default due offset so "call back" and "send quote" can suggest
// different deadlines, which is the difference between a task list people use
// and one they fill in twice.
type TaskType struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	Key   string `gorm:"size:50;not null" json:"key"`
	Label string `gorm:"size:100;not null" json:"label"`
	Icon  string `gorm:"size:50;not null;default:'check-square'" json:"icon"`
	Color string `gorm:"size:20;not null;default:'gray'" json:"color"`

	// DefaultDueOffsetMinutes is how far ahead a new task of this type is due.
	DefaultDueOffsetMinutes int `gorm:"not null;default:1440" json:"default_due_offset_minutes"`

	// IsSystem types can be relabelled or archived but not deleted, because
	// product features create tasks of these kinds by key.
	IsSystem   bool       `gorm:"not null;default:false" json:"is_system"`
	Position   int        `gorm:"not null;default:0" json:"position"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
}

func (TaskType) TableName() string {
	return "task_types"
}

// Task is a piece of follow-up work owned by someone (plan 04).
//
// A shared inbox can only answer "what has arrived?". A task list answers "what
// did we promise, and who owes it?" — which is the difference between handling
// messages and running a CRM.
type Task struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	ContactID      uuid.UUID `gorm:"type:uuid;not null;index" json:"contact_id"`

	// Optional context for where the task came from.
	ConversationID *uuid.UUID `gorm:"type:uuid" json:"conversation_id,omitempty"`
	DealID         *uuid.UUID `gorm:"type:uuid;index" json:"deal_id,omitempty"`
	MessageID      *uuid.UUID `gorm:"type:uuid" json:"message_id,omitempty"`
	// CallLogID links a call-back to the call that caused it (plan 10, 4.4),
	// so completing it from the call outcome dialog knows which task it is and
	// the timeline can show the call and the follow-up as one thread.
	CallLogID *uuid.UUID `gorm:"type:uuid;index" json:"call_log_id,omitempty"`

	TypeID      uuid.UUID `gorm:"type:uuid;not null" json:"type_id"`
	Title       string    `gorm:"size:255;not null" json:"title"`
	Description string    `gorm:"type:text;not null;default:''" json:"description"`

	// OwnerID is who owes the work. A task with no owner is a task nobody does.
	OwnerID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"owner_id"`
	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`

	Priority string `gorm:"size:10;not null;default:'normal'" json:"priority"`
	Status   string `gorm:"size:20;not null;default:'open'" json:"status"`

	DueAt time.Time `gorm:"not null;index" json:"due_at"`
	// AllDay tasks are due at end of day in the owner's timezone, stored in UTC.
	AllDay bool `gorm:"not null;default:false" json:"all_day"`
	// RemindAt is when to notify the owner ahead of the deadline.
	RemindAt *time.Time `json:"remind_at,omitempty"`

	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	CompletedByID *uuid.UUID `gorm:"type:uuid" json:"completed_by_id,omitempty"`
	CancelledAt   *time.Time `json:"cancelled_at,omitempty"`

	Source           string     `gorm:"size:20;not null;default:'manual'" json:"source"`
	AutomationRuleID *uuid.UUID `gorm:"type:uuid" json:"automation_rule_id,omitempty"`

	// These record that a notification happened, so it is not repeated.
	// Overdue itself is derived from status and due_at, never stored: a stored
	// flag would need a job to keep it true and would be wrong between ticks.
	ReminderSentAt    *time.Time `json:"reminder_sent_at,omitempty"`
	OverdueNotifiedAt *time.Time `json:"overdue_notified_at,omitempty"`

	// Relations
	Contact *Contact  `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Type    *TaskType `gorm:"foreignKey:TypeID" json:"type,omitempty"`
	Owner   *User     `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
}

func (Task) TableName() string {
	return "tasks"
}

// IsOverdue reports whether an open task is past its deadline.
func (t Task) IsOverdue(now time.Time) bool {
	return t.Status == TaskOpen && t.DueAt.Before(now)
}

// IsOpen reports whether the task still needs doing.
func (t Task) IsOpen() bool {
	return t.Status == TaskOpen
}
