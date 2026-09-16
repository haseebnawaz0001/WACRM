package reports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// PipelineFunnel is R3: how deals move, what converts and how long it takes.
type PipelineFunnel struct {
	Steps []FunnelStep `json:"steps"`
	// WinRate is won over closed, not over everything: counting deals that are
	// still in play as losses makes every pipeline look terrible and every
	// quarter look better than the last as old deals finally close.
	WinRate float64 `json:"win_rate"`
	Won     int64   `json:"won"`
	Lost    int64   `json:"lost"`

	LostReasons []LostReasonRow `json:"lost_reasons"`
}

// LostReasonRow is one reason deals were lost, and how many.
type LostReasonRow struct {
	Reason string  `json:"reason"`
	Count  int64   `json:"count"`
	Value  float64 `json:"value"`
}

// PipelineFunnel builds the deal funnel for one pipeline over a period.
func (s *Service) PipelineFunnel(ctx context.Context, v Viewer, r Range, pipelineID uuid.UUID) (*PipelineFunnel, error) {
	var stages []models.PipelineStage
	if err := s.DB.WithContext(ctx).
		Where("pipeline_id = ? AND organization_id = ?", pipelineID, v.OrgID).
		Order("position").Find(&stages).Error; err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		return &PipelineFunnel{Steps: []FunnelStep{}}, nil
	}

	// How many deals entered each stage during the period, and how long they
	// had spent in the stage they came from.
	type entry struct {
		StageID       uuid.UUID
		Deals         int64
		MedianSeconds *float64
	}
	var entries []entry
	err := s.DB.WithContext(ctx).Table("deal_stage_history AS h").
		Select(`h.to_stage_id AS stage_id,
			count(DISTINCT h.deal_id) AS deals,
			percentile_cont(0.5) WITHIN GROUP (ORDER BY h.duration_seconds)
				FILTER (WHERE h.from_stage_id IS NOT NULL) AS median_seconds`).
		Joins("JOIN deals d ON d.id = h.deal_id").
		Where("h.organization_id = ? AND d.pipeline_id = ?", v.OrgID, pipelineID).
		Where("h.created_at >= ? AND h.created_at <= ?", r.From, r.To).
		Group("h.to_stage_id").Scan(&entries).Error
	if err != nil {
		return nil, err
	}

	reached := make(map[uuid.UUID]int64, len(entries))
	median := make(map[uuid.UUID]*float64, len(entries))
	for _, item := range entries {
		reached[item.StageID] = item.Deals
		median[item.StageID] = item.MedianSeconds
	}

	out := &PipelineFunnel{Steps: make([]FunnelStep, 0, len(stages))}
	var previous int64
	for i, stage := range stages {
		step := FunnelStep{Key: stage.ID.String(), Label: stage.Name, Reached: reached[stage.ID]}
		if i == 0 {
			step.Conversion = 100
		} else if previous > 0 {
			step.Conversion = float64(step.Reached) / float64(previous) * 100
		}
		if seconds := median[stage.ID]; seconds != nil {
			days := *seconds / 86400
			step.MedianDaysFromPrevious = &days
		}
		previous = step.Reached

		switch stage.StageType {
		case models.StageWon:
			out.Won += step.Reached
		case models.StageLost:
			out.Lost += step.Reached
		}
		out.Steps = append(out.Steps, step)
	}

	if closed := out.Won + out.Lost; closed > 0 {
		out.WinRate = float64(out.Won) / float64(closed) * 100
	}

	reasons, err := s.lostReasons(ctx, v, r, pipelineID)
	if err != nil {
		return nil, err
	}
	out.LostReasons = reasons
	return out, nil
}

func (s *Service) lostReasons(ctx context.Context, v Viewer, r Range, pipelineID uuid.UUID) ([]LostReasonRow, error) {
	type row struct {
		Reason string
		Count  int64
		Value  float64
	}
	var rows []row

	err := s.DB.WithContext(ctx).Model(&models.Deal{}).
		Select("COALESCE(NULLIF(lost_reason, ''), ?) AS reason, count(*) AS count, COALESCE(sum(value),0) AS value",
			UnknownBucket).
		Where("organization_id = ? AND pipeline_id = ? AND status = ?", v.OrgID, pipelineID, models.DealLost).
		Where("closed_at >= ? AND closed_at <= ?", r.From, r.To).
		Group("reason").Order("count DESC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make([]LostReasonRow, 0, len(rows))
	for _, item := range rows {
		out = append(out, LostReasonRow{Reason: item.Reason, Count: item.Count, Value: item.Value})
	}
	return out, nil
}

// ForecastMonth is one month of expected closes.
type ForecastMonth struct {
	Month string  `json:"month"`
	Deals int64   `json:"deals"`
	Value float64 `json:"value"`
	// Weighted multiplies each deal by its stage probability, so a forecast is
	// not simply the sum of everything anyone ever hoped for.
	Weighted float64 `json:"weighted_value"`
}

// PipelineForecast is R3's forward look.
type PipelineForecast struct {
	Months []ForecastMonth `json:"months"`
	// Overdue is open deals whose expected close date has already passed —
	// the part of a forecast that is quietly fiction.
	Overdue      int64   `json:"overdue"`
	OverdueValue float64 `json:"overdue_value"`
	// Undated is open deals with no close date at all, which no forecast can
	// place; hiding them would make the total look complete when it is not.
	Undated      int64   `json:"undated"`
	UndatedValue float64 `json:"undated_value"`
}

// PipelineForecast projects open deals by expected close month.
func (s *Service) PipelineForecast(ctx context.Context, v Viewer, pipelineID uuid.UUID, months int, loc *time.Location) (*PipelineForecast, error) {
	if months <= 0 || months > 24 {
		months = 6
	}
	if loc == nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	horizon := now.AddDate(0, months, 0)

	type row struct {
		Month    time.Time
		Deals    int64
		Value    float64
		Weighted float64
	}
	var rows []row

	err := s.DB.WithContext(ctx).Table("deals AS d").
		Select(`date_trunc('month', d.expected_close_date AT TIME ZONE ?) AS month,
			count(*) AS deals,
			COALESCE(sum(d.value), 0) AS value,
			COALESCE(sum(d.value * ps.probability / 100.0), 0) AS weighted`, loc.String()).
		Joins("JOIN pipeline_stages ps ON ps.id = d.stage_id").
		Where("d.organization_id = ? AND d.pipeline_id = ? AND d.status = ? AND d.deleted_at IS NULL",
			v.OrgID, pipelineID, models.DealOpen).
		Where("d.expected_close_date IS NOT NULL AND d.expected_close_date >= ? AND d.expected_close_date < ?",
			startOfDay(now, loc), horizon).
		Group("month").Order("month").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := &PipelineForecast{Months: make([]ForecastMonth, 0, len(rows))}
	for _, item := range rows {
		out.Months = append(out.Months, ForecastMonth{
			Month:    item.Month.In(loc).Format("2006-01"),
			Deals:    item.Deals,
			Value:    item.Value,
			Weighted: item.Weighted,
		})
	}

	type tally struct {
		Deals int64
		Value float64
	}
	var overdue, undated tally

	base := s.DB.WithContext(ctx).Model(&models.Deal{}).
		Where("organization_id = ? AND pipeline_id = ? AND status = ?", v.OrgID, pipelineID, models.DealOpen)

	if err := base.Session(&gorm.Session{}).
		Select("count(*) AS deals, COALESCE(sum(value),0) AS value").
		Where("expected_close_date IS NOT NULL AND expected_close_date < ?", startOfDay(now, loc)).
		Scan(&overdue).Error; err != nil {
		return nil, err
	}
	if err := base.Session(&gorm.Session{}).
		Select("count(*) AS deals, COALESCE(sum(value),0) AS value").
		Where("expected_close_date IS NULL").
		Scan(&undated).Error; err != nil {
		return nil, err
	}

	out.Overdue, out.OverdueValue = overdue.Deals, overdue.Value
	out.Undated, out.UndatedValue = undated.Deals, undated.Value
	return out, nil
}
