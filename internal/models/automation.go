package models

import (
	"time"

	"github.com/google/uuid"
)

// Automation run statuses (plan 08).
const (
	AutomationSucceeded       = "succeeded"
	AutomationPartiallyFailed = "partially_failed"
	AutomationFailed          = "failed"
	AutomationSkipped         = "skipped"
	// AutomationWaiting is a run paused at a wait step. It finishes when the
	// wait is over, as the same run, so the history reads as one journey.
	AutomationWaiting = "waiting"
	// AutomationCancelled is a paused run that could not carry on: the rule
	// was switched off, the step it waited at was removed, or the contact is
	// gone.
	AutomationCancelled = "cancelled"
)

// Why a run did nothing. The reason is stored rather than inferred, because
// "the rule did not fire" has half a dozen very different explanations and the
// person debugging needs to know which one.
const (
	SkipConditionsNotMet = "conditions_not_met"
	SkipTriggerNotMet    = "trigger_not_met"
	SkipCooldown         = "cooldown"
	SkipOncePerContact   = "once_per_contact"
	SkipRateLimited      = "rate_limited"
	SkipLoopDepth        = "loop_depth"
	SkipSelfTrigger      = "self_trigger"
	SkipDisabled         = "disabled"
	// Why a paused run was cancelled rather than resumed.
	CancelRuleOff     = "rule_off"
	CancelStepRemoved = "step_removed"
	CancelContactGone = "contact_gone"
	CancelRuleDeleted = "rule_deleted"
)

// AutomationRule is one "when this happens, do that" rule.
type AutomationRule struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	Name        string `gorm:"size:150;not null" json:"name"`
	Description string `gorm:"size:500;not null;default:''" json:"description"`

	// Enabled defaults to false: a rule that starts messaging customers the
	// moment it is saved leaves no room to check it first.
	Enabled bool `gorm:"not null;default:false" json:"enabled"`

	TriggerType   string `gorm:"size:64;not null;index" json:"trigger_type"`
	TriggerConfig JSONB  `gorm:"type:jsonb;not null;default:'{}'" json:"trigger_config"`

	// ContactFilter is an F6 filter AST, evaluated against the contact at run
	// time rather than at save time — the answer changes as the contact does.
	ContactFilter JSONB `gorm:"type:jsonb" json:"contact_filter,omitempty"`

	// Actions is the ordered list the rule performs.
	Actions JSONB `gorm:"type:jsonb;not null;default:'[]'" json:"actions"`

	// RunPolicy holds once_per_contact, cooldown_minutes and max_runs_per_hour.
	RunPolicy JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"run_policy"`

	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`
	UpdatedByID *uuid.UUID `gorm:"type:uuid" json:"updated_by_id,omitempty"`

	LastRunAt  *time.Time `json:"last_run_at,omitempty"`
	RunCount   int64      `gorm:"not null;default:0" json:"run_count"`
	ErrorCount int64      `gorm:"not null;default:0" json:"error_count"`
	// ConsecutiveFailures drives auto-disable: a rule failing every time is
	// usually broken, and leaving it running just fills the log.
	ConsecutiveFailures int `gorm:"not null;default:0" json:"consecutive_failures"`
}

func (AutomationRule) TableName() string { return "automation_rules" }

// AutomationRun is one attempt at one rule for one event.
//
// It exists for the person asking "why did my customer get that message?".
// Without a durable record the only answer is a guess.
type AutomationRun struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	RuleID         uuid.UUID `gorm:"type:uuid;not null;index" json:"rule_id"`

	// EventID makes a run idempotent: redelivering the same stream entry
	// conflicts on the unique index rather than acting twice.
	EventID   uuid.UUID  `gorm:"type:uuid;not null" json:"event_id"`
	EventType string     `gorm:"size:64;not null" json:"event_type"`
	ContactID *uuid.UUID `gorm:"type:uuid;index" json:"contact_id,omitempty"`

	Status     string `gorm:"size:20;not null" json:"status"`
	SkipReason string `gorm:"size:50" json:"skip_reason,omitempty"`

	// Depth is how long the causal chain was when this run started.
	Depth int `gorm:"not null;default:0" json:"depth"`

	ActionResults JSONB `gorm:"type:jsonb;not null;default:'[]'" json:"action_results"`
	DryRun        bool  `gorm:"not null;default:false" json:"dry_run"`

	StartedAt  time.Time  `gorm:"not null" json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (AutomationRun) TableName() string { return "automation_runs" }

// AutomationContactState remembers what a rule has already done to a contact,
// which is what makes "once per contact" and cooldowns possible.
type AutomationContactState struct {
	RuleID    uuid.UUID `gorm:"type:uuid;primaryKey" json:"rule_id"`
	ContactID uuid.UUID `gorm:"type:uuid;primaryKey" json:"contact_id"`
	// SubjectKey separates repeats that are genuinely about different things:
	// a time trigger re-arms per conversation and per waiting period.
	SubjectKey string `gorm:"size:100;primaryKey;default:''" json:"subject_key"`

	LastRunAt time.Time `gorm:"not null" json:"last_run_at"`
	RunCount  int       `gorm:"not null;default:0" json:"run_count"`
}

func (AutomationContactState) TableName() string { return "automation_contact_state" }

// Automation wait statuses.
const (
	WaitPending   = "pending"
	WaitResumed   = "resumed"
	WaitCancelled = "cancelled"
)

// AutomationWait is a run parked at a wait step until ResumeAt.
//
// "Wait a day, then check in" is half of how people describe a process, and it
// has to survive a restart, a deploy and a second replica: a timer in memory
// would silently forget every customer waiting when the process went down. The
// row carries what the run needs to pick up where it stopped — the event that
// started it (its variables and loop depth) and the step it is waiting at —
// and the step is found by id in the rule as it is when the wait ends, so an
// edit made in the meantime applies to what the contact has not reached yet.
type AutomationWait struct {
	ID             uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"organization_id"`
	RuleID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"rule_id"`
	RunID          uuid.UUID  `gorm:"type:uuid;not null;index" json:"run_id"`
	ContactID      *uuid.UUID `gorm:"type:uuid;index" json:"contact_id,omitempty"`

	// StepID is the wait step the run is parked at.
	StepID string `gorm:"size:64;not null" json:"step_id"`
	// Event is the triggering event, serialised: the resumed steps render the
	// same variables and carry the same loop depth as the first half.
	Event JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"event"`

	ResumeAt time.Time `gorm:"not null;index:idx_automation_waits_due,where:status = 'pending'" json:"resume_at"`
	Status   string    `gorm:"size:20;not null;default:'pending'" json:"status"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (AutomationWait) TableName() string { return "automation_waits" }
