package models

import (
	"time"

	"github.com/google/uuid"
)

// CRMEventOutbox is the transactional outbox for domain events (plan 10, S3).
//
// Events are inserted in the same database transaction as the change that
// caused them, so a crash can never leave the change committed without its
// event. A relay then fans each row out to the Redis event stream, the
// WebSocket fan-out channel and any subscribed webhooks, and marks it
// published. Rows are pruned once they are older than the retention window.
//
// Producers that have no access to the HTTP App — the campaign worker, the
// calling manager, scheduler jobs — publish by inserting a row here; they only
// need a *gorm.DB.
type CRMEventOutbox struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	// Type is the dotted catalog name, e.g. "contact.tag_added". The same
	// string is used for the activity type, the outbound webhook event and
	// the automation trigger.
	Type string `gorm:"size:64;not null;index" json:"type"`

	// ContactID is set whenever the event belongs to a contact's history.
	ContactID *uuid.UUID `gorm:"type:uuid;index" json:"contact_id,omitempty"`

	// Subject identifies the record the event is about, which is not always
	// the contact (a transfer, a task, a deal...).
	SubjectType string     `gorm:"size:32" json:"subject_type,omitempty"`
	SubjectID   *uuid.UUID `gorm:"type:uuid" json:"subject_id,omitempty"`

	// Actor is who caused the event: user | system | automation | contact | api.
	ActorType string     `gorm:"size:20;not null" json:"actor_type"`
	ActorID   *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	ActorName string     `gorm:"size:255" json:"actor_name,omitempty"`

	// Origin carries automation loop protection: which rule produced this
	// event and how deep the causal chain already is.
	OriginRuleID *uuid.UUID `gorm:"type:uuid" json:"origin_rule_id,omitempty"`
	OriginDepth  int        `gorm:"not null;default:0" json:"origin_depth"`

	// Data is the event payload delivered to webhook subscribers.
	Data JSONB `gorm:"type:jsonb;default:'{}'" json:"data"`

	OccurredAt time.Time `gorm:"not null;index" json:"occurred_at"`

	// PublishedAt is nil until the relay has fanned the event out. The relay
	// claims rows with FOR UPDATE SKIP LOCKED, so several replicas can relay
	// concurrently without delivering an event twice.
	PublishedAt *time.Time `gorm:"index" json:"published_at,omitempty"`

	// Attempts and LastError record sink failures. A sink failure does not
	// hold up the batch; the row is still marked published so one broken
	// webhook endpoint cannot stall the whole outbox.
	Attempts  int    `gorm:"not null;default:0" json:"attempts"`
	LastError string `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (CRMEventOutbox) TableName() string {
	return "crm_event_outbox"
}

// WebhookDelivery logs every outbound webhook attempt (plan 00, F11).
//
// Deliveries were previously fire-and-forget goroutines, so nothing survived a
// restart and receivers had no way to ask what had been sent. Each row records
// the delivery_id that went out in the payload envelope, so a receiver can
// correlate what it got with what was attempted.
type WebhookDelivery struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	WebhookID      uuid.UUID `gorm:"type:uuid;not null;index:idx_webhook_deliveries_hook" json:"webhook_id"`

	// EventID is the crm_event_outbox row this delivery came from. It is nil
	// for legacy call sites that still dispatch without an outbox row.
	EventID   *uuid.UUID `gorm:"type:uuid;index" json:"event_id,omitempty"`
	EventType string     `gorm:"size:64;not null" json:"event_type"`

	URL        string `gorm:"type:text;not null" json:"url"`
	StatusCode int    `gorm:"not null;default:0" json:"status_code"`
	Success    bool   `gorm:"not null;default:false" json:"success"`
	Attempts   int    `gorm:"not null;default:0" json:"attempts"`
	DurationMs int64  `gorm:"not null;default:0" json:"duration_ms"`
	Error      string `gorm:"type:text" json:"error,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_webhook_deliveries_hook" json:"created_at"`
}

func (WebhookDelivery) TableName() string {
	return "webhook_deliveries"
}
