package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// ListPipelines returns the organization's boards with their stages.
func (a *App) ListPipelines(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourcePipelines, models.ActionRead)
	if err != nil {
		return err
	}

	includeArchived := string(r.RequestCtx.QueryArgs().Peek("include_archived")) == "true"
	rows, err := a.Deals().ListPipelines(context.Background(), orgID, includeArchived)
	if err != nil {
		a.Log.Error("Failed to list pipelines", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load pipelines", nil, "")
	}
	return r.SendEnvelope(map[string]any{"pipelines": rows})
}

type pipelineRequest struct {
	Name                *string `json:"name"`
	ObjectLabelSingular *string `json:"object_label_singular"`
	ObjectLabelPlural   *string `json:"object_label_plural"`
	Currency            *string `json:"currency"`
	IsDefault           *bool   `json:"is_default"`
	Position            *int    `json:"position"`
}

// CreatePipeline adds a board with its default stages.
func (a *App) CreatePipeline(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionWrite)
	if err != nil {
		return err
	}

	var req pipelineRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	currency := ""
	if req.Currency != nil {
		currency = strings.ToUpper(*req.Currency)
	}

	pipeline, err := a.Deals().CreatePipeline(context.Background(), orgID, name, currency, &userID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	// Labels are an edit rather than part of creation, so the create path has
	// one job and the validation lives in one place.
	if req.ObjectLabelSingular != nil || req.ObjectLabelPlural != nil {
		pipeline, err = a.Deals().UpdatePipeline(context.Background(), orgID, pipeline.ID, deals.PipelineInput{
			ObjectLabelSingular: req.ObjectLabelSingular,
			ObjectLabelPlural:   req.ObjectLabelPlural,
		})
		if err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
		}
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, pipeline.ID, models.AuditActionCreated, nil,
		map[string]any{"name": pipeline.Name})
	return r.SendEnvelope(map[string]any{"pipeline": pipeline})
}

// GetPipeline returns one board with its stages.
func (a *App) GetPipeline(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourcePipelines, models.ActionRead)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	pipeline, err := a.Deals().GetPipeline(context.Background(), orgID, pipelineID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
	}
	return r.SendEnvelope(map[string]any{"pipeline": pipeline})
}

// UpdatePipeline edits a board's settings.
func (a *App) UpdatePipeline(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionWrite)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	var req pipelineRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	pipeline, err := a.Deals().UpdatePipeline(context.Background(), orgID, pipelineID, deals.PipelineInput{
		Name:                req.Name,
		ObjectLabelSingular: req.ObjectLabelSingular,
		ObjectLabelPlural:   req.ObjectLabelPlural,
		Currency:            req.Currency,
		IsDefault:           req.IsDefault,
		Position:            req.Position,
	})
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, pipeline.ID, models.AuditActionUpdated, nil,
		map[string]any{"name": pipeline.Name})
	return r.SendEnvelope(map[string]any{"pipeline": pipeline})
}

// DeletePipeline removes a board, or archives it when asked.
func (a *App) DeletePipeline(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionDelete)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	// Archiving is the safe retirement: it keeps closed deals reportable.
	if string(r.RequestCtx.QueryArgs().Peek("archive")) == "true" {
		pipeline, archiveErr := a.Deals().ArchivePipeline(context.Background(), orgID, pipelineID, true)
		if archiveErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
		}
		a.logAudit(orgID, userID, models.ResourcePipelines, pipelineID, models.AuditActionUpdated, nil,
			map[string]any{"archived": true})
		return r.SendEnvelope(map[string]any{"pipeline": pipeline})
	}

	if err := a.Deals().DeletePipeline(context.Background(), orgID, pipelineID); err != nil {
		switch {
		case errors.Is(err, deals.ErrNotFound):
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
		case errors.Is(err, deals.ErrPipelineInUse):
			return r.SendErrorEnvelope(fasthttp.StatusConflict, err.Error(), nil, "")
		}
		a.Log.Error("Failed to delete pipeline", "error", err, "pipeline_id", pipelineID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete pipeline", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, pipelineID, models.AuditActionDeleted, nil, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

type stageRequest struct {
	Name        *string `json:"name"`
	StageType   *string `json:"stage_type"`
	Probability *int    `json:"probability"`
	Color       *string `json:"color"`
	RottingDays *int    `json:"rotting_days"`
	Position    *int    `json:"position"`
}

func (req stageRequest) input() deals.StageInput {
	return deals.StageInput{
		Name:        req.Name,
		StageType:   req.StageType,
		Probability: req.Probability,
		Color:       req.Color,
		RottingDays: req.RottingDays,
		Position:    req.Position,
	}
}

// CreateStage adds a column to a board.
func (a *App) CreateStage(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionWrite)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	var req stageRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	stage, err := a.Deals().CreateStage(context.Background(), orgID, pipelineID, req.input())
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, stage.ID, models.AuditActionCreated, nil,
		map[string]any{"stage": stage.Name})
	return r.SendEnvelope(map[string]any{"stage": stage})
}

// UpdateStage edits a column.
func (a *App) UpdateStage(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionWrite)
	if err != nil {
		return err
	}
	stageID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid stage id", nil, "")
	}

	var req stageRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	stage, err := a.Deals().UpdateStage(context.Background(), orgID, stageID, req.input())
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Stage not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, stage.ID, models.AuditActionUpdated, nil,
		map[string]any{"stage": stage.Name})
	return r.SendEnvelope(map[string]any{"stage": stage})
}

// ReorderStages writes a new column order.
func (a *App) ReorderStages(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionWrite)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	var req struct {
		StageIDs []string `json:"stage_ids"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	order := make([]uuid.UUID, 0, len(req.StageIDs))
	for _, raw := range req.StageIDs {
		parsed, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid stage id", nil, "")
		}
		order = append(order, parsed)
	}

	if err := a.Deals().ReorderStages(context.Background(), orgID, pipelineID, order); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, pipelineID, models.AuditActionUpdated, nil,
		map[string]any{"reordered": len(order)})

	pipeline, err := a.Deals().GetPipeline(context.Background(), orgID, pipelineID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
	}
	return r.SendEnvelope(map[string]any{"pipeline": pipeline})
}

// DeleteStage removes a column, moving any cards to move_deals_to.
func (a *App) DeleteStage(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourcePipelines, models.ActionDelete)
	if err != nil {
		return err
	}
	stageID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid stage id", nil, "")
	}

	moveTo, _ := optionalUUIDArg(r.RequestCtx.QueryArgs(), "move_deals_to")

	if err := a.Deals().DeleteStage(context.Background(), orgID, stageID, moveTo); err != nil {
		switch {
		case errors.Is(err, deals.ErrNotFound):
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Stage not found", nil, "")
		case errors.Is(err, deals.ErrStageHasDeals):
			// 409 with the reason: the UI turns this into "move them where?".
			return r.SendErrorEnvelope(fasthttp.StatusConflict, err.Error(), nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourcePipelines, stageID, models.AuditActionDeleted, nil, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

// boardFilterFrom reads the shared board filters off the query string.
func (a *App) boardFilterFrom(r *fastglue.Request, orgID, userID uuid.UUID) deals.BoardFilter {
	args := r.RequestCtx.QueryArgs()
	f := deals.BoardFilter{
		Status:    strings.ToLower(string(args.Peek("status"))),
		Search:    string(args.Peek("search")),
		CloseFrom: optionalDateArg(args, "close_from"),
		CloseTo:   optionalDateArg(args, "close_to"),
		Scope:     a.dealScope(orgID, userID),
	}
	if id, ok := optionalUUIDArg(args, "owner_id"); ok {
		f.OwnerID = id
	}
	// "me" saves the client from knowing its own id to filter its own board.
	if strings.EqualFold(string(args.Peek("owner_id")), "me") {
		f.OwnerID = &userID
	}
	if n, ok := optionalIntArg(args, "limit"); ok {
		f.Limit = n
	}
	return f
}

// BoardColumnResponse is one column as the board draws it.
type BoardColumnResponse struct {
	Stage    models.PipelineStage `json:"stage"`
	Count    int64                `json:"count"`
	Value    float64              `json:"total_value"`
	Weighted float64              `json:"weighted_value"`
	Deals    []DealResponse       `json:"deals"`
	HasMore  bool                 `json:"has_more"`
}

// Board returns a pipeline's columns, cards and totals.
func (a *App) Board(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}

	pipeline, columns, err := a.Deals().Board(context.Background(), orgID, pipelineID,
		a.boardFilterFrom(r, orgID, userID))
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Pipeline not found", nil, "")
		}
		a.Log.Error("Failed to load board", "error", err, "pipeline_id", pipelineID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load board", nil, "")
	}

	out := make([]BoardColumnResponse, 0, len(columns))
	for _, column := range columns {
		cards := make([]DealResponse, 0, len(column.Deals))
		for _, card := range column.Deals {
			// The stage is the column, so attaching it here is what lets each
			// card report whether it is rotting without another query.
			stage := column.Stage
			card.Stage = &stage
			cards = append(cards, a.toDealResponse(orgID, card))
		}
		out = append(out, BoardColumnResponse{
			Stage:    column.Stage,
			Count:    column.Count,
			Value:    column.Value,
			Weighted: column.Weighted,
			Deals:    cards,
			HasMore:  column.HasMore,
		})
	}

	return r.SendEnvelope(map[string]any{"pipeline": pipeline, "columns": out})
}

// StageDeals loads one more page of cards for a column.
func (a *App) StageDeals(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}
	pipelineID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid pipeline id", nil, "")
	}
	stageID, err := uuid.Parse(r.RequestCtx.UserValue("stageId").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid stage id", nil, "")
	}

	cursor := string(r.RequestCtx.QueryArgs().Peek("cursor"))
	cards, more, err := a.Deals().StageDeals(context.Background(), orgID, pipelineID, stageID, cursor,
		a.boardFilterFrom(r, orgID, userID))
	if err != nil {
		a.Log.Error("Failed to load stage deals", "error", err, "stage_id", stageID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load deals", nil, "")
	}

	items := make([]DealResponse, 0, len(cards))
	for _, card := range cards {
		items = append(items, a.toDealResponse(orgID, card))
	}
	next := ""
	if more && len(cards) > 0 {
		next = cards[len(cards)-1].BoardPosition
	}
	return r.SendEnvelope(map[string]any{"deals": items, "has_more": more, "next_cursor": next})
}
