package handlers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Inbox views (plan 03).
const (
	// InboxViewMine is what this agent is handling.
	InboxViewMine = "mine"
	// InboxViewUnassigned is the queue: nobody has picked these up.
	InboxViewUnassigned = "unassigned"
	// InboxViewBot is being handled by the chatbot with no human involved.
	InboxViewBot = "bot"
	// InboxViewAll is everything the viewer may see.
	InboxViewAll = "all"

	// InboxViewUnanswered is everybody currently waiting on a reply, whoever
	// owns the conversation.
	//
	// waiting_since is the arrival time of the oldest customer message nobody
	// has answered, so it is the right question to ask: first_response_at
	// would only find conversations never answered at all, and miss the one
	// answered yesterday that has an unanswered question in it today.
	InboxViewUnanswered = "unanswered"
)

const (
	inboxDefaultLimit = 50
	inboxMaxLimit     = 200
)

// ConversationResponse is the API shape of a conversation.
type ConversationResponse struct {
	ID           string `json:"id"`
	ContactID    string `json:"contact_id"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	Status       string `json:"status"`
	AssigneeID   string `json:"assignee_id,omitempty"`
	TeamID       string `json:"team_id,omitempty"`
	Handling     string `json:"handling"`
	// BotActive is kept for clients written before handling existed. It is
	// derived, never stored (plan 10, S5).
	BotActive       bool       `json:"bot_active"`
	WhatsAppAccount string     `json:"whatsapp_account,omitempty"`
	SnoozedUntil    *time.Time `json:"snoozed_until,omitempty"`
	OpenedAt        time.Time  `json:"opened_at"`
	LastMessageAt   *time.Time `json:"last_message_at,omitempty"`
	LastMessagePrev string     `json:"last_message_preview,omitempty"`

	// WaitingSince is when the oldest unanswered customer message arrived.
	// The inbox shows it as "waiting 2h", which is the number an agent acts on.
	WaitingSince    *time.Time `json:"waiting_since,omitempty"`
	FirstResponseAt *time.Time `json:"first_response_at,omitempty"`
	MessageCount    int        `json:"message_count"`
	ReopenedCount   int        `json:"reopened_count"`
}

// ListInbox returns conversations for one view.
//
// The views answer the question an agent actually asks — "what is mine and
// still open?" — which the contact list could not, because there was no
// conversation state to filter on.
func (a *App) ListInbox(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return err
	}

	view := strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("view")))
	if view == "" {
		view = InboxViewMine
	}
	status := strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("status")))

	limit := inboxDefaultLimit
	if raw := string(r.RequestCtx.QueryArgs().Peek("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			limit = n
		}
	}
	if limit > inboxMaxLimit {
		limit = inboxMaxLimit
	}
	page := 1
	if raw := string(r.RequestCtx.QueryArgs().Peek("page")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			page = n
		}
	}

	query := a.DB.Model(&models.Conversation{}).
		Where("conversations.organization_id = ?", orgID)

	switch view {
	case InboxViewMine:
		query = query.Where("conversations.assignee_id = ?", userID)
	case InboxViewUnassigned:
		// The queue is unassigned conversations that a human should pick up.
		// Bot-handled ones are excluded: nobody is waiting on a person there.
		// A suppressed handoff is included — the customer asked for a person
		// and the request went nowhere, which is precisely a queue item.
		query = query.Where("conversations.assignee_id IS NULL AND conversations.handling <> ?",
			models.HandlingBot)
	case InboxViewBot:
		query = query.Where("conversations.handling = ? AND conversations.assignee_id IS NULL",
			models.HandlingBot)
	case InboxViewUnanswered:
		// Bot-handled conversations are excluded for the same reason they are
		// excluded from the queue: nobody is waiting on a person there.
		query = query.Where("conversations.waiting_since IS NOT NULL AND conversations.handling <> ?",
			models.HandlingBot)
	case InboxViewAll:
		// No extra predicate; the visibility scope below still applies.
	default:
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Unknown inbox view", nil, "")
	}

	if status != "" {
		query = query.Where("conversations.status = ?", status)
	} else {
		// Resolved conversations are history, not inbox. Asking for them
		// explicitly is supported; showing them by default is not.
		query = query.Where("conversations.status <> ?", models.ConversationResolved)
	}

	// An agent who cannot read every contact sees only conversations for the
	// contacts they can see, so the inbox cannot become a way around that.
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID) {
		query = query.Where(`conversations.assignee_id = ? OR EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = conversations.contact_id AND c.assigned_user_id = ?)`,
			userID, userID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		a.Log.Error("Failed to count inbox", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load inbox", nil, "")
	}

	// Newest first is right for reading an inbox and wrong for clearing one:
	// the person who has waited longest is the one to answer next, and they
	// are at the bottom of a list sorted by recency. `sort=waiting` puts them
	// first, and the unanswered view takes it as its default because that is
	// the only order that view is for.
	sort := strings.ToLower(string(r.RequestCtx.QueryArgs().Peek("sort")))
	if sort == "" && view == InboxViewUnanswered {
		sort = "waiting"
	}
	order := "conversations.last_message_at DESC NULLS LAST, conversations.opened_at DESC"
	if sort == "waiting" {
		order = "conversations.waiting_since ASC NULLS LAST, conversations.last_message_at DESC NULLS LAST"
	}

	var rows []models.Conversation
	if err := query.
		Preload("Contact").
		Order(order).
		Offset((page - 1) * limit).Limit(limit).
		Find(&rows).Error; err != nil {
		a.Log.Error("Failed to list inbox", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load inbox", nil, "")
	}

	shouldMask := a.ShouldMaskPhoneNumbers(orgID)
	items := make([]ConversationResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, a.toConversationResponse(orgID, row, shouldMask))
	}

	return r.SendEnvelope(map[string]any{
		"conversations": items,
		"total":         total,
		"page":          page,
		"limit":         limit,
		"view":          view,
	})
}

func (a *App) toConversationResponse(orgID uuid.UUID, c models.Conversation, shouldMask bool) ConversationResponse {
	out := ConversationResponse{
		ID:              c.ID.String(),
		ContactID:       c.ContactID.String(),
		Status:          string(c.Status),
		Handling:        string(c.Handling),
		BotActive:       c.IsBotHandled(),
		WhatsAppAccount: c.WhatsAppAccount,
		SnoozedUntil:    c.SnoozedUntil,
		OpenedAt:        c.OpenedAt,
		LastMessageAt:   c.LastMessageAt,
		WaitingSince:    c.WaitingSince,
		FirstResponseAt: c.FirstResponseAt,
		MessageCount:    c.MessageCount,
		ReopenedCount:   c.ReopenedCount,
	}
	if c.AssigneeID != nil {
		out.AssigneeID = c.AssigneeID.String()
	}
	if c.TeamID != nil {
		out.TeamID = c.TeamID.String()
	}
	if c.Contact != nil {
		name, phone := c.Contact.ProfileName, c.Contact.PhoneNumber
		if shouldMask {
			name, phone = a.MaskContactFields(orgID, name, phone)
		}
		out.ContactName = name
		out.ContactPhone = phone
		out.LastMessagePrev = c.Contact.LastMessagePreview
	}
	return out
}

// GetInboxCounts returns the badge counts for each view in one query.
//
// One grouped query rather than one per view: the sidebar shows every count at
// once, and four round trips for four badges is four times the cost for the
// same answer.
func (a *App) GetInboxCounts(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return err
	}

	type counts struct {
		Mine       int64
		Unassigned int64
		Bot        int64
		Unanswered int64
		All        int64
	}
	var out counts

	err = a.DB.Model(&models.Conversation{}).
		Select(`
			count(*) FILTER (WHERE assignee_id = ?) AS mine,
			count(*) FILTER (WHERE assignee_id IS NULL AND handling <> 'bot') AS unassigned,
			count(*) FILTER (WHERE assignee_id IS NULL AND handling = 'bot') AS bot,
			count(*) FILTER (WHERE waiting_since IS NOT NULL AND handling <> 'bot') AS unanswered,
			count(*) AS all`, userID).
		Where("organization_id = ? AND status <> ?", orgID, models.ConversationResolved).
		Scan(&out).Error
	if err != nil {
		a.Log.Error("Failed to count inbox views", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load inbox counts", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"mine": out.Mine, "unassigned": out.Unassigned, "bot": out.Bot,
		"unanswered": out.Unanswered, "all": out.All,
	})
}

// conversationActionRequest is the body shared by the status actions.
type conversationActionRequest struct {
	ContactID  string     `json:"contact_id"`
	Until      *time.Time `json:"until"`
	AssigneeID *string    `json:"assignee_id"`
	TeamID     *string    `json:"team_id"`
	Reason     string     `json:"reason"`
}

// contactForAction resolves and authorises the target contact.
func (a *App) contactForAction(r *fastglue.Request, orgID uuid.UUID, req conversationActionRequest) (uuid.UUID, bool) {
	contactID, err := uuid.Parse(req.ContactID)
	if err != nil {
		_ = r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
		return uuid.Nil, false
	}
	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID).First(&contact).Error; err != nil {
		_ = r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
		return uuid.Nil, false
	}
	return contactID, true
}

func decodeConversationAction(r *fastglue.Request) (conversationActionRequest, bool) {
	var req conversationActionRequest
	if body := r.RequestCtx.PostBody(); len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			_ = r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
			return req, false
		}
	}
	return req, true
}

// ResolveConversation marks a conversation done.
func (a *App) ResolveConversation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return err
	}
	req, ok := decodeConversationAction(r)
	if !ok {
		return nil
	}
	contactID, ok := a.contactForAction(r, orgID, req)
	if !ok {
		return nil
	}

	conv, err := a.Conversations().Resolve(context.Background(), orgID, contactID,
		models.ResolutionAgent, crmevents.UserActor(userID, ""))
	if err != nil {
		if err == conversation.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No active conversation", nil, "")
		}
		a.Log.Error("Failed to resolve conversation", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to resolve conversation", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, false),
	})
}

// SnoozeConversation hides a conversation until a time, or until the customer
// writes again.
func (a *App) SnoozeConversation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return err
	}
	req, ok := decodeConversationAction(r)
	if !ok {
		return nil
	}
	if req.Until == nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "A snooze needs an end time", nil, "")
	}
	if req.Until.Before(time.Now()) {
		// A snooze in the past would be woken on the next tick, which is not
		// what anyone means by snoozing.
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "The snooze time must be in the future", nil, "")
	}
	contactID, ok := a.contactForAction(r, orgID, req)
	if !ok {
		return nil
	}

	conv, err := a.Conversations().Snooze(context.Background(), orgID, contactID,
		req.Until.UTC(), crmevents.UserActor(userID, ""))
	if err != nil {
		if err == conversation.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No active conversation", nil, "")
		}
		a.Log.Error("Failed to snooze conversation", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to snooze conversation", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, false),
	})
}

// AssignConversation sets who is handling a conversation.
func (a *App) AssignConversation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChatAssign, models.ActionWrite)
	if err != nil {
		return err
	}
	req, ok := decodeConversationAction(r)
	if !ok {
		return nil
	}
	contactID, ok := a.contactForAction(r, orgID, req)
	if !ok {
		return nil
	}

	var assignee *uuid.UUID
	if req.AssigneeID != nil && *req.AssigneeID != "" {
		parsed, parseErr := uuid.Parse(*req.AssigneeID)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid assignee id", nil, "")
		}
		var user models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", parsed, orgID).First(&user).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Assignee is not a member of this organization", nil, "")
		}
		assignee = &parsed
	}

	var teamID *uuid.UUID
	if req.TeamID != nil && *req.TeamID != "" {
		parsed, parseErr := uuid.Parse(*req.TeamID)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid team id", nil, "")
		}
		teamID = &parsed
	}

	conv, err := a.Conversations().Assign(context.Background(), orgID, contactID,
		assignee, teamID, crmevents.UserActor(userID, ""))
	if err != nil {
		if err == conversation.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No active conversation", nil, "")
		}
		a.Log.Error("Failed to assign conversation", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to assign conversation", nil, "")
	}

	// Who handed this conversation to whom. Assignment decides who is
	// accountable for a customer, and "updated" does not say that (plan 10,
	// 4.10).
	a.logAudit(orgID, userID, models.ResourceChat, conv.ID, models.AuditActionAssigned, nil,
		map[string]any{
			"contact_id":  contactID.String(),
			"assignee_id": uuidOrEmpty(assignee),
			"team_id":     uuidOrEmpty(teamID),
		})

	// Tell them. Being handed a customer and finding out only when you next
	// happen to open the inbox is how a conversation sits unanswered for an
	// afternoon (plan 00, F5). Assigning to yourself is not news.
	if assignee != nil && *assignee != userID {
		a.notifyAssignee(orgID, *assignee, contactID, conv.ID)
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, false),
	})
}

// notifyAssignee tells somebody a conversation is now theirs.
//
// A failure here never fails the assignment: the conversation has moved, and
// refusing the request would leave the caller believing it had not.
func (a *App) notifyAssignee(orgID, assigneeID, contactID, conversationID uuid.UUID) {
	name := "A conversation"
	var contact models.Contact
	if err := a.DB.Select("profile_name", "phone_number").
		Where("id = ?", contactID).First(&contact).Error; err == nil {
		if contact.ProfileName != "" {
			name = contact.ProfileName
		} else if contact.PhoneNumber != "" {
			name = contact.PhoneNumber
		}
	}

	err := a.Notify().Send(context.Background(), notify.Input{
		OrgID:   orgID,
		UserIDs: []uuid.UUID{assigneeID},
		Type:    models.NotificationConversationAssigned,
		Title:   "Conversation assigned to you",
		Body:    name + " is now yours.",
		Link:    "/chat?contact=" + contactID.String(),
		Entity:  notify.Entity{Type: "conversation", ID: &conversationID},
	})
	if err != nil {
		a.Log.Error("Failed to notify the new assignee", "error", err,
			"assignee_id", assigneeID, "conversation_id", conversationID)
	}
}

// uuidOrEmpty renders an optional id for an audit payload. A blank reads as
// "nobody", which is what unassigning means.
func uuidOrEmpty(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// GetConversation returns a contact's active conversation, if any.
func (a *App) GetConversation(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return err
	}

	contactID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	conv, err := a.Conversations().Active(context.Background(), orgID, contactID)
	if err != nil {
		if err == conversation.ErrNotFound {
			// No active conversation is a normal state, not an error: a
			// contact who has never written has none.
			return r.SendEnvelope(map[string]any{"conversation": nil})
		}
		a.Log.Error("Failed to load conversation", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load conversation", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, a.ShouldMaskPhoneNumbers(orgID)),
	})
}

// MarkConversationPending records that we have replied and are waiting on the
// customer (plan 03).
//
// Open and Pending are the difference between "this needs me" and "this needs
// them". Without it an agent's count includes everything they are waiting on,
// so it never goes down and stops being worth looking at.
func (a *App) MarkConversationPending(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return err
	}
	req, ok := decodeConversationAction(r)
	if !ok {
		return nil
	}
	contactID, ok := a.contactForAction(r, orgID, req)
	if !ok {
		return nil
	}

	conv, err := a.Conversations().SetPending(context.Background(), orgID, contactID,
		crmevents.UserActor(userID, ""))
	if err != nil {
		if err == conversation.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "No active conversation", nil, "")
		}
		a.Log.Error("Failed to mark conversation pending", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update conversation", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, false),
	})
}

// ReopenConversation puts a resolved conversation back in the inbox.
//
// Resolving one by mistake is a single click, and without this the agent's only
// options were to wait for the customer to write again or to start what looks
// like a new issue.
func (a *App) ReopenConversation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return err
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	conversationID, parseErr := uuid.Parse(req.ConversationID)
	if parseErr != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "conversation_id is required", nil, "")
	}

	// Visibility is checked on the contact, the same as every other
	// conversation action: reopening is a way of reaching a conversation, so
	// it must not reach one the agent cannot see.
	var existing models.Conversation
	if err := a.DB.Where("id = ? AND organization_id = ?", conversationID, orgID).
		First(&existing).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Conversation not found", nil, "")
	}
	if !a.canSeeContact(orgID, userID, existing.ContactID) {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Conversation not found", nil, "")
	}

	conv, err := a.Conversations().Reopen(context.Background(), orgID, conversationID,
		crmevents.UserActor(userID, ""))
	if err != nil {
		if err == conversation.ErrNotFound {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Conversation not found", nil, "")
		}
		a.Log.Error("Failed to reopen conversation", "error", err, "conversation_id", conversationID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to reopen conversation", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"conversation": a.toConversationResponse(orgID, *conv, false),
	})
}
