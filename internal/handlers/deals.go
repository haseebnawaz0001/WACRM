package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// Deals returns the deal service.
func (a *App) Deals() *deals.Service { return deals.New(a.DB) }

// DealResponse is the API shape of a deal.
type DealResponse struct {
	ID                string     `json:"id"`
	PipelineID        string     `json:"pipeline_id"`
	StageID           string     `json:"stage_id"`
	StageName         string     `json:"stage_name,omitempty"`
	ContactID         string     `json:"contact_id"`
	ContactName       string     `json:"contact_name,omitempty"`
	ContactPhone      string     `json:"contact_phone,omitempty"`
	ConversationID    string     `json:"conversation_id,omitempty"`
	Title             string     `json:"title"`
	Value             float64    `json:"value"`
	Currency          string     `json:"currency"`
	OwnerID           string     `json:"owner_id,omitempty"`
	ExpectedCloseDate *time.Time `json:"expected_close_date,omitempty"`
	Status            string     `json:"status"`
	LostReason        string     `json:"lost_reason,omitempty"`
	StageEnteredAt    time.Time  `json:"stage_entered_at"`
	BoardPosition     string     `json:"board_position"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
	// Rotting is computed on read, never stored: a stored flag is wrong from
	// the moment a card goes stale until the next job tick.
	Rotting   bool      `json:"rotting"`
	CreatedAt time.Time `json:"created_at"`
}

func toDealResponse(d models.Deal) DealResponse {
	out := DealResponse{
		ID:                d.ID.String(),
		PipelineID:        d.PipelineID.String(),
		StageID:           d.StageID.String(),
		ContactID:         d.ContactID.String(),
		Title:             d.Title,
		Value:             d.Value,
		Currency:          d.Currency,
		ExpectedCloseDate: d.ExpectedCloseDate,
		Status:            d.Status,
		LostReason:        d.LostReason,
		StageEnteredAt:    d.StageEnteredAt,
		BoardPosition:     d.BoardPosition,
		ClosedAt:          d.ClosedAt,
		CreatedAt:         d.CreatedAt,
	}
	if d.OwnerID != nil {
		out.OwnerID = d.OwnerID.String()
	}
	if d.ConversationID != nil {
		out.ConversationID = d.ConversationID.String()
	}
	if d.Contact != nil {
		out.ContactName = d.Contact.ProfileName
		out.ContactPhone = d.Contact.PhoneNumber
	}
	if d.Stage != nil {
		out.StageName = d.Stage.Name
		out.Rotting = d.IsRotting(*d.Stage, time.Now().UTC())
	}
	return out
}

// dealScope restricts a query to the deals the caller may see.
func (a *App) dealScope(orgID, userID uuid.UUID) func(*gorm.DB) *gorm.DB {
	return deals.VisibleTo(userID, a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID))
}

// broadcastDeal tells open boards that a card changed.
//
// The payload is ids and positions, not the deal: boards are shared and a
// viewer who cannot see this deal must not learn its title from a broadcast.
// Clients refetch what they are allowed to read.
func (a *App) broadcastDeal(orgID uuid.UUID, deal *models.Deal) {
	if a.WSHub == nil || deal == nil {
		return
	}
	a.WSHub.BroadcastToOrg(orgID, websocket.WSMessage{
		Type: websocket.TypeDealUpdated,
		Payload: map[string]any{
			"deal_id":        deal.ID.String(),
			"pipeline_id":    deal.PipelineID.String(),
			"stage_id":       deal.StageID.String(),
			"board_position": deal.BoardPosition,
			"status":         deal.Status,
		},
	})
}

// ListDeals returns deals for the list/table view.
func (a *App) ListDeals(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}

	args := r.RequestCtx.QueryArgs()
	opts := deals.ListOpts{Status: strings.ToLower(string(args.Peek("status")))}
	if id, ok := optionalUUIDArg(args, "pipeline_id"); ok {
		opts.PipelineID = id
	}
	if id, ok := optionalUUIDArg(args, "contact_id"); ok {
		opts.ContactID = id
	}
	if id, ok := optionalUUIDArg(args, "owner_id"); ok {
		opts.OwnerID = id
	}
	opts.Scope = a.dealScope(orgID, userID)

	rows, err := a.Deals().List(context.Background(), orgID, opts)
	if err != nil {
		a.Log.Error("Failed to list deals", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load deals", nil, "")
	}

	items := make([]DealResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDealResponse(row))
	}
	return r.SendEnvelope(map[string]any{"deals": items, "total": len(items)})
}

type createDealRequest struct {
	ContactID         string     `json:"contact_id"`
	PipelineID        string     `json:"pipeline_id"`
	StageID           string     `json:"stage_id"`
	ConversationID    string     `json:"conversation_id"`
	Title             string     `json:"title"`
	Value             float64    `json:"value"`
	Currency          string     `json:"currency"`
	OwnerID           string     `json:"owner_id"`
	ExpectedCloseDate *time.Time `json:"expected_close_date"`
}

// CreateDeal adds an opportunity.
func (a *App) CreateDeal(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionWrite)
	if err != nil {
		return err
	}

	var req createDealRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	contactID, err := uuid.Parse(req.ContactID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	in := deals.CreateInput{
		OrgID:     orgID,
		ContactID: contactID,
		Title:     strings.TrimSpace(req.Title),
		Value:     req.Value,
		Currency:  strings.ToUpper(req.Currency),
		CreatedBy: &userID,
	}
	if parsed, parseErr := uuid.Parse(req.PipelineID); parseErr == nil {
		in.PipelineID = &parsed
	}
	if parsed, parseErr := uuid.Parse(req.StageID); parseErr == nil {
		in.StageID = &parsed
	}
	if parsed, parseErr := uuid.Parse(req.OwnerID); parseErr == nil {
		in.OwnerID = &parsed
	}
	if parsed, parseErr := uuid.Parse(req.ConversationID); parseErr == nil {
		in.ConversationID = &parsed
	}
	in.ExpectedCloseDate = req.ExpectedCloseDate

	deal, err := a.Deals().Create(context.Background(), in)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceDeals, deal.ID, models.AuditActionCreated, nil,
		map[string]any{"title": deal.Title, "value": deal.Value, "stage_id": deal.StageID})

	a.broadcastDeal(orgID, deal)
	return r.SendEnvelope(map[string]any{"deal": toDealResponse(*deal)})
}

// GetDeal returns one deal.
func (a *App) GetDeal(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}
	dealID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
	}

	deal, err := a.Deals().Get(context.Background(), orgID, dealID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Deal not found", nil, "")
	}
	return r.SendEnvelope(map[string]any{"deal": toDealResponse(*deal)})
}

type updateDealRequest struct {
	Title             *string    `json:"title"`
	Value             *float64   `json:"value"`
	Currency          *string    `json:"currency"`
	OwnerID           *string    `json:"owner_id"`
	ExpectedCloseDate *time.Time `json:"expected_close_date"`
	// ClearCloseDate distinguishes "no date given" from "remove the date":
	// a null expected_close_date in JSON is indistinguishable from an absent one.
	ClearCloseDate bool    `json:"clear_close_date"`
	LostReason     *string `json:"lost_reason"`
}

// UpdateDeal edits a deal's details.
func (a *App) UpdateDeal(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionWrite)
	if err != nil {
		return err
	}
	dealID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
	}

	var req updateDealRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	in := deals.UpdateInput{
		Title:      req.Title,
		Value:      req.Value,
		Currency:   req.Currency,
		LostReason: req.LostReason,
	}
	if req.OwnerID != nil {
		var owner *uuid.UUID
		if *req.OwnerID != "" {
			parsed, parseErr := uuid.Parse(*req.OwnerID)
			if parseErr != nil {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid owner id", nil, "")
			}
			owner = &parsed
		}
		in.OwnerID = &owner
	}
	switch {
	case req.ClearCloseDate:
		var none *time.Time
		in.ExpectedCloseDate = &none
	case req.ExpectedCloseDate != nil:
		in.ExpectedCloseDate = &req.ExpectedCloseDate
	}

	deal, err := a.Deals().Update(context.Background(), orgID, dealID, in, crmActorForUser(userID))
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Deal not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceDeals, deal.ID, models.AuditActionUpdated, nil,
		map[string]any{"title": deal.Title, "value": deal.Value})

	a.broadcastDeal(orgID, deal)
	return r.SendEnvelope(map[string]any{"deal": toDealResponse(*deal)})
}

// DeleteDeal removes a deal.
func (a *App) DeleteDeal(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionDelete)
	if err != nil {
		return err
	}
	dealID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
	}

	if err := a.Deals().Delete(context.Background(), orgID, dealID, crmActorForUser(userID)); err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Deal not found", nil, "")
		}
		a.Log.Error("Failed to delete deal", "error", err, "deal_id", dealID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete deal", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceDeals, dealID, models.AuditActionDeleted, nil, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

type moveDealRequest struct {
	StageID    string `json:"stage_id"`
	BeforeID   string `json:"before_id"`
	AfterID    string `json:"after_id"`
	LostReason string `json:"lost_reason"`
}

// MoveDeal handles a drag-and-drop between or within columns.
func (a *App) MoveDeal(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionWrite)
	if err != nil {
		return err
	}
	dealID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
	}

	var req moveDealRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	stageID, err := uuid.Parse(req.StageID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid stage id", nil, "")
	}

	in := deals.MoveInput{StageID: stageID, LostReason: req.LostReason}
	if parsed, parseErr := uuid.Parse(req.BeforeID); parseErr == nil {
		in.BeforeID = &parsed
	}
	if parsed, parseErr := uuid.Parse(req.AfterID); parseErr == nil {
		in.AfterID = &parsed
	}

	deal, err := a.Deals().Move(context.Background(), orgID, dealID, in, crmActorForUser(userID))
	if err != nil {
		if errors.Is(err, deals.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Deal not found", nil, "")
		}
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceDeals, deal.ID, models.AuditActionUpdated, nil,
		map[string]any{"stage_id": deal.StageID, "status": deal.Status})

	a.broadcastDeal(orgID, deal)
	return r.SendEnvelope(map[string]any{"deal": toDealResponse(*deal)})
}

// DealHistory returns every stage move for a deal.
func (a *App) DealHistory(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}
	dealID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid deal id", nil, "")
	}

	history, err := a.Deals().History(context.Background(), orgID, dealID)
	if err != nil {
		a.Log.Error("Failed to load deal history", "error", err, "deal_id", dealID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load history", nil, "")
	}
	return r.SendEnvelope(map[string]any{"history": history})
}

// ContactDeals returns one contact's deals for the profile panel.
func (a *App) ContactDeals(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceDeals, models.ActionRead)
	if err != nil {
		return err
	}
	contactID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	status := strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("status")))
	if status == "" {
		status = "all"
	}

	rows, err := a.Deals().List(context.Background(), orgID, deals.ListOpts{
		ContactID: &contactID,
		Status:    status,
		Scope:     a.dealScope(orgID, userID),
	})
	if err != nil {
		a.Log.Error("Failed to load contact deals", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load deals", nil, "")
	}

	items := make([]DealResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDealResponse(row))
	}
	return r.SendEnvelope(map[string]any{"deals": items})
}

// optionalUUIDArg reads a uuid query argument, reporting whether it was given.
func optionalUUIDArg(args *fasthttp.Args, key string) (*uuid.UUID, bool) {
	raw := strings.TrimSpace(string(args.Peek(key)))
	if raw == "" {
		return nil, false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, false
	}
	return &parsed, true
}

// optionalIntArg reads an integer query argument.
func optionalIntArg(args *fasthttp.Args, key string) (int, bool) {
	raw := strings.TrimSpace(string(args.Peek(key)))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// optionalDateArg reads a YYYY-MM-DD query argument.
func optionalDateArg(args *fasthttp.Args, key string) *time.Time {
	raw := strings.TrimSpace(string(args.Peek(key)))
	if raw == "" {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil
	}
	return &parsed
}
