package reports

import (
	"context"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// AgentRow is one agent's line on the performance leaderboard.
//
// Medians and p90s rather than averages: one conversation left open over a
// weekend moves an average enough to hide a whole team's week, and the p90 is
// what a customer at the back of the queue actually experiences.
type AgentRow struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`

	// Handled counts conversations this agent answered first or resolved.
	Handled int64 `json:"handled"`

	FirstResponseMedianSeconds *float64 `json:"first_response_median_seconds,omitempty"`
	FirstResponseP90Seconds    *float64 `json:"first_response_p90_seconds,omitempty"`
	ResolutionMedianSeconds    *float64 `json:"resolution_median_seconds,omitempty"`
	ResolutionP90Seconds       *float64 `json:"resolution_p90_seconds,omitempty"`

	Resolved int64 `json:"resolved"`
	// ReopenedRate is the share of what they resolved that came back.
	ReopenedRate float64 `json:"reopened_rate"`
}

// AgentPerformance is R5.
type AgentPerformance struct {
	Rows []AgentRow `json:"rows"`
	// Note states what the durations measure, because "median response time"
	// means three different things depending on who is asked.
	Note string `json:"note"`
}

// AgentPerformance summarises response and resolution times per agent.
//
// Durations are calendar time. Business-hours-aware figures are a separate
// question, and presenting calendar time as if it excluded nights would be
// worse than saying which it is.
func (s *Service) AgentPerformance(ctx context.Context, v Viewer, r Range, teamID *uuid.UUID) (*AgentPerformance, error) {
	type row struct {
		UserID              uuid.UUID
		Name                string
		Handled             int64
		FirstResponseMedian *float64
		FirstResponseP90    *float64
		ResolutionMedian    *float64
		ResolutionP90       *float64
		Resolved            int64
		Reopened            int64
	}
	var rows []row

	query := s.DB.WithContext(ctx).Table("conversations AS c").
		Select(`u.id AS user_id,
			u.full_name AS name,
			count(*) AS handled,
			percentile_cont(0.5) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.first_response_at - c.first_customer_message_at))
			) FILTER (WHERE c.first_response_at IS NOT NULL AND c.first_customer_message_at IS NOT NULL)
				AS first_response_median,
			percentile_cont(0.9) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.first_response_at - c.first_customer_message_at))
			) FILTER (WHERE c.first_response_at IS NOT NULL AND c.first_customer_message_at IS NOT NULL)
				AS first_response_p90,
			percentile_cont(0.5) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.resolved_at - c.opened_at))
			) FILTER (WHERE c.resolved_at IS NOT NULL AND c.resolved_by_id = u.id)
				AS resolution_median,
			percentile_cont(0.9) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.resolved_at - c.opened_at))
			) FILTER (WHERE c.resolved_at IS NOT NULL AND c.resolved_by_id = u.id)
				AS resolution_p90,
			count(*) FILTER (WHERE c.resolved_by_id = u.id) AS resolved,
			count(*) FILTER (WHERE c.resolved_by_id = u.id AND c.reopened_count > 0) AS reopened`).
		// An agent counts as handling a conversation if they answered it first
		// or closed it. Assignment alone is not work.
		Joins("JOIN users u ON u.id = c.first_responder_id OR u.id = c.resolved_by_id").
		Where("c.organization_id = ?", v.OrgID).
		Where("c.opened_at >= ? AND c.opened_at <= ?", r.From, r.To)

	if teamID != nil {
		query = query.Where("c.team_id = ?", *teamID)
	}
	if !v.SeesEveryone {
		// A leaderboard everyone can read is a performance review nobody
		// agreed to, so an agent sees their own row only.
		query = query.Where("u.id = ?", v.UserID)
	}

	if err := query.Group("u.id, u.full_name").Order("handled DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := &AgentPerformance{
		Rows: make([]AgentRow, 0, len(rows)),
		Note: "Durations are calendar time, including nights and weekends.",
	}
	for _, item := range rows {
		agent := AgentRow{
			UserID:                     item.UserID.String(),
			Name:                       item.Name,
			Handled:                    item.Handled,
			FirstResponseMedianSeconds: item.FirstResponseMedian,
			FirstResponseP90Seconds:    item.FirstResponseP90,
			ResolutionMedianSeconds:    item.ResolutionMedian,
			ResolutionP90Seconds:       item.ResolutionP90,
			Resolved:                   item.Resolved,
		}
		if item.Resolved > 0 {
			agent.ReopenedRate = float64(item.Reopened) / float64(item.Resolved) * 100
		}
		out.Rows = append(out.Rows, agent)
	}
	return out, nil
}

// TaskRow is one agent's follow-up workload.
type TaskRow struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`

	Open     int64 `json:"open"`
	Overdue  int64 `json:"overdue"`
	DueToday int64 `json:"due_today"`

	Completed int64 `json:"completed"`
	// OnTimeRate is the share of completions that beat their deadline, which
	// is the number that says whether the deadlines mean anything.
	OnTimeRate float64 `json:"on_time_rate"`
	// MedianLateSeconds is how late the late ones were.
	MedianLateSeconds *float64 `json:"median_late_seconds,omitempty"`
}

// TasksByAgent is R4.
type TasksByAgent struct {
	Rows []TaskRow `json:"rows"`
}

// TasksByAgent summarises follow-up work per owner.
//
// Overdue is computed at read time from the deadline, never stored: a stored
// flag is wrong from the moment a task lapses until the next job tick, which
// is exactly the window somebody is looking at this report.
func (s *Service) TasksByAgent(ctx context.Context, v Viewer, r Range, teamID *uuid.UUID, typeKey string) (*TasksByAgent, error) {
	type row struct {
		UserID     uuid.UUID
		Name       string
		Open       int64
		Overdue    int64
		DueToday   int64
		Completed  int64
		OnTime     int64
		MedianLate *float64
	}
	var rows []row

	endOfToday := endOfDay(nowIn(r.Location), r.Location)

	query := s.DB.WithContext(ctx).Table("tasks AS t").
		Select(`u.id AS user_id, u.full_name AS name,
			count(*) FILTER (WHERE t.status = ?) AS open,
			count(*) FILTER (WHERE t.status = ? AND t.due_at < now()) AS overdue,
			count(*) FILTER (WHERE t.status = ? AND t.due_at <= ?) AS due_today,
			count(*) FILTER (WHERE t.status = ? AND t.completed_at BETWEEN ? AND ?) AS completed,
			count(*) FILTER (WHERE t.status = ? AND t.completed_at BETWEEN ? AND ? AND t.completed_at <= t.due_at) AS on_time,
			percentile_cont(0.5) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (t.completed_at - t.due_at))
			) FILTER (WHERE t.status = ? AND t.completed_at > t.due_at) AS median_late`,
			models.TaskOpen,
			models.TaskOpen,
			models.TaskOpen, endOfToday,
			models.TaskCompleted, r.From, r.To,
			models.TaskCompleted, r.From, r.To,
			models.TaskCompleted).
		Joins("JOIN users u ON u.id = t.owner_id").
		Where("t.organization_id = ? AND t.deleted_at IS NULL", v.OrgID)

	if typeKey != "" {
		query = query.Joins("JOIN task_types tt ON tt.id = t.type_id").Where("tt.key = ?", typeKey)
	}
	if teamID != nil {
		query = query.Joins("JOIN team_members tm ON tm.user_id = u.id").Where("tm.team_id = ?", *teamID)
	}
	if !v.SeesEveryone {
		query = query.Where("u.id = ?", v.UserID)
	}

	if err := query.Group("u.id, u.full_name").Order("overdue DESC, open DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := &TasksByAgent{Rows: make([]TaskRow, 0, len(rows))}
	for _, item := range rows {
		agent := TaskRow{
			UserID:            item.UserID.String(),
			Name:              item.Name,
			Open:              item.Open,
			Overdue:           item.Overdue,
			DueToday:          item.DueToday,
			Completed:         item.Completed,
			MedianLateSeconds: item.MedianLate,
		}
		if item.Completed > 0 {
			agent.OnTimeRate = float64(item.OnTime) / float64(item.Completed) * 100
		}
		out.Rows = append(out.Rows, agent)
	}
	return out, nil
}
