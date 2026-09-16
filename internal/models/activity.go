package models

import (
	"time"

	"github.com/google/uuid"
)

// ContactActivity is one entry in a contact's history (plan 00, F3).
//
// It is not audit_logs. That table is polymorphic on (resource_type,
// resource_id), has no contact_id, stores field diffs for admin auditing and is
// written from a goroutine that a crash can lose. Activity on a contact's child
// records — a tag, a transfer, later a task or a deal — cannot be attributed
// back to the contact through it. audit_logs remains the admin trail; this is
// the customer-facing history the timeline reads.
//
// Rows are immutable and never soft-deleted: a history you can edit is not a
// history.
type ContactActivity struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index:idx_contact_activities_timeline,priority:1" json:"organization_id"`
	ContactID      uuid.UUID `gorm:"type:uuid;not null;index:idx_contact_activities_timeline,priority:2" json:"contact_id"`

	// Type is the crmevents catalog name, e.g. "contact.tag_added".
	Type string `gorm:"size:64;not null" json:"type"`

	ActorType string     `gorm:"size:20;not null" json:"actor_type"`
	ActorID   *uuid.UUID `gorm:"type:uuid" json:"actor_id,omitempty"`
	ActorName string     `gorm:"size:255;not null;default:''" json:"actor_name"`

	SubjectType string     `gorm:"size:32" json:"subject_type,omitempty"`
	SubjectID   *uuid.UUID `gorm:"type:uuid" json:"subject_id,omitempty"`

	// Data holds what changed, e.g. {"tag":"VIP"} or {"from":"lead","to":"customer"}.
	Data JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"data"`

	OccurredAt time.Time `gorm:"not null;index:idx_contact_activities_timeline,priority:3,sort:desc" json:"occurred_at"`
	CreatedAt  time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (ContactActivity) TableName() string {
	return "contact_activities"
}
