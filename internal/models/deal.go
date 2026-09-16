package models

import (
	"time"

	"github.com/google/uuid"
)

// Deal and stage statuses (plan 07).
const (
	DealOpen = "open"
	DealWon  = "won"
	DealLost = "lost"
)

// Stage types. A stage is where a deal sits; its type is what that position
// means, so "Closed — won" and "Signed" can both mean won.
const (
	StageOpen = "open"
	StageWon  = "won"
	StageLost = "lost"
)

// Pipeline is one sales or service process (plan 07).
//
// The object labels are configurable because not every organization sells: the
// same board runs cases, appointments and bookings, and calling those "deals"
// makes the product read as someone else's tool.
type Pipeline struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	Name                string `gorm:"size:100;not null" json:"name"`
	ObjectLabelSingular string `gorm:"size:40;not null;default:'Deal'" json:"object_label_singular"`
	ObjectLabelPlural   string `gorm:"size:40;not null;default:'Deals'" json:"object_label_plural"`

	Currency  string `gorm:"type:char(3);not null;default:'USD'" json:"currency"`
	IsDefault bool   `gorm:"not null;default:false" json:"is_default"`
	Position  int    `gorm:"not null;default:0" json:"position"`

	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`

	Stages []PipelineStage `gorm:"foreignKey:PipelineID" json:"stages,omitempty"`
}

func (Pipeline) TableName() string {
	return "pipelines"
}

// PipelineStage is one column of a board.
type PipelineStage struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	PipelineID     uuid.UUID `gorm:"type:uuid;not null;index" json:"pipeline_id"`

	Name     string `gorm:"size:100;not null" json:"name"`
	Position int    `gorm:"not null" json:"position"`

	// StageType decides what reaching this stage means for the deal.
	StageType string `gorm:"size:10;not null;default:'open'" json:"stage_type"`
	// Probability drives weighted pipeline totals, so a forecast is not just
	// the sum of everything anyone ever hoped for.
	Probability int    `gorm:"not null;default:0" json:"probability"`
	Color       string `gorm:"size:20;not null;default:'gray'" json:"color"`

	// RottingDays flags a card that has not moved. 0 turns it off.
	RottingDays int        `gorm:"not null;default:0" json:"rotting_days"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
}

func (PipelineStage) TableName() string {
	return "pipeline_stages"
}

// Deal is one opportunity moving through a pipeline.
type Deal struct {
	BaseModel
	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"organization_id"`
	PipelineID     uuid.UUID  `gorm:"type:uuid;not null;index" json:"pipeline_id"`
	StageID        uuid.UUID  `gorm:"type:uuid;not null;index" json:"stage_id"`
	ContactID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"contact_id"`
	ConversationID *uuid.UUID `gorm:"type:uuid" json:"conversation_id,omitempty"`

	Title string `gorm:"size:255;not null" json:"title"`
	// Value is numeric in the database so sums are exact; float64 is only the
	// Go representation.
	Value    float64 `gorm:"type:numeric(14,2);not null;default:0" json:"value"`
	Currency string  `gorm:"type:char(3);not null" json:"currency"`

	OwnerID           *uuid.UUID `gorm:"type:uuid;index" json:"owner_id,omitempty"`
	ExpectedCloseDate *time.Time `gorm:"type:date" json:"expected_close_date,omitempty"`
	Status            string     `gorm:"size:10;not null;default:'open'" json:"status"`

	// StageEnteredAt is when the deal reached its current stage, which is what
	// rotting is measured from — not when the deal was created.
	StageEnteredAt time.Time `gorm:"not null" json:"stage_entered_at"`
	// BoardPosition orders cards within a column. It is a sortable string
	// rather than an integer so dropping a card between two others writes one
	// row instead of renumbering the whole column.
	BoardPosition string     `gorm:"size:64;not null;default:'n'" json:"board_position"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
	LostReason    string     `gorm:"size:255" json:"lost_reason,omitempty"`

	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`

	Contact *Contact       `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Stage   *PipelineStage `gorm:"foreignKey:StageID" json:"stage,omitempty"`
}

func (Deal) TableName() string {
	return "deals"
}

// IsOpen reports whether the deal is still in play.
func (d Deal) IsOpen() bool { return d.Status == DealOpen }

// IsRotting reports whether the deal has sat in its stage past the limit.
func (d Deal) IsRotting(stage PipelineStage, now time.Time) bool {
	if stage.RottingDays <= 0 || d.Status != DealOpen {
		return false
	}
	return now.Sub(d.StageEnteredAt) > time.Duration(stage.RottingDays)*24*time.Hour
}

// DealStageHistory records every move, so a funnel report can measure how long
// deals actually spend in each stage rather than guessing from the current one.
type DealStageHistory struct {
	ID             uuid.UUID  `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID  `gorm:"type:uuid;not null;index" json:"organization_id"`
	DealID         uuid.UUID  `gorm:"type:uuid;not null;index" json:"deal_id"`
	FromStageID    *uuid.UUID `gorm:"type:uuid" json:"from_stage_id,omitempty"`
	ToStageID      uuid.UUID  `gorm:"type:uuid;not null" json:"to_stage_id"`
	MovedByID      *uuid.UUID `gorm:"type:uuid" json:"moved_by_id,omitempty"`
	// DurationSeconds is how long the deal spent in the stage it just left.
	DurationSeconds int64     `gorm:"not null;default:0" json:"duration_seconds"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (DealStageHistory) TableName() string {
	return "deal_stage_history"
}
