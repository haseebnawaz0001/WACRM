package deals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Get returns one deal with its stage and contact.
func (s *Service) Get(ctx context.Context, orgID, dealID uuid.UUID) (*models.Deal, error) {
	var deal models.Deal
	if err := s.DB.WithContext(ctx).Preload("Stage").Preload("Contact").
		Where("id = ? AND organization_id = ?", dealID, orgID).
		First(&deal).Error; err != nil {
		return nil, ErrNotFound
	}
	return &deal, nil
}

// UpdateInput carries the editable fields of a deal. Every field is a pointer
// so "set the value to zero" and "leave the value alone" stay distinguishable.
type UpdateInput struct {
	Title             *string
	Value             *float64
	Currency          *string
	OwnerID           **uuid.UUID
	ExpectedCloseDate **time.Time
	LostReason        *string
}

// Update edits a deal's details. The stage is deliberately not editable here:
// moving is its own operation because it writes history and fires events.
func (s *Service) Update(ctx context.Context, orgID, dealID uuid.UUID, in UpdateInput, actor crmevents.Actor) (*models.Deal, error) {
	updates := map[string]any{}
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return nil, fmt.Errorf("deals: a deal needs a title")
		}
		updates["title"] = strings.TrimSpace(*in.Title)
	}
	if in.Value != nil {
		updates["value"] = *in.Value
	}
	if in.Currency != nil && *in.Currency != "" {
		updates["currency"] = strings.ToUpper(*in.Currency)
	}
	if in.OwnerID != nil {
		updates["owner_id"] = *in.OwnerID
	}
	if in.ExpectedCloseDate != nil {
		updates["expected_close_date"] = *in.ExpectedCloseDate
	}
	if in.LostReason != nil {
		updates["lost_reason"] = *in.LostReason
	}

	var deal models.Deal
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND organization_id = ?", dealID, orgID).
			First(&deal).Error; err != nil {
			return ErrNotFound
		}
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&models.Deal{}).Where("id = ?", dealID).
			Updates(updates).Error; err != nil {
			return err
		}
		var fresh models.Deal
		if err := tx.Where("id = ?", dealID).First(&fresh).Error; err != nil {
			return err
		}
		deal = fresh
		return s.publish(tx, &deal, "deal.updated", actor)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, deal.ID)
}

// Delete removes a deal.
func (s *Service) Delete(ctx context.Context, orgID, dealID uuid.UUID, actor crmevents.Actor) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var deal models.Deal
		if err := tx.Where("id = ? AND organization_id = ?", dealID, orgID).
			First(&deal).Error; err != nil {
			return ErrNotFound
		}
		if err := tx.Delete(&deal).Error; err != nil {
			return err
		}
		return s.publish(tx, &deal, "deal.deleted", actor)
	})
}

// HistoryEntry is one stage move, resolved for display.
type HistoryEntry struct {
	models.DealStageHistory
	FromStageName string `json:"from_stage_name,omitempty"`
	ToStageName   string `json:"to_stage_name"`
}

// History returns every stage move for a deal, oldest first.
func (s *Service) History(ctx context.Context, orgID, dealID uuid.UUID) ([]HistoryEntry, error) {
	var rows []models.DealStageHistory
	if err := s.DB.WithContext(ctx).
		Where("deal_id = ? AND organization_id = ?", dealID, orgID).
		Order("created_at").Find(&rows).Error; err != nil {
		return nil, err
	}

	names, err := s.stageNames(ctx, orgID)
	if err != nil {
		return nil, err
	}

	out := make([]HistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry := HistoryEntry{DealStageHistory: row, ToStageName: names[row.ToStageID]}
		if row.FromStageID != nil {
			entry.FromStageName = names[*row.FromStageID]
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *Service) stageNames(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]string, error) {
	var stages []models.PipelineStage
	if err := s.DB.WithContext(ctx).Select("id", "name").
		Where("organization_id = ?", orgID).Find(&stages).Error; err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, len(stages))
	for _, stage := range stages {
		names[stage.ID] = stage.Name
	}
	return names, nil
}

// ListPipelines returns the organization's pipelines with their stages.
func (s *Service) ListPipelines(ctx context.Context, orgID uuid.UUID, includeArchived bool) ([]models.Pipeline, error) {
	q := s.DB.WithContext(ctx).
		Preload("Stages", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
		Where("organization_id = ?", orgID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}
	var out []models.Pipeline
	err := q.Order("is_default DESC, position, created_at").Find(&out).Error
	return out, err
}

// PipelineInput carries the editable fields of a pipeline.
type PipelineInput struct {
	Name                *string
	ObjectLabelSingular *string
	ObjectLabelPlural   *string
	Currency            *string
	IsDefault           *bool
	Position            *int
}

// UpdatePipeline edits a pipeline's settings.
func (s *Service) UpdatePipeline(ctx context.Context, orgID, pipelineID uuid.UUID, in PipelineInput) (*models.Pipeline, error) {
	updates := map[string]any{}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return nil, fmt.Errorf("deals: a pipeline needs a name")
		}
		updates["name"] = strings.TrimSpace(*in.Name)
	}
	if in.ObjectLabelSingular != nil && *in.ObjectLabelSingular != "" {
		updates["object_label_singular"] = *in.ObjectLabelSingular
	}
	if in.ObjectLabelPlural != nil && *in.ObjectLabelPlural != "" {
		updates["object_label_plural"] = *in.ObjectLabelPlural
	}
	if in.Currency != nil && *in.Currency != "" {
		updates["currency"] = strings.ToUpper(*in.Currency)
	}
	if in.Position != nil {
		updates["position"] = *in.Position
	}

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pipeline models.Pipeline
		if err := tx.Where("id = ? AND organization_id = ?", pipelineID, orgID).
			First(&pipeline).Error; err != nil {
			return ErrNotFound
		}
		if in.IsDefault != nil && *in.IsDefault {
			// Exactly one default, or "the default pipeline" stops meaning
			// anything and new deals land wherever the sort happens to put them.
			if err := tx.Model(&models.Pipeline{}).
				Where("organization_id = ?", orgID).
				Update("is_default", false).Error; err != nil {
				return err
			}
			updates["is_default"] = true
		}
		if len(updates) == 0 {
			return nil
		}
		return tx.Model(&models.Pipeline{}).Where("id = ?", pipelineID).Updates(updates).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetPipeline(ctx, orgID, pipelineID)
}

// ErrPipelineInUse is returned when a pipeline still holds open deals.
var ErrPipelineInUse = errors.New("deals: this pipeline still has open deals")

// DeletePipeline removes a pipeline, refusing while open deals live on it.
//
// Archiving is the way to retire a pipeline that has history: deleting would
// orphan closed deals that reports still count.
func (s *Service) DeletePipeline(ctx context.Context, orgID, pipelineID uuid.UUID) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pipeline models.Pipeline
		if err := tx.Where("id = ? AND organization_id = ?", pipelineID, orgID).
			First(&pipeline).Error; err != nil {
			return ErrNotFound
		}

		var open int64
		if err := tx.Model(&models.Deal{}).
			Where("pipeline_id = ? AND status = ?", pipelineID, models.DealOpen).
			Count(&open).Error; err != nil {
			return err
		}
		if open > 0 {
			return ErrPipelineInUse
		}

		if err := tx.Where("pipeline_id = ?", pipelineID).
			Delete(&models.PipelineStage{}).Error; err != nil {
			return err
		}
		return tx.Delete(&pipeline).Error
	})
}

// ArchivePipeline hides a pipeline without touching its deals.
func (s *Service) ArchivePipeline(ctx context.Context, orgID, pipelineID uuid.UUID, archived bool) (*models.Pipeline, error) {
	var at *time.Time
	if archived {
		now := time.Now().UTC()
		at = &now
	}
	res := s.DB.WithContext(ctx).Model(&models.Pipeline{}).
		Where("id = ? AND organization_id = ?", pipelineID, orgID).
		Update("archived_at", at)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return s.GetPipeline(ctx, orgID, pipelineID)
}

// StageInput carries the editable fields of a stage.
type StageInput struct {
	Name        *string
	StageType   *string
	Probability *int
	Color       *string
	RottingDays *int
	Position    *int
}

// CreateStage adds a column to a pipeline.
func (s *Service) CreateStage(ctx context.Context, orgID, pipelineID uuid.UUID, in StageInput) (*models.PipelineStage, error) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, fmt.Errorf("deals: a stage needs a name")
	}
	if _, err := s.GetPipeline(ctx, orgID, pipelineID); err != nil {
		return nil, err
	}

	stage := models.PipelineStage{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		PipelineID:     pipelineID,
		Name:           strings.TrimSpace(*in.Name),
		StageType:      models.StageOpen,
		Color:          "gray",
	}
	if err := applyStageInput(&stage, in); err != nil {
		return nil, err
	}
	if in.Position == nil {
		// Land before the closed columns rather than after them: a new stage
		// appended past "Won" would sit outside the funnel it belongs to.
		var last int
		s.DB.WithContext(ctx).Model(&models.PipelineStage{}).
			Where("pipeline_id = ? AND stage_type = ?", pipelineID, models.StageOpen).
			Select("COALESCE(MAX(position),0)").Scan(&last)
		stage.Position = last + 5
	}

	if err := s.DB.WithContext(ctx).Create(&stage).Error; err != nil {
		return nil, err
	}
	return &stage, nil
}

// UpdateStage edits a column.
func (s *Service) UpdateStage(ctx context.Context, orgID, stageID uuid.UUID, in StageInput) (*models.PipelineStage, error) {
	var stage models.PipelineStage
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", stageID, orgID).
		First(&stage).Error; err != nil {
		return nil, ErrNotFound
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return nil, fmt.Errorf("deals: a stage needs a name")
		}
		stage.Name = strings.TrimSpace(*in.Name)
	}
	if err := applyStageInput(&stage, in); err != nil {
		return nil, err
	}

	if err := s.DB.WithContext(ctx).Save(&stage).Error; err != nil {
		return nil, err
	}

	// Deals already sitting here must agree with what the column now means,
	// otherwise a stage retyped to "Won" leaves a board full of cards in the
	// won column that every report still counts as open.
	if err := s.syncStatusToStage(ctx, stage); err != nil {
		return nil, err
	}
	return &stage, nil
}

func (s *Service) syncStatusToStage(ctx context.Context, stage models.PipelineStage) error {
	status := statusForStage(&stage)
	updates := map[string]any{"status": status}
	if status == models.DealOpen {
		updates["closed_at"] = nil
	}
	return s.DB.WithContext(ctx).Model(&models.Deal{}).
		Where("stage_id = ? AND status <> ?", stage.ID, status).
		Updates(updates).Error
}

func applyStageInput(stage *models.PipelineStage, in StageInput) error {
	if in.StageType != nil {
		switch *in.StageType {
		case models.StageOpen, models.StageWon, models.StageLost:
			stage.StageType = *in.StageType
		default:
			return fmt.Errorf("deals: %q is not a stage type", *in.StageType)
		}
	}
	if in.Probability != nil {
		if *in.Probability < 0 || *in.Probability > 100 {
			return fmt.Errorf("deals: probability is a percentage between 0 and 100")
		}
		stage.Probability = *in.Probability
	}
	if in.Color != nil && *in.Color != "" {
		stage.Color = *in.Color
	}
	if in.RottingDays != nil {
		if *in.RottingDays < 0 {
			return fmt.Errorf("deals: rotting days cannot be negative")
		}
		stage.RottingDays = *in.RottingDays
	}
	if in.Position != nil {
		stage.Position = *in.Position
	}
	return nil
}

// ReorderStages writes a new column order in one pass.
func (s *Service) ReorderStages(ctx context.Context, orgID, pipelineID uuid.UUID, order []uuid.UUID) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, stageID := range order {
			res := tx.Model(&models.PipelineStage{}).
				Where("id = ? AND pipeline_id = ? AND organization_id = ?", stageID, pipelineID, orgID).
				Update("position", (i+1)*10)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("deals: stage %s is not part of this pipeline", stageID)
			}
		}
		return nil
	})
}

// ErrStageHasDeals is returned when a stage cannot be removed because deals
// still sit in it and no destination was given.
var ErrStageHasDeals = errors.New("deals: this stage still holds deals")

// DeleteStage removes a column, moving any cards in it to moveTo.
//
// Deleting the column out from under live deals would leave them pointing at a
// stage that no longer exists, so the caller has to say where they go.
func (s *Service) DeleteStage(ctx context.Context, orgID, stageID uuid.UUID, moveTo *uuid.UUID) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stage models.PipelineStage
		if err := tx.Where("id = ? AND organization_id = ?", stageID, orgID).
			First(&stage).Error; err != nil {
			return ErrNotFound
		}

		var remaining int64
		if err := tx.Model(&models.Deal{}).Where("stage_id = ?", stageID).
			Count(&remaining).Error; err != nil {
			return err
		}

		if remaining > 0 {
			if moveTo == nil {
				return ErrStageHasDeals
			}
			var target models.PipelineStage
			if err := tx.Where("id = ? AND pipeline_id = ?", *moveTo, stage.PipelineID).
				First(&target).Error; err != nil {
				return fmt.Errorf("deals: the destination stage is not in this pipeline")
			}
			if target.ID == stage.ID {
				return fmt.Errorf("deals: a stage cannot be moved into itself")
			}
			if err := tx.Model(&models.Deal{}).Where("stage_id = ?", stageID).
				Updates(map[string]any{
					"stage_id":         target.ID,
					"status":           statusForStage(&target),
					"stage_entered_at": time.Now().UTC(),
				}).Error; err != nil {
				return err
			}
		}

		// A pipeline with no open column can never take a new deal.
		var openStages int64
		if err := tx.Model(&models.PipelineStage{}).
			Where("pipeline_id = ? AND stage_type = ? AND id <> ?",
				stage.PipelineID, models.StageOpen, stageID).
			Count(&openStages).Error; err != nil {
			return err
		}
		if openStages == 0 && stage.StageType == models.StageOpen {
			return fmt.Errorf("deals: a pipeline needs at least one open stage")
		}

		return tx.Delete(&stage).Error
	})
}
