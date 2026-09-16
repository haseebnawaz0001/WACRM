package models

import (
	"time"

	"github.com/google/uuid"
)

// Segment visibility.
const (
	// SegmentShared is visible to the whole organization.
	SegmentShared = "shared"
	// SegmentPrivate is visible only to its creator.
	SegmentPrivate = "private"
)

// Segment is a saved contact filter (plan 05).
//
// The same audience gets described repeatedly — "customers in London who have
// not replied in 30 days" — once for a campaign, again for a report, again next
// month. Saving the filter rather than the resulting list means the segment
// stays correct as contacts change, which a static list cannot.
type Segment struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	Name        string `gorm:"size:120;not null" json:"name"`
	Description string `gorm:"size:500;not null;default:''" json:"description"`

	// Filter is the contactquery AST, validated on save so a segment cannot
	// store a filter that would fail when someone later runs it.
	Filter JSONB `gorm:"type:jsonb;not null" json:"filter"`

	Visibility  string     `gorm:"size:10;not null;default:'shared'" json:"visibility"`
	CreatedByID uuid.UUID  `gorm:"type:uuid;not null" json:"created_by_id"`
	UpdatedByID *uuid.UUID `gorm:"type:uuid" json:"updated_by_id,omitempty"`

	// ContactCount is cached because counting a segment is a full filter query,
	// and the list shows a count for every segment at once. CountedAt says how
	// stale it is, so the UI can be honest about it rather than implying the
	// number is live.
	ContactCount *int       `json:"contact_count,omitempty"`
	CountedAt    *time.Time `json:"counted_at,omitempty"`

	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (Segment) TableName() string {
	return "segments"
}

// IsPrivate reports whether only the creator may see the segment.
func (s Segment) IsPrivate() bool {
	return s.Visibility == SegmentPrivate
}
