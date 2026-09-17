package deals

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// DefaultColumnPage is how many cards a column loads at a time. A stage with
// four hundred deals must not send four hundred cards to render six.
const DefaultColumnPage = 50

// BoardFilter narrows what the board shows. The filters apply server-side so a
// column's count and total describe what the user is looking at rather than
// the whole pipeline.
type BoardFilter struct {
	OwnerID   *uuid.UUID
	CloseFrom *time.Time
	CloseTo   *time.Time
	Search    string
	// Status defaults to open. "all" includes won and lost.
	Status string
	Limit  int
	// Scope restricts the board to what the caller may see.
	Scope func(*gorm.DB) *gorm.DB
}

// Column is one stage as the board draws it.
type Column struct {
	Stage models.PipelineStage `json:"stage"`
	// Count and the totals describe every deal in the column, not only the
	// page of cards below: "12 deals · $40k" has to stay true when the user
	// has loaded three of them.
	Count    int64         `json:"count"`
	Value    float64       `json:"total_value"`
	Weighted float64       `json:"weighted_value"`
	Deals    []models.Deal `json:"deals"`
	HasMore  bool          `json:"has_more"`
}

// Board returns a pipeline's stages with their cards and totals.
func (s *Service) Board(ctx context.Context, orgID, pipelineID uuid.UUID, f BoardFilter) (*models.Pipeline, []Column, error) {
	pipeline, err := s.GetPipeline(ctx, orgID, pipelineID)
	if err != nil {
		return nil, nil, err
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = DefaultColumnPage
	}

	columns := make([]Column, 0, len(pipeline.Stages))
	for _, stage := range pipeline.Stages {
		column := Column{Stage: stage}

		type summary struct {
			Count int64
			Value float64
		}
		var sum summary
		if err := s.filtered(ctx, orgID, pipelineID, f).
			Where("deals.stage_id = ?", stage.ID).
			Select("count(*) as count, coalesce(sum(deals.value),0) as value").
			Scan(&sum).Error; err != nil {
			return nil, nil, err
		}
		column.Count = sum.Count
		column.Value = sum.Value
		column.Weighted = sum.Value * float64(stage.Probability) / 100

		// One extra row answers "is there a next page" without a second count.
		var cards []models.Deal
		if err := s.filtered(ctx, orgID, pipelineID, f).
			Where("deals.stage_id = ?", stage.ID).
			Preload("Contact").
			Order("deals.board_position, deals.created_at").
			Limit(limit + 1).Find(&cards).Error; err != nil {
			return nil, nil, err
		}
		if len(cards) > limit {
			column.HasMore = true
			cards = cards[:limit]
		}
		column.Deals = cards

		columns = append(columns, column)
	}

	return pipeline, columns, nil
}

// StageDeals loads one more page of cards for a column.
func (s *Service) StageDeals(ctx context.Context, orgID, pipelineID, stageID uuid.UUID, after string, f BoardFilter) ([]models.Deal, bool, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = DefaultColumnPage
	}

	q := s.filtered(ctx, orgID, pipelineID, f).Where("deals.stage_id = ?", stageID)
	if after != "" {
		// Keyset rather than offset: cards move while someone is paging, and
		// an offset would then skip or repeat a card.
		q = q.Where("deals.board_position > ?", after)
	}

	var cards []models.Deal
	if err := q.Preload("Contact").
		Order("deals.board_position, deals.created_at").
		Limit(limit + 1).Find(&cards).Error; err != nil {
		return nil, false, err
	}
	more := len(cards) > limit
	if more {
		cards = cards[:limit]
	}
	return cards, more, nil
}

func (s *Service) filtered(ctx context.Context, orgID, pipelineID uuid.UUID, f BoardFilter) *gorm.DB {
	q := s.DB.WithContext(ctx).Model(&models.Deal{}).
		Where("deals.organization_id = ? AND deals.pipeline_id = ?", orgID, pipelineID).
		Where(liveDealContactOnly())

	status := f.Status
	if status == "" {
		status = models.DealOpen
	}
	if status != "all" {
		q = q.Where("deals.status = ?", status)
	}
	if f.OwnerID != nil {
		q = q.Where("deals.owner_id = ?", *f.OwnerID)
	}
	if f.CloseFrom != nil {
		q = q.Where("deals.expected_close_date >= ?", *f.CloseFrom)
	}
	if f.CloseTo != nil {
		q = q.Where("deals.expected_close_date <= ?", *f.CloseTo)
	}
	if search := strings.TrimSpace(f.Search); search != "" {
		q = q.Where("deals.title ILIKE ?", "%"+search+"%")
	}
	if f.Scope != nil {
		q = f.Scope(q)
	}
	return q
}

// VisibleTo restricts a deal query to what one user may see.
//
// Someone who can read every contact can read every deal. Everyone else sees
// the deals they own and the deals on contacts assigned to them, so an agent's
// board is their own work rather than the whole company's pipeline.
func VisibleTo(userID uuid.UUID, canReadAllContacts bool) func(*gorm.DB) *gorm.DB {
	if canReadAllContacts {
		return nil
	}
	return func(q *gorm.DB) *gorm.DB {
		return q.Where(
			"deals.owner_id = ? OR EXISTS (SELECT 1 FROM contacts c WHERE c.id = deals.contact_id AND c.assigned_user_id = ?)",
			userID, userID)
	}
}
