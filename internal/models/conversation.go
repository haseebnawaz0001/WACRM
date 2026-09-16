package models

import (
	"time"

	"github.com/google/uuid"
)

// Conversation statuses (plan 03).
type ConversationStatus string

const (
	// ConversationOpen needs attention from us, bot or human.
	ConversationOpen ConversationStatus = "open"
	// ConversationPending means we replied and are waiting on the customer.
	ConversationPending ConversationStatus = "pending"
	// ConversationSnoozed is hidden until snoozed_until, or until the
	// customer writes again.
	ConversationSnoozed ConversationStatus = "snoozed"
	// ConversationResolved is done.
	ConversationResolved ConversationStatus = "resolved"
)

// IsActive reports whether the conversation is still live. Exactly one active
// conversation may exist per contact.
func (s ConversationStatus) IsActive() bool {
	return s != ConversationResolved
}

// Resolution reasons, recorded so reporting can tell an agent resolving a
// conversation apart from a timeout closing it.
const (
	ResolutionAgent            = "agent"
	ResolutionSLAAutoClose     = "sla_auto_close"
	ResolutionClientInactivity = "client_inactivity"
	ResolutionPendingTimeout   = "pending_timeout"
	ResolutionMerged           = "merged"
	ResolutionBulk             = "bulk"
	ResolutionAutomation       = "automation"
	ResolutionContactDeleted   = "contact_deleted"
)

// Conversation is one episode of contact with a customer (plan 03).
//
// The product previously had no conversation record at all: a contact was
// either "read" or not, and agent_transfers stood in for assignment. That left
// no way to say a conversation was finished, no response-time measurement, and
// no workload model — an agent could not see what was theirs and still open.
//
// A conversation is deliberately not a thread of messages. Messages point at it
// through messages.conversation_id; this row carries the state and the
// timestamps that reporting and SLA need.
type Conversation struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	ContactID      uuid.UUID `gorm:"type:uuid;not null;index" json:"contact_id"`

	Status ConversationStatus `gorm:"size:20;not null;default:'open'" json:"status"`

	// AssigneeID is who is handling the conversation now. It is distinct from
	// contacts.assigned_user_id, which is the long-lived relationship owner.
	AssigneeID *uuid.UUID `gorm:"type:uuid;index" json:"assignee_id,omitempty"`
	TeamID     *uuid.UUID `gorm:"type:uuid;index" json:"team_id,omitempty"`

	// BotActive means the chatbot is handling this with no human transfer.
	BotActive bool `gorm:"not null;default:true" json:"bot_active"`

	// The column is pinned: GORM would derive "whats_app_account" from the
	// field name, and plan 03 specifies whatsapp_account.
	WhatsAppAccount string `gorm:"column:whatsapp_account;size:100;not null;default:''" json:"whatsapp_account"`

	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	SnoozedByID  *uuid.UUID `gorm:"type:uuid" json:"snoozed_by_id,omitempty"`

	OpenedAt               time.Time  `gorm:"not null" json:"opened_at"`
	FirstCustomerMessageAt *time.Time `json:"first_customer_message_at,omitempty"`

	// FirstResponseAt is the first human agent reply after the conversation
	// opened. Only sender_type=agent counts, so a bot greeting or an automated
	// template cannot make response times look better than they were.
	FirstResponseAt  *time.Time `json:"first_response_at,omitempty"`
	FirstResponderID *uuid.UUID `gorm:"type:uuid" json:"first_responder_id,omitempty"`

	LastCustomerMessageAt *time.Time `json:"last_customer_message_at,omitempty"`
	LastAgentMessageAt    *time.Time `json:"last_agent_message_at,omitempty"`
	LastMessageAt         *time.Time `gorm:"index" json:"last_message_at,omitempty"`

	// WaitingSince is when the oldest unanswered customer message arrived.
	// Null means the ball is not in our court.
	WaitingSince *time.Time `json:"waiting_since,omitempty"`

	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	ResolvedByID     *uuid.UUID `gorm:"type:uuid" json:"resolved_by_id,omitempty"`
	ResolutionReason string     `gorm:"size:30" json:"resolution_reason,omitempty"`

	ReopenedCount int `gorm:"not null;default:0" json:"reopened_count"`
	MessageCount  int `gorm:"not null;default:0" json:"message_count"`

	// Relations
	Contact  *Contact `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Assignee *User    `gorm:"foreignKey:AssigneeID" json:"assignee,omitempty"`
	Team     *Team    `gorm:"foreignKey:TeamID" json:"team,omitempty"`
}

func (Conversation) TableName() string {
	return "conversations"
}

// IsWaitingOnUs reports whether a customer message is still unanswered.
func (c Conversation) IsWaitingOnUs() bool {
	return c.WaitingSince != nil
}
