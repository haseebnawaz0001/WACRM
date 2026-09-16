package models

import (
	"time"

	"github.com/google/uuid"
)

// Notification types.
const (
	NotificationTaskDue                 = "task_due"
	NotificationTaskOverdue             = "task_overdue"
	NotificationTaskAssigned            = "task_assigned"
	NotificationConversationAssigned    = "conversation_assigned"
	NotificationConversationSnoozeEnded = "conversation_snooze_ended"
	NotificationSLAEscalation           = "sla_escalation"
	NotificationAutomation              = "automation"
	NotificationMergeSuggestions        = "merge_suggestions"
	NotificationDealRotting             = "deal_rotting"
	NotificationCampaignPaused          = "campaign_paused"
)

// Notification is one in-app notification for one user (plan 00, F5).
//
// Everything notifying an agent before this was a transient toast fired from a
// WebSocket handler: nothing was stored, so anything that arrived while the
// agent was away or on another screen was simply lost, and there was no way to
// catch up.
//
// There is no permission resource for notifications. A row belongs to exactly
// one user and only that user can read it.
type Notification struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index:idx_notifications_user,priority:2" json:"organization_id"`
	UserID         uuid.UUID `gorm:"type:uuid;not null;index:idx_notifications_user,priority:1" json:"user_id"`

	Type  string `gorm:"size:50;not null" json:"type"`
	Title string `gorm:"size:255;not null" json:"title"`
	Body  string `gorm:"type:text;not null;default:''" json:"body"`

	// Link is the frontend route to open, e.g. /contacts/<id>?tab=tasks.
	Link string `gorm:"size:500;not null;default:''" json:"link"`

	EntityType string     `gorm:"size:32" json:"entity_type,omitempty"`
	EntityID   *uuid.UUID `gorm:"type:uuid" json:"entity_id,omitempty"`

	Data JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"data"`

	// ReadAt is nil until the user has seen it.
	ReadAt *time.Time `gorm:"index:idx_notifications_user,priority:3" json:"read_at,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_notifications_user,priority:4,sort:desc" json:"created_at"`
}

func (Notification) TableName() string {
	return "notifications"
}
