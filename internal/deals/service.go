// Package deals moves opportunities through a pipeline (plan 07).
//
// Conversations answer "who is talking to us"; deals answer "what is it worth
// and where has it got to". Without them a sales-led organization tracks
// pipeline in a spreadsheet beside the product, which goes stale the moment
// anyone forgets to update it.
package deals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned when a deal, pipeline or stage does not exist.
var ErrNotFound = errors.New("deals: not found")

// Service creates and moves deals.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// DefaultStages are the stages a new pipeline starts with.
//
// A board with no columns is unusable, and asking someone to invent a sales
// process before they can create their first deal is a poor first run.
func DefaultStages() []models.PipelineStage {
	return []models.PipelineStage{
		{Name: "New", Position: 10, StageType: models.StageOpen, Probability: 10, Color: "gray"},
		{Name: "Qualified", Position: 20, StageType: models.StageOpen, Probability: 30, Color: "blue"},
		{Name: "Proposal", Position: 30, StageType: models.StageOpen, Probability: 60, Color: "purple", RottingDays: 14},
		{Name: "Won", Position: 40, StageType: models.StageWon, Probability: 100, Color: "green"},
		{Name: "Lost", Position: 50, StageType: models.StageLost, Probability: 0, Color: "red"},
	}
}

// CreatePipeline adds a pipeline with its default stages.
func (s *Service) CreatePipeline(ctx context.Context, orgID uuid.UUID, name, currency string, actorID *uuid.UUID) (*models.Pipeline, error) {
	if name == "" {
		return nil, fmt.Errorf("deals: a pipeline needs a name")
	}
	if currency == "" {
		currency = "USD"
	}

	pipeline := &models.Pipeline{
		BaseModel:           models.BaseModel{ID: uuid.New()},
		OrganizationID:      orgID,
		Name:                name,
		ObjectLabelSingular: "Deal",
		ObjectLabelPlural:   "Deals",
		Currency:            currency,
		CreatedByID:         actorID,
	}

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The first pipeline becomes the default, so a deal created before
		// anyone configures anything still has somewhere to go.
		var existing int64
		if err := tx.Model(&models.Pipeline{}).
			Where("organization_id = ?", orgID).Count(&existing).Error; err != nil {
			return err
		}
		pipeline.IsDefault = existing == 0

		if err := tx.Create(pipeline).Error; err != nil {
			return err
		}

		for _, stage := range DefaultStages() {
			row := stage
			row.ID = uuid.New()
			row.OrganizationID = orgID
			row.PipelineID = pipeline.ID
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetPipeline(ctx, orgID, pipeline.ID)
}

// GetPipeline returns a pipeline with its stages in board order.
func (s *Service) GetPipeline(ctx context.Context, orgID, pipelineID uuid.UUID) (*models.Pipeline, error) {
	var pipeline models.Pipeline
	if err := s.DB.WithContext(ctx).
		Preload("Stages", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Where("id = ? AND organization_id = ?", pipelineID, orgID).
		First(&pipeline).Error; err != nil {
		return nil, ErrNotFound
	}
	return &pipeline, nil
}

// DefaultPipeline returns the organization's default pipeline.
func (s *Service) DefaultPipeline(ctx context.Context, orgID uuid.UUID) (*models.Pipeline, error) {
	var pipeline models.Pipeline
	err := s.DB.WithContext(ctx).
		Preload("Stages", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Where("organization_id = ? AND archived_at IS NULL", orgID).
		Order("is_default DESC, position").First(&pipeline).Error
	if err != nil {
		return nil, ErrNotFound
	}
	return &pipeline, nil
}

// CreateInput describes a deal to create.
type CreateInput struct {
	OrgID      uuid.UUID
	PipelineID *uuid.UUID
	StageID    *uuid.UUID
	ContactID  uuid.UUID
	// ConversationID records where the opportunity came from, so a deal
	// created from chat links back to the thread that produced it.
	ConversationID    *uuid.UUID
	Title             string
	Value             float64
	Currency          string
	OwnerID           *uuid.UUID
	ExpectedCloseDate *time.Time
	CreatedBy         *uuid.UUID
}

// Create adds a deal, defaulting to the first open stage of the default
// pipeline.
func (s *Service) Create(ctx context.Context, in CreateInput) (*models.Deal, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("deals: a deal needs a title")
	}

	var contact models.Contact
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", in.ContactID, in.OrgID).
		First(&contact).Error; err != nil {
		return nil, fmt.Errorf("deals: contact not found")
	}

	pipeline, err := s.resolvePipeline(ctx, in)
	if err != nil {
		return nil, err
	}
	stage, err := s.resolveStage(ctx, in, pipeline)
	if err != nil {
		return nil, err
	}

	currency := in.Currency
	if currency == "" {
		currency = pipeline.Currency
	}
	owner := in.OwnerID
	if owner == nil {
		// The contact's owner by default: whoever manages the relationship is
		// the obvious person to own the opportunity.
		owner = contact.AssignedUserID
	}

	now := time.Now().UTC()
	deal := &models.Deal{
		BaseModel:         models.BaseModel{ID: uuid.New()},
		OrganizationID:    in.OrgID,
		PipelineID:        pipeline.ID,
		StageID:           stage.ID,
		ContactID:         in.ContactID,
		ConversationID:    in.ConversationID,
		Title:             in.Title,
		Value:             in.Value,
		Currency:          currency,
		OwnerID:           owner,
		Status:            statusForStage(stage),
		ExpectedCloseDate: in.ExpectedCloseDate,
		StageEnteredAt:    now,
		CreatedByID:       in.CreatedBy,
	}

	if err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		last, err := lastRank(tx, stage.ID)
		if err != nil {
			return err
		}
		// New cards land at the bottom of the column: appearing at the top
		// would shuffle a board someone is already reading.
		deal.BoardPosition = rankBetween(last, "")

		if err := tx.Create(deal).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.DealStageHistory{
			ID:             uuid.New(),
			OrganizationID: in.OrgID,
			DealID:         deal.ID,
			ToStageID:      stage.ID,
			MovedByID:      in.CreatedBy,
		}).Error; err != nil {
			return err
		}
		return s.publish(tx, deal, "deal.created", actorFor(in.CreatedBy))
	}); err != nil {
		return nil, err
	}

	deal.Stage = stage
	return deal, nil
}

func (s *Service) resolvePipeline(ctx context.Context, in CreateInput) (*models.Pipeline, error) {
	if in.PipelineID != nil {
		return s.GetPipeline(ctx, in.OrgID, *in.PipelineID)
	}
	return s.DefaultPipeline(ctx, in.OrgID)
}

func (s *Service) resolveStage(ctx context.Context, in CreateInput, pipeline *models.Pipeline) (*models.PipelineStage, error) {
	if in.StageID != nil {
		var stage models.PipelineStage
		if err := s.DB.WithContext(ctx).
			Where("id = ? AND pipeline_id = ?", *in.StageID, pipeline.ID).
			First(&stage).Error; err != nil {
			return nil, fmt.Errorf("deals: that stage is not part of the pipeline")
		}
		return &stage, nil
	}

	// The first open stage, not simply the first: a board whose first column
	// happened to be "Lost" would create every deal already closed.
	for i := range pipeline.Stages {
		if pipeline.Stages[i].StageType == models.StageOpen {
			return &pipeline.Stages[i], nil
		}
	}
	return nil, fmt.Errorf("deals: the pipeline has no open stage")
}

func statusForStage(stage *models.PipelineStage) string {
	switch stage.StageType {
	case models.StageWon:
		return models.DealWon
	case models.StageLost:
		return models.DealLost
	}
	return models.DealOpen
}

// MoveStage moves a deal to another stage.
//
// The status follows the stage rather than being set separately: a deal in a
// "Won" column that still says open is the kind of disagreement that makes a
// board untrustworthy.
func (s *Service) MoveStage(ctx context.Context, orgID, dealID, stageID uuid.UUID, actor crmevents.Actor) (*models.Deal, error) {
	return s.Move(ctx, orgID, dealID, MoveInput{StageID: stageID}, actor)
}

// MoveInput describes where a card was dropped.
//
// The neighbours are ids rather than an index because two people dragging at
// once disagree about indexes but agree about which cards they dropped
// between.
type MoveInput struct {
	StageID    uuid.UUID
	BeforeID   *uuid.UUID // the card the deal was dropped above
	AfterID    *uuid.UUID // the card the deal was dropped below
	LostReason string
}

// Move places a deal in a stage, at a position within that stage.
func (s *Service) Move(ctx context.Context, orgID, dealID uuid.UUID, in MoveInput, actor crmevents.Actor) (*models.Deal, error) {
	var deal models.Deal

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the row: two people dragging the same card would otherwise both
		// compute a position from the same starting point and one move would
		// silently vanish.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND organization_id = ?", dealID, orgID).
			First(&deal).Error; err != nil {
			return ErrNotFound
		}

		var stage models.PipelineStage
		if err := tx.Where("id = ? AND pipeline_id = ?", in.StageID, deal.PipelineID).
			First(&stage).Error; err != nil {
			return fmt.Errorf("deals: that stage is not part of this deal's pipeline")
		}

		position, err := s.rankFor(tx, stage.ID, dealID, in)
		if err != nil {
			return err
		}

		sameStage := stage.ID == deal.StageID
		if sameStage {
			// Reordering inside a column is not a change in the deal's state,
			// so it writes no history and fires no event: a funnel report that
			// counted drag-to-tidy as progress would be fiction.
			if position == deal.BoardPosition {
				return nil
			}
			if err := tx.Model(&models.Deal{}).Where("id = ?", dealID).
				Update("board_position", position).Error; err != nil {
				return err
			}
			deal.BoardPosition = position
			return nil
		}

		now := time.Now().UTC()
		status := statusForStage(&stage)

		updates := map[string]any{
			"stage_id":         stage.ID,
			"status":           status,
			"stage_entered_at": now,
			"board_position":   position,
		}
		switch status {
		case models.DealOpen:
			// Reopening clears the close, or a reopened deal would still
			// report as closed in every summary.
			updates["closed_at"] = nil
			updates["lost_reason"] = ""
		case models.DealLost:
			updates["closed_at"] = now
			if in.LostReason != "" {
				updates["lost_reason"] = in.LostReason
			}
		default:
			updates["closed_at"] = now
		}

		if err := tx.Model(&models.Deal{}).Where("id = ?", dealID).Updates(updates).Error; err != nil {
			return err
		}

		// How long it sat in the stage it just left, so funnel reporting can
		// measure the process rather than guess from where deals are now.
		previous := deal.StageID
		if err := tx.Create(&models.DealStageHistory{
			ID:              uuid.New(),
			OrganizationID:  orgID,
			DealID:          dealID,
			FromStageID:     &previous,
			ToStageID:       stage.ID,
			MovedByID:       actor.ID,
			DurationSeconds: int64(now.Sub(deal.StageEnteredAt).Seconds()),
		}).Error; err != nil {
			return err
		}

		// Reload into a fresh struct: scanning over the pre-update copy would
		// leave cleared columns (closed_at) holding their stale pointers.
		var moved models.Deal
		if err := tx.Where("id = ?", dealID).First(&moved).Error; err != nil {
			return err
		}
		moved.Stage = &stage
		deal = moved

		eventType := "deal.stage_changed"
		switch status {
		case models.DealWon:
			eventType = "deal.won"
		case models.DealLost:
			eventType = "deal.lost"
		}
		return s.publish(tx, &deal, eventType, actor)
	})

	if err != nil {
		return nil, err
	}
	return &deal, nil
}

// rankFor works out the board position a dropped card should take.
func (s *Service) rankFor(tx *gorm.DB, stageID, dealID uuid.UUID, in MoveInput) (string, error) {
	after, err := rankOf(tx, in.AfterID, dealID)
	if err != nil {
		return "", err
	}
	before, err := rankOf(tx, in.BeforeID, dealID)
	if err != nil {
		return "", err
	}

	// No neighbours named: the caller only changed stage, so the card goes to
	// the bottom of its new column.
	if in.AfterID == nil && in.BeforeID == nil {
		last, err := lastRank(tx, stageID)
		if err != nil {
			return "", err
		}
		return rankBetween(last, ""), nil
	}
	return rankBetween(after, before), nil
}

// rankOf reads one neighbour's position. A neighbour that is the deal itself,
// or has gone, is treated as absent rather than as an error: the board the
// user dragged on is always slightly behind the database.
func rankOf(tx *gorm.DB, id *uuid.UUID, self uuid.UUID) (string, error) {
	if id == nil || *id == self {
		return "", nil
	}
	var neighbour models.Deal
	err := tx.Select("board_position").Where("id = ?", *id).First(&neighbour).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return neighbour.BoardPosition, nil
}

// lastRank returns the position of the bottom card in a column.
func lastRank(tx *gorm.DB, stageID uuid.UUID) (string, error) {
	var deal models.Deal
	err := tx.Select("board_position").Where("stage_id = ?", stageID).
		Order("board_position DESC").First(&deal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return deal.BoardPosition, nil
}

// ListOpts filters a board query.
type ListOpts struct {
	PipelineID *uuid.UUID
	ContactID  *uuid.UUID
	OwnerID    *uuid.UUID
	// Status defaults to open; "all" includes closed deals.
	Status string
	// Scope restricts the query to what the caller may see.
	Scope func(*gorm.DB) *gorm.DB
	Limit int
}

// List returns deals for a board.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, opts ListOpts) ([]models.Deal, error) {
	q := s.DB.WithContext(ctx).Where("deals.organization_id = ?", orgID)

	status := opts.Status
	if status == "" {
		status = models.DealOpen
	}
	if status != "all" {
		q = q.Where("deals.status = ?", status)
	}
	if opts.PipelineID != nil {
		q = q.Where("deals.pipeline_id = ?", *opts.PipelineID)
	}
	if opts.ContactID != nil {
		q = q.Where("deals.contact_id = ?", *opts.ContactID)
	}
	if opts.OwnerID != nil {
		q = q.Where("deals.owner_id = ?", *opts.OwnerID)
	}
	if opts.Scope != nil {
		q = opts.Scope(q)
	}
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}

	var out []models.Deal
	err := q.Preload("Stage").Preload("Contact").
		Order("deals.stage_entered_at DESC").Find(&out).Error
	return out, err
}

// StageTotal is one column's summary.
type StageTotal struct {
	StageID  uuid.UUID `json:"stage_id"`
	Count    int       `json:"count"`
	Value    float64   `json:"value"`
	Weighted float64   `json:"weighted_value"`
}

// Totals summarises a pipeline by stage.
//
// The weighted total multiplies each deal by its stage probability, so a
// forecast is not simply the sum of everything anyone ever hoped for.
func (s *Service) Totals(ctx context.Context, orgID, pipelineID uuid.UUID) ([]StageTotal, error) {
	type row struct {
		StageID     uuid.UUID
		Count       int
		Value       float64
		Probability int
	}
	var rows []row

	if err := s.DB.WithContext(ctx).Model(&models.Deal{}).
		Select("deals.stage_id, count(*) as count, coalesce(sum(deals.value),0) as value, max(pipeline_stages.probability) as probability").
		Joins("JOIN pipeline_stages ON pipeline_stages.id = deals.stage_id").
		Where("deals.organization_id = ? AND deals.pipeline_id = ? AND deals.status = ?",
			orgID, pipelineID, models.DealOpen).
		Group("deals.stage_id").Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]StageTotal, 0, len(rows))
	for _, r := range rows {
		out = append(out, StageTotal{
			StageID:  r.StageID,
			Count:    r.Count,
			Value:    r.Value,
			Weighted: r.Value * float64(r.Probability) / 100,
		})
	}
	return out, nil
}

func (s *Service) publish(tx *gorm.DB, deal *models.Deal, eventType string, actor crmevents.Actor) error {
	if !crmevents.IsKnown(eventType) {
		return nil
	}
	event := crmevents.New(deal.OrganizationID, eventType, actor, map[string]any{
		"deal_id": deal.ID.String(),
		"title":   deal.Title,
		"value":   deal.Value,
		// The pipeline travels with the event so an automation rule can be
		// scoped to one board without loading the deal to find out which.
		"pipeline_id": deal.PipelineID.String(),
		"currency":    deal.Currency,
		"stage_id":    deal.StageID.String(),
		"status":      deal.Status,
	}).ForContact(deal.ContactID).About(crmevents.SubjectDeal, deal.ID)

	return crmevents.PublishTx(tx, event)
}

func actorFor(userID *uuid.UUID) crmevents.Actor {
	if userID == nil {
		return crmevents.SystemActor()
	}
	return crmevents.UserActor(*userID, "")
}
