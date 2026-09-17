package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// MaxBulkConversations caps one bulk inbox action (plan 03).
const MaxBulkConversations = 200

// ListContactConversations returns one contact's conversation history.
//
// The inbox lists conversations across contacts; this is the other axis —
// every conversation this person has had, which is what the profile needs to
// answer "have we spoken before, and how did it end?" (plan 02).
func (a *App) ListContactConversations(r *fastglue.Request) error {
	orgID, userID, err := a.requireAnyPermission(r,
		perm(models.ResourceContacts, models.ActionRead),
		perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return err
	}

	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	// Scoped like the timeline: a contact's history is not reachable by id
	// alone by somebody the contact list will not show them (plan 10, S9).
	var contact models.Contact
	scoped := a.scopeAssignedContact(
		a.DB.Where("id = ? AND organization_id = ?", contactID, orgID), userID, orgID)
	if err := scoped.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	var rows []models.Conversation
	if err := a.DB.Where("organization_id = ? AND contact_id = ?", orgID, contactID).
		Order("opened_at DESC").
		Limit(100).
		Find(&rows).Error; err != nil {
		a.Log.Error("Failed to list contact conversations", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load conversations", nil, "")
	}

	shouldMask := a.ShouldMaskPhoneNumbers(orgID)
	out := make([]ConversationResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, a.toConversationResponse(orgID, row, shouldMask))
	}

	return r.SendEnvelope(map[string]any{"conversations": out, "total": len(out)})
}

// GetConversationByID returns one conversation by its own id.
//
// The other conversation endpoints are keyed by contact, because that is how
// an agent works: there is one live conversation per person. A notification, a
// webhook or a report names the conversation itself, including ones that have
// since been resolved, and following that reference needed an endpoint that
// takes it (plan 03).
func (a *App) GetConversationByID(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return err
	}

	conversationID, err := parsePathUUID(r, "id", "conversation")
	if err != nil {
		return nil
	}

	var conv models.Conversation
	if err := a.DB.Preload("Contact").
		Where("id = ? AND organization_id = ?", conversationID, orgID).
		First(&conv).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Conversation not found", nil, "")
	}

	var visible int64
	scoped := a.scopeAssignedContact(
		a.DB.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", conv.ContactID, orgID),
		userID, orgID)
	if err := scoped.Count(&visible).Error; err != nil || visible == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Conversation not found", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, conv, a.ShouldMaskPhoneNumbers(orgID)),
	})
}

// BulkConversationRequest is a list of conversation ids and one action.
type BulkConversationRequest struct {
	IDs    []uuid.UUID `json:"ids"`
	Action string      `json:"action"`
	// Until is the wake time for a snooze, RFC3339.
	Until *time.Time `json:"until"`
	// AssigneeID and TeamID are the target of an assign.
	AssigneeID *uuid.UUID `json:"assignee_id"`
	TeamID     *uuid.UUID `json:"team_id"`
}

// BulkConversations applies one action to several conversations (plan 03).
//
// Clearing a queue of forty resolved-but-unmarked conversations one at a time
// is work people abandon, and an inbox whose statuses nobody maintains stops
// describing anything.
//
// Ids are conversations, but the service works per contact: a conversation is
// the current state of talking to somebody, and the transitions are defined on
// that relationship. Each id is resolved to its contact first.
func (a *App) BulkConversations(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return err
	}

	var req BulkConversationRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if len(req.IDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "ids is required", nil, "")
	}
	if len(req.IDs) > MaxBulkConversations {
		return r.SendErrorEnvelope(fasthttp.StatusUnprocessableEntity,
			"Too many conversations in one request", map[string]any{"max": MaxBulkConversations}, "")
	}

	// Assigning is its own permission, and a bulk endpoint is not a way round
	// it (plan 03).
	if req.Action == "assign" &&
		!a.HasPermission(userID, models.ResourceChatAssign, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden,
			"You do not have permission to assign conversations", nil, "")
	}
	if req.Action == "snooze" {
		if req.Until == nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "A snooze needs an end time", nil, "")
		}
		if req.Until.Before(time.Now()) {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "The snooze time must be in the future", nil, "")
		}
	}

	actor := crmevents.UserActor(userID, "")
	svc := a.Conversations()
	ctx := context.Background()

	applied := 0
	failures := map[string]string{}

	for _, id := range req.IDs {
		var conv models.Conversation
		if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).
			First(&conv).Error; err != nil {
			failures[id.String()] = "not found"
			continue
		}

		var visible int64
		scoped := a.scopeAssignedContact(
			a.DB.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", conv.ContactID, orgID),
			userID, orgID)
		if err := scoped.Count(&visible).Error; err != nil || visible == 0 {
			failures[id.String()] = "not found"
			continue
		}

		var actionErr error
		switch req.Action {
		case "resolve":
			_, actionErr = svc.Resolve(ctx, orgID, conv.ContactID, models.ResolutionAgent, actor)
		case "pending":
			_, actionErr = svc.SetPending(ctx, orgID, conv.ContactID, actor)
		case "snooze":
			_, actionErr = svc.Snooze(ctx, orgID, conv.ContactID, req.Until.UTC(), actor)
		case "assign":
			_, actionErr = svc.Assign(ctx, orgID, conv.ContactID, req.AssigneeID, req.TeamID, actor)
		default:
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Unknown bulk action", nil, "")
		}

		if actionErr != nil {
			failures[id.String()] = actionErr.Error()
			continue
		}
		applied++
	}

	a.logAudit(orgID, userID, models.ResourceChat, uuid.Nil, models.AuditActionUpdated, nil,
		map[string]any{"action": req.Action, "requested": len(req.IDs), "applied": applied})

	return r.SendEnvelope(map[string]any{
		"applied":  applied,
		"failed":   len(failures),
		"failures": failures,
	})
}
