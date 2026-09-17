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

// ConversationHandling says who is dealing with a conversation (plan 10, S5).
//
// It is derived from real state rather than set independently: an active
// transfer or assignee means human, an active chatbot session means bot, and a
// handoff the product could not complete means handoff_pending.
type ConversationHandling string

const (
	// HandlingBot: the chatbot is answering, with no human involved.
	HandlingBot ConversationHandling = "bot"
	// HandlingHuman: an agent or team owns it.
	HandlingHuman ConversationHandling = "human"
	// HandlingHandoffPending: a handoff was requested and suppressed — out of
	// hours, or no agent available. These appear in Unassigned with a chip
	// saying why, because a customer who asked for a person is waiting for one.
	HandlingHandoffPending ConversationHandling = "handoff_pending"
	// HandlingNone: nobody is handling it. Reached when the bot is off and no
	// handoff has been asked for.
	HandlingNone ConversationHandling = "none"
)

// NeedsAHuman reports whether the conversation is waiting for a person, either
// because one was asked for or because nothing else is handling it.
func (h ConversationHandling) NeedsAHuman() bool {
	return h == HandlingHandoffPending || h == HandlingNone
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

	// Handling says who is dealing with this conversation right now.
	//
	// It replaces the boolean bot_active (plan 10, S5), which could not
	// express the state that matters most operationally: a handoff was asked
	// for and did not happen. Out of hours, or with no agent available, the
	// old code left bot_active false and no assignee, which looked exactly
	// like an ordinary unassigned conversation — so nobody could tell the
	// difference between "waiting in the queue" and "the customer asked for a
	// human and the request evaporated".
	Handling ConversationHandling `gorm:"size:20;not null;default:'bot';index" json:"handling"`

	// The column is pinned: GORM would derive "whats_app_account" from the
	// field name, and plan 03 specifies whatsapp_account.
	WhatsAppAccount string `gorm:"column:whatsapp_account;size:100;not null;default:''" json:"whatsapp_account"`

	// OriginCampaignID attributes a conversation to the campaign that started
	// it (plan 10, §4.5).
	//
	// Without it a campaign could report how many messages went out and
	// nothing about what came back, so "did that blast work?" was unanswerable
	// — which is the only question anybody asks about a campaign.
	OriginCampaignID *uuid.UUID `gorm:"type:uuid;index" json:"origin_campaign_id,omitempty"`

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

// IsBotHandled reports whether the chatbot is dealing with this conversation.
func (c Conversation) IsBotHandled() bool { return c.Handling == HandlingBot }

// IsWaitingOnUs reports whether a customer message is still unanswered.
func (c Conversation) IsWaitingOnUs() bool {
	return c.WaitingSince != nil
}

// ConversationRead is one user's read position in one conversation
// (plan 10, S5).
//
// Read state used to be a single flag on the contact, which meant it belonged
// to whoever opened the chat last. A supervisor glancing at a queue cleared the
// badge for the agent who owned it, and an agent could not tell an unanswered
// customer from one a colleague had already picked up. Read is a fact about a
// person, not about a conversation, so it is stored per person.
type ConversationRead struct {
	ConversationID uuid.UUID `gorm:"type:uuid;primaryKey" json:"conversation_id"`
	UserID         uuid.UUID `gorm:"type:uuid;primaryKey" json:"user_id"`

	// LastReadAt is the timestamp of the newest message this user has seen.
	// Messages after it are unread for them and nobody else.
	LastReadAt time.Time `gorm:"not null" json:"last_read_at"`

	UpdatedAt time.Time `json:"updated_at"`
}

func (ConversationRead) TableName() string {
	return "conversation_reads"
}
