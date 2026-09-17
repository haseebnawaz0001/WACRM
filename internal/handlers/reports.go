package handlers

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/reports"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Reports returns the report service.
func (a *App) Reports() *reports.Service { return reports.New(a.DB) }

// reportContext reads the shared query arguments every CRM report accepts.
//
// Dates are interpreted in the organization's timezone: "last week" means the
// business's week, and a report bucketed in UTC puts a Monday-morning
// conversation in Karachi into Sunday.
func (a *App) reportContext(r *fastglue.Request, orgID, userID uuid.UUID) (reports.Viewer, reports.Range) {
	args := r.RequestCtx.QueryArgs()

	viewer := reports.Viewer{
		OrgID:  orgID,
		UserID: userID,
		// Whoever may read the team's reports sees every row; everybody else
		// sees their own, because a leaderboard is a performance review.
		SeesEveryone: a.HasPermission(userID, models.ResourceReports, models.ActionRead, orgID),
	}

	return viewer, reports.NewRange(
		optionalDateArg(args, "from"),
		optionalDateArg(args, "to"),
		string(args.Peek("interval")),
		a.OrgLocation(orgID),
	)
}

// ContactsBySourceReport is R1.
func (a *App) ContactsBySourceReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionRead)
	if err != nil {
		return err
	}
	viewer, period := a.reportContext(r, orgID, userID)

	split := string(r.RequestCtx.QueryArgs().Peek("split"))
	result, err := a.Reports().ContactsBySource(context.Background(), viewer, period, split)
	if err != nil {
		a.Log.Error("Failed to build contacts-by-source report", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// LifecycleFunnelReport is R2.
func (a *App) LifecycleFunnelReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionRead)
	if err != nil {
		return err
	}
	viewer, period := a.reportContext(r, orgID, userID)

	result, err := a.Reports().LifecycleFunnel(context.Background(), viewer, period)
	if err != nil {
		a.Log.Error("Failed to build lifecycle funnel", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// PipelineFunnelReport is R3's funnel and win rate.
func (a *App) PipelineFunnelReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionRead)
	if err != nil {
		return err
	}
	// Deal figures are deal data: reading the pipeline report must not be a
	// way around not being allowed to see deals.
	if !a.HasPermission(userID, models.ResourceDeals, models.ActionRead, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
	}

	pipelineID, err := a.reportPipeline(r, orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	viewer, period := a.reportContext(r, orgID, userID)
	result, err := a.Reports().PipelineFunnel(context.Background(), viewer, period, pipelineID)
	if err != nil {
		a.Log.Error("Failed to build pipeline funnel", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// PipelineForecastReport is R3's forward look.
func (a *App) PipelineForecastReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionRead)
	if err != nil {
		return err
	}
	if !a.HasPermission(userID, models.ResourceDeals, models.ActionRead, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
	}

	pipelineID, err := a.reportPipeline(r, orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	months := 6
	if n, ok := optionalIntArg(r.RequestCtx.QueryArgs(), "months"); ok {
		months = n
	}

	viewer := reports.Viewer{OrgID: orgID, UserID: userID, SeesEveryone: true}
	result, err := a.Reports().PipelineForecast(context.Background(), viewer, pipelineID, months, a.OrgLocation(orgID))
	if err != nil {
		a.Log.Error("Failed to build pipeline forecast", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// reportPipeline resolves which board a pipeline report is about, defaulting
// to the organization's own default rather than making the caller look it up.
func (a *App) reportPipeline(r *fastglue.Request, orgID uuid.UUID) (uuid.UUID, error) {
	if id, ok := optionalUUIDArg(r.RequestCtx.QueryArgs(), "pipeline_id"); ok {
		return *id, nil
	}
	pipeline, err := a.Deals().DefaultPipeline(context.Background(), orgID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("this organization has no pipeline yet")
	}
	return pipeline.ID, nil
}

// TasksByAgentReport is R4.
func (a *App) TasksByAgentReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceTasks, models.ActionRead)
	if err != nil {
		return err
	}
	viewer, period := a.reportContext(r, orgID, userID)

	args := r.RequestCtx.QueryArgs()
	teamID, _ := optionalUUIDArg(args, "team_id")

	result, err := a.Reports().TasksByAgent(context.Background(), viewer, period, teamID,
		string(args.Peek("type")))
	if err != nil {
		a.Log.Error("Failed to build tasks-by-agent report", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// AgentPerformanceReport is R5.
func (a *App) AgentPerformanceReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAnalyticsAgents, models.ActionRead)
	if err != nil {
		return err
	}
	viewer, period := a.reportContext(r, orgID, userID)
	// Team-wide figures need the team-wide permission; analytics.agents:read
	// alone is "my own numbers".
	viewer.SeesEveryone = a.HasPermission(userID, models.ResourceAnalytics, models.ActionRead, orgID) ||
		a.HasPermission(userID, models.ResourceReports, models.ActionRead, orgID)

	teamID, _ := optionalUUIDArg(r.RequestCtx.QueryArgs(), "team_id")

	result, err := a.Reports().AgentPerformance(context.Background(), viewer, period, teamID)
	if err != nil {
		a.Log.Error("Failed to build agent performance report", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// ExportReport writes any CRM report as CSV.
//
// A spreadsheet is where numbers go to be argued with, and refusing to hand
// them over just means somebody retypes them.
func (a *App) ExportReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionExport)
	if err != nil {
		return err
	}
	key, _ := r.RequestCtx.UserValue("key").(string)
	viewer, period := a.reportContext(r, orgID, userID)

	rows, err := a.reportRows(context.Background(), key, r, viewer, period, orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	var buf strings.Builder
	writer := csv.NewWriter(&buf)
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to write the export", nil, "")
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to write the export", nil, "")
	}

	r.RequestCtx.Response.Header.Set("Content-Type", "text/csv; charset=utf-8")
	r.RequestCtx.Response.Header.Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", key+"-"+period.From.Format("2006-01-02")+".csv"))
	r.RequestCtx.SetStatusCode(fasthttp.StatusOK)
	r.RequestCtx.SetBodyString(buf.String())
	return nil
}

// CampaignRepliesReport is R6: did the campaign work?
//
// "Delivered" and "read" are Meta's numbers, and a campaign can score well on
// both while achieving nothing. A reply is the first thing a customer does
// that the business asked for (plan 09).
func (a *App) CampaignRepliesReport(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceReports, models.ActionRead)
	if err != nil {
		return err
	}
	viewer, period := a.reportContext(r, orgID, userID)

	result, err := a.Reports().CampaignReplies(context.Background(), viewer, period)
	if err != nil {
		a.Log.Error("Failed to build campaign replies report", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to build the report", nil, "")
	}
	return r.SendEnvelope(result)
}

// reportRows renders one report as CSV rows, header first.
func (a *App) reportRows(ctx context.Context, key string, r *fastglue.Request, viewer reports.Viewer, period reports.Range, orgID uuid.UUID) ([][]string, error) {
	switch key {
	case "contacts-by-source":
		result, err := a.Reports().ContactsBySource(ctx, viewer, period, "")
		if err != nil {
			return nil, err
		}
		rows := [][]string{{"source", "contacts", "share_percent", "became_customer"}}
		for _, row := range result.Totals {
			rows = append(rows, []string{
				row.Source,
				strconv.FormatInt(row.Contacts, 10),
				strconv.FormatFloat(row.Share, 'f', 1, 64),
				strconv.FormatInt(row.BecameCustomer, 10),
			})
		}
		return rows, nil

	case "lifecycle-funnel":
		result, err := a.Reports().LifecycleFunnel(ctx, viewer, period)
		if err != nil {
			return nil, err
		}
		rows := [][]string{{"stage", "reached", "conversion_percent"}}
		for _, step := range result.Steps {
			rows = append(rows, []string{
				step.Label,
				strconv.FormatInt(step.Reached, 10),
				strconv.FormatFloat(step.Conversion, 'f', 1, 64),
			})
		}
		return rows, nil

	case "agent-performance":
		result, err := a.Reports().AgentPerformance(ctx, viewer, period, nil)
		if err != nil {
			return nil, err
		}
		rows := [][]string{{
			"agent", "handled", "first_response_median_seconds", "first_response_p90_seconds",
			"resolution_median_seconds", "resolved", "reopened_rate_percent",
		}}
		for _, row := range result.Rows {
			rows = append(rows, []string{
				row.Name,
				strconv.FormatInt(row.Handled, 10),
				formatSeconds(row.FirstResponseMedianSeconds),
				formatSeconds(row.FirstResponseP90Seconds),
				formatSeconds(row.ResolutionMedianSeconds),
				strconv.FormatInt(row.Resolved, 10),
				strconv.FormatFloat(row.ReopenedRate, 'f', 1, 64),
			})
		}
		return rows, nil

	case "tasks-by-agent":
		teamID, _ := optionalUUIDArg(r.RequestCtx.QueryArgs(), "team_id")
		result, err := a.Reports().TasksByAgent(ctx, viewer, period, teamID, "")
		if err != nil {
			return nil, err
		}
		rows := [][]string{{"agent", "open", "overdue", "due_today", "completed", "on_time_percent"}}
		for _, row := range result.Rows {
			rows = append(rows, []string{
				row.Name,
				strconv.FormatInt(row.Open, 10),
				strconv.FormatInt(row.Overdue, 10),
				strconv.FormatInt(row.DueToday, 10),
				strconv.FormatInt(row.Completed, 10),
				strconv.FormatFloat(row.OnTimeRate, 'f', 1, 64),
			})
		}
		return rows, nil

	case "pipeline-funnel":
		pipelineID, err := a.reportPipeline(r, orgID)
		if err != nil {
			return nil, err
		}
		result, err := a.Reports().PipelineFunnel(ctx, viewer, period, pipelineID)
		if err != nil {
			return nil, err
		}
		rows := [][]string{{"stage", "deals", "conversion_percent", "median_days_in_previous_stage"}}
		for _, step := range result.Steps {
			days := ""
			if step.MedianDaysFromPrevious != nil {
				days = strconv.FormatFloat(*step.MedianDaysFromPrevious, 'f', 1, 64)
			}
			rows = append(rows, []string{
				step.Label,
				strconv.FormatInt(step.Reached, 10),
				strconv.FormatFloat(step.Conversion, 'f', 1, 64),
				days,
			})
		}
		return rows, nil

	case "campaign-replies":
		result, err := a.Reports().CampaignReplies(ctx, viewer, period)
		if err != nil {
			return nil, err
		}
		rows := [][]string{{"campaign", "started_at", "recipients", "delivered", "replied", "reply_rate_percent"}}
		for _, row := range result.Rows {
			rows = append(rows, []string{
				row.Name,
				row.StartedAt.Format(time.RFC3339),
				strconv.FormatInt(row.Recipients, 10),
				strconv.FormatInt(row.Delivered, 10),
				strconv.FormatInt(row.Replied, 10),
				strconv.FormatFloat(row.ReplyRate, 'f', 1, 64),
			})
		}
		return rows, nil
	}

	return nil, fmt.Errorf("%q is not a report", key)
}

// formatSeconds renders an optional duration for a spreadsheet, leaving it
// blank rather than writing a misleading zero when there is no measurement.
func formatSeconds(seconds *float64) string {
	if seconds == nil {
		return ""
	}
	return strconv.FormatFloat(*seconds, 'f', 0, 64)
}
