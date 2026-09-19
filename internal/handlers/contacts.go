package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/middleware"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/utils"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// ContactResponse represents a contact with additional fields for the frontend
type ContactResponse struct {
	ID                 uuid.UUID  `json:"id"`
	PhoneNumber        string     `json:"phone_number"`
	Name               string     `json:"name"`
	ProfileName        string     `json:"profile_name"`
	AvatarURL          string     `json:"avatar_url"`
	Status             string     `json:"status"`
	Tags               []string   `json:"tags"`
	Metadata           any        `json:"metadata"`
	LastMessageAt      *time.Time `json:"last_message_at"`
	LastMessagePreview string     `json:"last_message_preview"`
	UnreadCount        int        `json:"unread_count"`
	AssignedUserID     *uuid.UUID `json:"assigned_user_id,omitempty"`
	WhatsAppAccount    string     `json:"whatsapp_account,omitempty"`
	LastInboundAt      *time.Time `json:"last_inbound_at,omitempty"`
	ServiceWindowOpen  bool       `json:"service_window_open"`
	MarketingOptOut    bool       `json:"marketing_opt_out"`
	// Fields carries the contact's custom field values, keyed by field key.
	Fields map[string]any `json:"fields,omitempty"`
	Source string         `json:"source,omitempty"`
	// MergedIntoID is set when this record was merged away. Clients use it to
	// redirect from the secondary's URL to the surviving contact (plan 06);
	// without it a bookmark or an old link renders an empty shell of a record
	// whose history now lives somewhere else.
	MergedIntoID *uuid.UUID `json:"merged_into_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// MessageResponse represents a message for the frontend
type MessageResponse struct {
	ID               uuid.UUID            `json:"id"`
	ContactID        uuid.UUID            `json:"contact_id"`
	Direction        models.Direction     `json:"direction"`
	MessageType      models.MessageType   `json:"message_type"`
	Content          any                  `json:"content"`
	MediaURL         string               `json:"media_url,omitempty"` // base-path-relative path to the media endpoint, not the storage path
	MediaMimeType    string               `json:"media_mime_type,omitempty"`
	MediaFilename    string               `json:"media_filename,omitempty"`
	InteractiveData  models.JSONB         `json:"interactive_data,omitempty"`
	Status           models.MessageStatus `json:"status"`
	WAMID            string               `json:"wamid"`
	Error            string               `json:"error_message"`
	IsReply          bool                 `json:"is_reply"`
	ReplyToMessageID *string              `json:"reply_to_message_id,omitempty"`
	ReplyToMessage   *ReplyPreview        `json:"reply_to_message,omitempty"`
	Reactions        []ReactionInfo       `json:"reactions,omitempty"`
	WhatsAppAccount  string               `json:"whatsapp_account,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// ReplyPreview contains a preview of the replied-to message
type ReplyPreview struct {
	ID          string             `json:"id"`
	Content     any                `json:"content"`
	MessageType models.MessageType `json:"message_type"`
	Direction   models.Direction   `json:"direction"`
}

// ReactionInfo represents a reaction on a message
type ReactionInfo struct {
	Emoji     string `json:"emoji"`
	FromPhone string `json:"from_phone,omitempty"`
	FromUser  string `json:"from_user,omitempty"`
}

// ListContacts returns all contacts for the organization
// Users without contacts:read permission only see contacts assigned to them
func (a *App) ListContacts(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Pagination
	pg := parsePagination(r)
	search := string(r.RequestCtx.QueryArgs().Peek("search"))
	tagsParam := string(r.RequestCtx.QueryArgs().Peek("tags"))

	var contacts []models.Contact
	query := a.ScopeToOrg(a.DB, userID, orgID)

	// Users without contacts:read permission can only see contacts assigned to them
	// or contacts with an active chat transfer to them
	query = a.scopeAssignedContact(query, userID, orgID)

	if search != "" {
		// Limit search string length to prevent abuse
		if len(search) > 1000 {
			search = search[:1000]
		}
		searchPattern := "%" + search + "%"
		// Use ILIKE for case-insensitive search on profile_name
		query = query.Where("phone_number LIKE ? OR profile_name ILIKE ?", searchPattern, searchPattern)
	}

	// Filter by tags (comma-separated, matches contacts that have ANY of the specified tags)
	if tagsParam != "" {
		tagList := strings.Split(tagsParam, ",")
		// Trim whitespace from each tag and build OR conditions
		// Using @> operator which leverages the GIN index on tags
		conditions := make([]string, 0, len(tagList))
		args := make([]any, 0, len(tagList))
		for _, tag := range tagList {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				// Use proper JSONB containment with explicit cast
				conditions = append(conditions, "tags @> ?::jsonb")
				tagJSON, _ := json.Marshal([]string{tag})
				args = append(args, string(tagJSON))
			}
		}
		if len(conditions) > 0 {
			query = query.Where("("+strings.Join(conditions, " OR ")+")", args...)
		}
	}

	// Order by last message time (most recent first)
	query = query.Order("last_message_at DESC NULLS LAST, created_at DESC")

	var total int64
	query.Model(&models.Contact{}).Count(&total)

	if err := query.Offset(pg.Offset).Limit(pg.Limit).Find(&contacts).Error; err != nil {
		a.Log.Error("Failed to list contacts", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list contacts", nil, "")
	}

	// Check if phone masking is enabled
	shouldMask := a.ShouldMaskPhoneNumbers(orgID)

	// Convert to response format
	response := make([]ContactResponse, len(contacts))
	for i, c := range contacts {
		// Count unread messages
		var unreadCount int64
		a.DB.Model(&models.Message{}).
			Where("contact_id = ? AND direction = ? AND status != ?", c.ID, models.DirectionIncoming, models.MessageStatusRead).
			Count(&unreadCount)

		tags := []string{}
		if c.Tags != nil {
			for _, t := range c.Tags {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
		}

		phoneNumber := c.PhoneNumber
		profileName := c.ProfileName
		if shouldMask {
			phoneNumber = utils.MaskPhoneNumber(phoneNumber)
			profileName = utils.MaskIfPhoneNumber(profileName)
		}

		serviceWindowOpen := c.LastInboundAt != nil && time.Since(*c.LastInboundAt) < 24*time.Hour

		response[i] = ContactResponse{
			ID:                 c.ID,
			PhoneNumber:        phoneNumber,
			Name:               profileName,
			ProfileName:        profileName,
			Status:             "active",
			Tags:               tags,
			Metadata:           c.Metadata,
			LastMessageAt:      c.LastMessageAt,
			LastMessagePreview: c.LastMessagePreview,
			UnreadCount:        int(unreadCount),
			AssignedUserID:     c.AssignedUserID,
			WhatsAppAccount:    c.WhatsAppAccount,
			LastInboundAt:      c.LastInboundAt,
			ServiceWindowOpen:  serviceWindowOpen,
			MarketingOptOut:    c.MarketingOptOut,
			CreatedAt:          c.CreatedAt,
			UpdatedAt:          c.UpdatedAt,
		}
	}

	return r.SendEnvelope(listEnvelope("contacts", response, total, pg))
}

// scopeAssignedContact narrows a contact query for users who lack the
// contacts:read permission: they may only access contacts assigned to them
// (assigned_user_id) or contacts with an active agent transfer to them. With
// the permission, the query is returned unchanged. Keeping this in one place
// ensures every contact endpoint enforces the same visibility — assignment
// via an active transfer counts even when assigned_user_id is unset (which it
// is unless the AssignToSameAgent setting is on).
func (a *App) scopeAssignedContact(query *gorm.DB, userID, orgID uuid.UUID) *gorm.DB {
	if a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID) {
		return query
	}
	return query.Where("assigned_user_id = ? OR id IN (?) OR id IN (?)",
		userID,
		a.DB.Model(&models.AgentTransfer{}).
			Select("contact_id").
			Where("agent_id = ? AND organization_id = ? AND status = ?", userID, orgID, models.TransferStatusActive),
		a.queueVisibleContactIDs(userID, orgID),
	)
}

// queueVisibleContactIDs are the contacts an agent can reach through the queue
// (plan 10, S9).
//
// Visibility has to be contact ∪ conversation, not contact alone. The inbox's
// Unassigned view deliberately lists conversations nobody has picked up — that
// is the queue, and any agent on the team is meant to take one. Scoping the
// contact by ownership alone meant an agent could see the row and then get a
// 404 opening it, which reads as the product being broken rather than as a
// permission boundary.
func (a *App) queueVisibleContactIDs(userID, orgID uuid.UUID) *gorm.DB {
	myTeams := a.DB.Model(&models.TeamMember{}).
		Select("team_id").
		Where("user_id = ?", userID)

	return a.DB.Model(&models.Conversation{}).
		Select("contact_id").
		Where("organization_id = ?", orgID).
		Where("assignee_id IS NULL").
		// A conversation the bot still holds is not waiting for a human.
		Where("handling <> ?", models.HandlingBot).
		Where("status <> ?", models.ConversationResolved).
		// Either the general queue, or one of the agent's own team queues.
		Where("team_id IS NULL OR team_id IN (?)", myTeams)
}

// canSeeContact reports whether a viewer may reach one contact at all
// (plan 10, S9).
//
// Endpoints keyed by a contact id — notes, the timeline — were gated only by
// chat:read, so any agent could read the private notes and full history of any
// contact in the organization by putting its id in the URL, including contacts
// the contact list itself refuses to show them. The scope already exists; these
// endpoints simply were not asking it.
func (a *App) canSeeContact(orgID, userID, contactID uuid.UUID) bool {
	query := a.scopeAssignedContact(
		a.DB.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", contactID, orgID),
		userID, orgID)
	var found int64
	if err := query.Count(&found).Error; err != nil {
		a.Log.Error("contact scope check failed", "error", err, "contact_id", contactID)
		return false
	}
	return found > 0
}

// GetContact returns a single contact
// Users without contacts:read permission can only access contacts assigned to them
func (a *App) GetContact(r *fastglue.Request) error {
	orgID, userID, err := a.requireAnyPermission(r, perm(models.ResourceContacts, models.ActionRead), perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return nil
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)

	// Users without contacts:read permission can only access their assigned contacts
	// or contacts with an active chat transfer to them
	query = a.scopeAssignedContact(query, userID, orgID)

	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// A merged contact's URL resolves to the contact that survived (plan 06).
	// Returning the emptied secondary instead would show a record whose
	// messages, tasks and deals have all moved somewhere else — technically
	// the row that was asked for, and useless.
	if contact.MergedIntoID != nil {
		var survivor models.Contact
		if err := a.DB.Where("organization_id = ? AND id = ?", orgID, *contact.MergedIntoID).
			First(&survivor).Error; err == nil {
			response := a.buildContactResponse(&survivor, orgID)
			// The pointer travels with the survivor so the client knows to
			// rewrite the URL it arrived on.
			response.MergedIntoID = &survivor.ID
			return r.SendEnvelope(response)
		}
	}

	response := a.buildContactResponse(&contact, orgID)

	return r.SendEnvelope(response)
}

// GetMessages returns messages for a contact
// Agents can only access messages for their assigned contacts
// Supports cursor-based pagination with before_id for loading older messages
func (a *App) GetMessages(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	hasContactsReadPermission := a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID)

	// Verify contact belongs to org (and to user if no contacts:read permission)
	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Pagination parameters
	limit, _ := strconv.Atoi(string(r.RequestCtx.QueryArgs().Peek("limit")))
	beforeIDStr := string(r.RequestCtx.QueryArgs().Peek("before_id"))

	if limit < 1 || limit > 100 {
		limit = 50
	}

	// Build base query
	msgQuery := a.DB.Where("contact_id = ?", contactID)

	// Filter by WhatsApp account if specified
	accountFilter := string(r.RequestCtx.QueryArgs().Peek("account"))
	if accountFilter != "" {
		msgQuery = msgQuery.Where("whats_app_account = ?", accountFilter)
	}

	// An agent without contacts:read may be limited to the conversation they
	// are actually handling, rather than the customer's whole history.
	//
	// The boundary is the conversation's opened_at, not the last chatbot
	// session's start (plan 10, S5). A session starts and ends many times
	// inside one conversation — every flow the customer walks through is
	// another one — so keying on it cut the thread mid-exchange and hid the
	// customer's own earlier messages from the agent answering them. A
	// conversation is the unit the product means by "this conversation".
	if !hasContactsReadPermission {
		settings, err := a.getChatbotSettingsCached(orgID, "")
		if err == nil && settings.AgentAssignment.CurrentConversationOnly {
			var conv models.Conversation
			if err := a.DB.Where("contact_id = ? AND organization_id = ?", contactID, orgID).
				Order("opened_at DESC").First(&conv).Error; err == nil {
				msgQuery = msgQuery.Where("created_at >= ?", conv.OpenedAt)
			}
		}
	}

	// Count total messages (with session filter if applied)
	var total int64
	msgQuery.Model(&models.Message{}).Count(&total)

	// Deep link: centre the page on one message (plan 02).
	//
	// Clicking a burst on the timeline has to land on the messages it
	// describes. Paging back from the newest message until the right one
	// appears is not a substitute — on a contact with fifty thousand messages
	// the thing you clicked is two hundred requests away.
	if aroundStr := string(r.RequestCtx.QueryArgs().Peek("around")); aroundStr != "" {
		return a.messagesAround(r, msgQuery, aroundStr, limit, total)
	}

	// Cursor-based pagination: load messages before a specific ID
	if beforeIDStr != "" {
		beforeID, err := uuid.Parse(beforeIDStr)
		if err == nil {
			// Get the created_at of the before_id message
			var beforeMsg models.Message
			if err := a.DB.Where("id = ?", beforeID).First(&beforeMsg).Error; err == nil {
				msgQuery = msgQuery.Where("created_at < ?", beforeMsg.CreatedAt)
			}
		}
		// For loading older messages, order DESC and limit, then reverse
		var messages []models.Message
		if err := msgQuery.Preload("ReplyToMessage").Order("created_at DESC").Limit(limit).Find(&messages).Error; err != nil {
			a.Log.Error("Failed to list messages", "error", err)
			return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list messages", nil, "")
		}
		// Reverse to get chronological order
		for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
			messages[i], messages[j] = messages[j], messages[i]
		}

		response := a.buildMessagesResponse(messages)
		return r.SendEnvelope(map[string]any{
			"messages": response,
			"total":    total,
			"has_more": len(messages) == limit,
		})
	}

	// Default: load most recent messages (page 1)
	page, _ := strconv.Atoi(string(r.RequestCtx.QueryArgs().Peek("page")))
	if page < 1 {
		page = 1
	}

	// For chat, we want the most recent messages
	// Calculate offset from the end for pagination
	// Preserve the original limit for the response; adjust a query-specific limit
	// when the remaining messages are fewer than the requested page size.
	responseLimit := limit
	queryLimit := limit
	offset := int(total) - (page * limit)
	if offset < 0 {
		queryLimit = limit + offset // Adjust limit if we're on the last page
		offset = 0
	}

	var messages []models.Message
	if err := msgQuery.Preload("ReplyToMessage").Order("created_at ASC").Offset(offset).Limit(queryLimit).Find(&messages).Error; err != nil {
		a.Log.Error("Failed to list messages", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list messages", nil, "")
	}

	// Mark messages as read
	a.markMessagesAsRead(orgID, contactID, &contact)

	response := a.buildMessagesResponse(messages)
	return r.SendEnvelope(map[string]any{
		"messages": response,
		"total":    total,
		"page":     page,
		"limit":    responseLimit,
		"has_more": offset > 0,
	})
}

// buildMessagesResponse converts messages to response format
func (a *App) buildMessagesResponse(messages []models.Message) []MessageResponse {
	response := make([]MessageResponse, len(messages))
	for i, m := range messages {
		var content any
		if m.MessageType == models.MessageTypeText {
			content = map[string]string{"body": m.Content}
		} else {
			content = map[string]string{"body": m.Content}
		}

		msgResp := MessageResponse{
			ID:              m.ID,
			ContactID:       m.ContactID,
			Direction:       m.Direction,
			MessageType:     m.MessageType,
			Content:         content,
			MediaURL:        messageMediaURL(&m),
			MediaMimeType:   m.MediaMimeType,
			MediaFilename:   m.MediaFilename,
			InteractiveData: m.InteractiveData,
			Status:          m.Status,
			WAMID:           m.WhatsAppMessageID,
			Error:           m.ErrorMessage,
			IsReply:         m.IsReply,
			WhatsAppAccount: m.WhatsAppAccount,
			CreatedAt:       m.CreatedAt,
			UpdatedAt:       m.UpdatedAt,
		}

		if m.IsReply && m.ReplyToMessageID != nil {
			replyToID := m.ReplyToMessageID.String()
			msgResp.ReplyToMessageID = &replyToID
			if m.ReplyToMessage != nil {
				msgResp.ReplyToMessage = &ReplyPreview{
					ID:          m.ReplyToMessage.ID.String(),
					Content:     map[string]string{"body": m.ReplyToMessage.Content},
					MessageType: m.ReplyToMessage.MessageType,
					Direction:   m.ReplyToMessage.Direction,
				}
			}
		}

		if m.Metadata != nil {
			if reactionsRaw, ok := m.Metadata["reactions"]; ok {
				if reactionsArray, ok := reactionsRaw.([]any); ok {
					for _, r := range reactionsArray {
						if rMap, ok := r.(map[string]any); ok {
							emoji, _ := rMap["emoji"].(string)
							fromPhone, _ := rMap["from_phone"].(string)
							fromUser, _ := rMap["from_user"].(string)
							msgResp.Reactions = append(msgResp.Reactions, ReactionInfo{
								Emoji:     emoji,
								FromPhone: fromPhone,
								FromUser:  fromUser,
							})
						}
					}
				}
			}
		}

		response[i] = msgResp
	}
	return response
}

// MarkContactRead marks all incoming messages from a contact as read.
// Called from the frontend when a new message arrives for the chat the
// user is currently viewing, so the sidebar unread badge stays at zero.
func (a *App) MarkContactRead(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return nil
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Peek mode: a supervisor looking at somebody else's queue (plan 10, S5).
	// Opening a chat to see what is going on must not clear the agent's badge
	// or tell the customer their message was read, because neither is true.
	if boolParam(r, "peek") {
		return r.SendEnvelope(map[string]any{"status": "ok", "peek": true})
	}

	a.markConversationRead(orgID, contactID, userID, &contact)
	return r.SendEnvelope(map[string]any{"status": "ok"})
}

// markConversationRead records this user's read position and, only when they
// are the person handling the conversation, sends WhatsApp read receipts.
//
// Read state is per user (plan 10, S5): a colleague opening the chat clears
// their own badge and nobody else's. The blue ticks the customer sees are a
// stronger claim — "somebody has this" — so they are reserved for the
// assignee, or for anyone able to take an unassigned conversation.
func (a *App) markConversationRead(orgID, contactID, userID uuid.UUID, contact *models.Contact) {
	now := time.Now().UTC()

	conv, err := a.Conversations().Active(context.Background(), orgID, contactID)
	if err != nil && !errors.Is(err, conversation.ErrNotFound) {
		a.Log.Error("Failed to load conversation for read state",
			"error", err, "contact_id", contactID)
	}
	if conv != nil {
		if err := a.Conversations().MarkRead(context.Background(), conv.ID, userID, now); err != nil {
			a.Log.Error("Failed to record read position",
				"error", err, "contact_id", contactID, "user_id", userID)
		}
	}

	canWrite := a.HasPermission(userID, models.ResourceChat, models.ActionWrite, orgID)
	if !conversation.MaySendReadReceipts(conv, userID, canWrite) {
		return
	}

	a.markMessagesAsRead(orgID, contactID, contact)
}

// markMessagesAsRead marks messages as read and sends read receipts
func (a *App) markMessagesAsRead(orgID uuid.UUID, contactID uuid.UUID, contact *models.Contact) {
	var unreadMessages []models.Message
	a.DB.Where("contact_id = ? AND direction = ? AND status != ?", contactID, models.DirectionIncoming, models.MessageStatusRead).
		Find(&unreadMessages)

	a.DB.Model(&models.Message{}).
		Where("contact_id = ? AND direction = ?", contactID, models.DirectionIncoming).
		Update("status", models.MessageStatusRead)

	a.DB.Model(contact).Update("is_read", true)

	if len(unreadMessages) > 0 && contact.WhatsAppAccount != "" {
		if account, err := a.resolveWhatsAppAccount(orgID, contact.WhatsAppAccount); err == nil {
			if account.AutoReadReceipt {
				a.wg.Add(1)
				go func() {
					defer a.wg.Done()
					// Use timeout context for external API calls
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()

					waAccount := a.toWhatsAppAccount(account)
					for _, msg := range unreadMessages {
						// Check if context was cancelled
						if ctx.Err() != nil {
							a.Log.Warn("Read receipt sending cancelled", "reason", ctx.Err())
							return
						}
						if msg.WhatsAppMessageID != "" {
							if err := a.WhatsApp.MarkMessageRead(ctx, waAccount, msg.WhatsAppMessageID); err != nil {
								a.Log.Error("Failed to send read receipt", "error", err, "message_id", msg.WhatsAppMessageID)
							}
						}
					}
				}()
			}
		}
	}
}

// SendMessageRequest represents a send message request
type SendMessageRequest struct {
	Type    models.MessageType `json:"type"`
	Content struct {
		Body string `json:"body"`
	} `json:"content"`
	ReplyToMessageID string `json:"reply_to_message_id,omitempty"`
	WhatsAppAccount  string `json:"whatsapp_account,omitempty"`

	// Interactive message fields (for type="interactive")
	Interactive *InteractiveContent `json:"interactive,omitempty"`
}

// InteractiveContent holds interactive message data
type InteractiveContent struct {
	Type       string          `json:"type"`                  // "button", "list", "cta_url", "voice_call", "flow"
	Body       string          `json:"body"`                  // Body text
	Buttons    []ButtonContent `json:"buttons,omitempty"`     // For button type
	ButtonText string          `json:"button_text,omitempty"` // CTA label for cta_url and flow
	URL        string          `json:"url,omitempty"`         // For cta_url type
	// voice_call only: button face label and clickable TTL.
	// The payload (round-trip opaque string Meta echoes back on the incoming-
	// call webhook) is set server-side from the auth context — never from the
	// request body — to prevent agent-id spoofing.
	DisplayText string `json:"display_text,omitempty"`
	TTLMinutes  int    `json:"ttl_minutes,omitempty"`
	// flow only: the Meta flow to launch, an optional first screen, and an
	// optional header. Body holds the message text, ButtonText the CTA label.
	FlowID      string `json:"flow_id,omitempty"`
	FirstScreen string `json:"first_screen,omitempty"`
	Header      string `json:"header,omitempty"`
}

// ButtonContent represents a button in interactive messages
type ButtonContent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SendMessage sends a message to a contact
// Agents can only send messages to their assigned contacts
func (a *App) SendMessage(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return nil
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	// Parse request body
	var req SendMessageRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	// Get contact (users without full read permission can only message their assigned contacts)
	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Get WhatsApp account - prefer request-specified account over contact default
	accountName := contact.WhatsAppAccount
	if req.WhatsAppAccount != "" {
		accountName = req.WhatsAppAccount
	}
	account, err := a.resolveWhatsAppAccount(orgID, accountName)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Failed to resolve WhatsApp account", nil, "")
	}

	// Handle reply context
	var replyToMessage *models.Message
	if req.ReplyToMessageID != "" {
		replyToID, err := uuid.Parse(req.ReplyToMessageID)
		if err == nil {
			var replyTo models.Message
			if err := a.DB.Where("id = ? AND contact_id = ?", replyToID, contactID).First(&replyTo).Error; err == nil {
				replyToMessage = &replyTo
			}
		}
	}

	// Build request and send using unified sender
	msgReq := OutgoingMessageRequest{
		Account:        account,
		Contact:        &contact,
		Type:           req.Type,
		Content:        req.Content.Body,
		ReplyToMessage: replyToMessage,
	}

	// Handle interactive messages
	if req.Type == models.MessageTypeInteractive && req.Interactive != nil {
		msgReq.InteractiveType = req.Interactive.Type
		msgReq.BodyText = req.Interactive.Body
		msgReq.ButtonText = req.Interactive.ButtonText
		msgReq.URL = req.Interactive.URL

		// Convert buttons
		if len(req.Interactive.Buttons) > 0 {
			msgReq.Buttons = make([]whatsapp.Button, len(req.Interactive.Buttons))
			for i, btn := range req.Interactive.Buttons {
				msgReq.Buttons[i] = whatsapp.Button{
					ID:    btn.ID,
					Title: btn.Title,
				}
			}
		}

		if req.Interactive.Type == "flow" {
			if req.Interactive.FlowID == "" {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "flow_id is required to send a flow", nil, "")
			}
			// Ensure the flow belongs to this org so an agent can't send another
			// org's flow by supplying its Meta id.
			var waFlow models.WhatsAppFlow
			if err := a.DB.Where("meta_flow_id = ? AND organization_id = ?", req.Interactive.FlowID, orgID).First(&waFlow).Error; err != nil {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Flow not found for this organization", nil, "")
			}
			cta := req.Interactive.ButtonText
			if cta == "" {
				cta = "Open"
			}
			body := req.Interactive.Body
			if body == "" {
				body = req.Content.Body
			}
			msgReq.Type = models.MessageTypeFlow
			msgReq.FlowID = req.Interactive.FlowID
			msgReq.FlowCTA = cta
			msgReq.FlowHeader = req.Interactive.Header
			msgReq.FlowFirstScreen = req.Interactive.FirstScreen
			msgReq.BodyText = body
			msgReq.FlowToken = fmt.Sprintf("agent_%s_%d", contact.ID, time.Now().UnixNano())
		}

		if req.Interactive.Type == "voice_call" {
			if !account.BusinessCallingEnabled {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
					"This WhatsApp account is not enrolled in the Business Calling API. Enable it under Settings → Accounts before sending Call buttons.",
					nil, "")
			}
			msgReq.DisplayText = req.Interactive.DisplayText
			msgReq.TTLMinutes = req.Interactive.TTLMinutes
			// Stamp the payload server-side so the incoming-call webhook can
			// sticky-route the resulting call back to the agent who sent it.
			// Never trust a client-supplied payload — would let any agent
			// impersonate any other.
			msgReq.VoiceCallPayload = "agent:" + userID.String()
			// Pre-register the sticky-routing intent in Redis so that the
			// resulting incoming-call webhook can resolve the originating
			// agent in O(1) (Meta does not currently echo the payload).
			// TTL matches the button's clickable lifetime.
			a.MarkPendingStickyCall(context.Background(), orgID, contact.PhoneNumber, userID, req.Interactive.TTLMinutes)
		}
	}

	opts := DefaultSendOptions()
	opts.SentByUserID = &userID

	ctx := context.Background()
	message, err := a.SendOutgoingMessage(ctx, msgReq, opts)
	if err != nil {
		a.Log.Error("Failed to send message", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to send message", nil, "")
	}

	// Build response
	response := MessageResponse{
		ID:              message.ID,
		ContactID:       message.ContactID,
		Direction:       message.Direction,
		MessageType:     message.MessageType,
		Content:         map[string]string{"body": message.Content},
		InteractiveData: message.InteractiveData,
		Status:          message.Status,
		IsReply:         message.IsReply,
		WhatsAppAccount: message.WhatsAppAccount,
		CreatedAt:       message.CreatedAt,
		UpdatedAt:       message.UpdatedAt,
	}

	// Add reply context to response
	if message.IsReply && message.ReplyToMessageID != nil && replyToMessage != nil {
		replyToID := message.ReplyToMessageID.String()
		response.ReplyToMessageID = &replyToID
		response.ReplyToMessage = &ReplyPreview{
			ID:          replyToMessage.ID.String(),
			Content:     map[string]string{"body": replyToMessage.Content},
			MessageType: replyToMessage.MessageType,
			Direction:   replyToMessage.Direction,
		}
	}

	return r.SendEnvelope(response)
}

// resolveWhatsAppAccount gets the WhatsApp account for sending messages
func (a *App) resolveWhatsAppAccount(orgID uuid.UUID, accountName string) (*models.WhatsAppAccount, error) {
	var account models.WhatsAppAccount

	if accountName != "" {
		if err := a.DB.Where("name = ? AND organization_id = ?", accountName, orgID).First(&account).Error; err != nil {
			return nil, fmt.Errorf("WhatsApp account not found")
		}
		a.decryptAccountSecrets(&account)
		return &account, nil
	}

	// Get default outgoing account
	if err := a.DB.Where("organization_id = ? AND is_default_outgoing = ?", orgID, true).First(&account).Error; err != nil {
		// Fall back to any account
		if err := a.DB.Where("organization_id = ?", orgID).First(&account).Error; err != nil {
			return nil, fmt.Errorf("no WhatsApp account configured")
		}
	}
	a.decryptAccountSecrets(&account)
	return &account, nil
}

// resolveWhatsAppAccountByID fetches a WhatsApp account by UUID and org, decrypts secrets.
func (a *App) resolveWhatsAppAccountByID(r *fastglue.Request, id, orgID uuid.UUID) (*models.WhatsAppAccount, error) {
	account, err := findByIDAndOrg[models.WhatsAppAccount](a.DB, r, id, orgID, "Account")
	if err != nil {
		return nil, err
	}
	a.decryptAccountSecrets(account)
	return account, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// SendMediaMessage sends a media message (image, document, video, audio) to a contact
func (a *App) SendMediaMessage(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return nil
	}

	// Parse multipart form
	form, err := r.RequestCtx.MultipartForm()
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid multipart form", nil, "")
	}

	// Get contact ID from form
	contactIDValues := form.Value["contact_id"]
	if len(contactIDValues) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "contact_id is required", nil, "")
	}
	contactID, err := uuid.Parse(contactIDValues[0])
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact ID", nil, "")
	}

	// Get media type (image, document, video, audio)
	mediaType := "image"
	if typeValues := form.Value["type"]; len(typeValues) > 0 {
		mediaType = typeValues[0]
	}

	// Get caption (optional)
	caption := ""
	if captionValues := form.Value["caption"]; len(captionValues) > 0 {
		caption = captionValues[0]
	}

	// Get WhatsApp account override (optional)
	formWhatsAppAccount := ""
	if accountValues := form.Value["whatsapp_account"]; len(accountValues) > 0 {
		formWhatsAppAccount = accountValues[0]
	}

	// Get uploaded file
	files := form.File["file"]
	if len(files) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "file is required", nil, "")
	}
	fileHeader := files[0]

	// Open the file
	file, err := fileHeader.Open()
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Failed to read file", nil, "")
	}
	defer func() { _ = file.Close() }()

	// Read file data
	fileData, err := io.ReadAll(file)
	if err != nil {
		a.Log.Error("Failed to read file data", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to read file data", nil, "")
	}

	// Get MIME type
	mimeType := fileHeader.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Get contact (users without full read permission can only message their assigned contacts)
	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Get WhatsApp account - prefer form-specified account over contact default
	mediaAccountName := contact.WhatsAppAccount
	if formWhatsAppAccount != "" {
		mediaAccountName = formWhatsAppAccount
	}
	account, err := a.resolveWhatsAppAccount(orgID, mediaAccountName)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	// Save file locally first
	localPath, err := a.saveMediaLocally(fileData, mimeType, fileHeader.Filename)
	if err != nil {
		a.Log.Error("Failed to save media locally", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to save media", nil, "")
	}

	// Build and send via unified message sender
	msgReq := OutgoingMessageRequest{
		Account:       account,
		Contact:       &contact,
		Type:          models.MessageType(mediaType),
		MediaData:     fileData,
		MediaURL:      localPath,
		MediaMimeType: mimeType,
		MediaFilename: fileHeader.Filename,
		Caption:       caption,
	}

	opts := DefaultSendOptions()
	opts.SentByUserID = &userID

	ctx := context.Background()
	message, err := a.SendOutgoingMessage(ctx, msgReq, opts)
	if err != nil {
		a.Log.Error("Failed to send message", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to send message", nil, "")
	}

	response := MessageResponse{
		ID:              message.ID,
		ContactID:       message.ContactID,
		Direction:       message.Direction,
		MessageType:     message.MessageType,
		Content:         map[string]string{"body": message.Content},
		MediaURL:        messageMediaURL(message),
		MediaMimeType:   message.MediaMimeType,
		MediaFilename:   message.MediaFilename,
		Status:          message.Status,
		WhatsAppAccount: message.WhatsAppAccount,
		CreatedAt:       message.CreatedAt,
		UpdatedAt:       message.UpdatedAt,
	}

	return r.SendEnvelope(response)
}

// saveMediaLocally saves media data to local storage and returns the relative path
func (a *App) saveMediaLocally(data []byte, mimeType, filename string) (string, error) {
	// Determine subdirectory based on MIME type
	var subdir string
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		subdir = "images"
	case strings.HasPrefix(mimeType, "video/"):
		subdir = "videos"
	case strings.HasPrefix(mimeType, "audio/"):
		subdir = "audio"
	default:
		subdir = "documents"
	}

	// Ensure directory exists
	if err := a.ensureMediaDir(subdir); err != nil {
		return "", fmt.Errorf("failed to create media directory: %w", err)
	}

	// Get extension from MIME type or filename
	ext := getExtensionFromMimeType(mimeType)
	if ext == "" {
		// Try to get from filename
		if dotIdx := strings.LastIndex(filename, "."); dotIdx >= 0 {
			ext = filename[dotIdx:]
		} else {
			ext = ".bin"
		}
	}

	// Generate unique filename
	newFilename := uuid.New().String() + ext
	filePath := filepath.Join(a.getMediaStoragePath(), subdir, newFilename)

	// Save file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save media file: %w", err)
	}

	// Return relative path
	relativePath := filepath.Join(subdir, newFilename)
	a.Log.Info("Media saved locally", "path", relativePath, "size", len(data))

	return relativePath, nil
}

// SendReactionRequest represents a request to send a reaction
type SendReactionRequest struct {
	Emoji string `json:"emoji"` // Empty string to remove reaction
}

// SendReaction sends a reaction to a message
func (a *App) SendReaction(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceChat, models.ActionWrite)
	if err != nil {
		return nil
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	messageIDStr := r.RequestCtx.UserValue("message_id").(string)

	messageID, err := uuid.Parse(messageIDStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid message ID", nil, "")
	}

	// Parse request body
	var req SendReactionRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	// Get contact (users without full read permission can only react to messages in their assigned contacts)
	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	// Get message
	var message models.Message
	if err := a.DB.Where("id = ? AND contact_id = ?", messageID, contactID).First(&message).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Message not found", nil, "")
	}

	// Get WhatsApp account from the message being reacted to (not from contact, which may be stale)
	reactionAccountName := message.WhatsAppAccount
	if reactionAccountName == "" {
		reactionAccountName = contact.WhatsAppAccount
	}
	account, err := a.resolveWhatsAppAccount(orgID, reactionAccountName)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	// Parse existing reactions from Metadata
	var metadata map[string]any
	if message.Metadata != nil {
		metadata = message.Metadata
	} else {
		metadata = make(map[string]any)
	}

	// Get or initialize reactions array
	type Reaction struct {
		Emoji     string `json:"emoji"`
		FromPhone string `json:"from_phone,omitempty"`
		FromUser  string `json:"from_user,omitempty"`
	}
	var reactions []Reaction
	if reactionsRaw, ok := metadata["reactions"]; ok {
		if reactionsArray, ok := reactionsRaw.([]any); ok {
			for _, r := range reactionsArray {
				if rMap, ok := r.(map[string]any); ok {
					emoji, _ := rMap["emoji"].(string)
					fromPhone, _ := rMap["from_phone"].(string)
					fromUser, _ := rMap["from_user"].(string)
					reactions = append(reactions, Reaction{
						Emoji:     emoji,
						FromPhone: fromPhone,
						FromUser:  fromUser,
					})
				}
			}
		}
	}

	// Remove existing reaction from this user (each user can only have one reaction)
	userIDStr := userID.String()
	var newReactions []Reaction
	for _, r := range reactions {
		if r.FromUser != userIDStr {
			newReactions = append(newReactions, r)
		}
	}

	// Add new reaction if emoji is not empty
	if req.Emoji != "" {
		newReactions = append(newReactions, Reaction{
			Emoji:    req.Emoji,
			FromUser: userIDStr,
		})
	}

	// Update metadata
	metadata["reactions"] = newReactions
	if err := a.DB.Model(&message).Update("metadata", metadata).Error; err != nil {
		a.Log.Error("Failed to update message reactions", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update reaction", nil, "")
	}

	// Send reaction to WhatsApp API
	go a.sendWhatsAppReaction(account, &contact, &message, req.Emoji)

	// Broadcast via WebSocket
	a.broadcastReactionUpdate(orgID, message.ID, contact.ID, newReactions)

	return r.SendEnvelope(map[string]any{
		"message_id": message.ID.String(),
		"reactions":  newReactions,
	})
}

// sendWhatsAppReaction sends a reaction to WhatsApp
func (a *App) sendWhatsAppReaction(account *models.WhatsAppAccount, contact *models.Contact, message *models.Message, emoji string) {
	if message.WhatsAppMessageID == "" {
		a.Log.Warn("Cannot send reaction - message has no WhatsApp ID", "message_id", message.ID)
		return
	}

	url := fmt.Sprintf("%s/%s/%s/messages", a.Config.WhatsApp.BaseURL, account.APIVersion, account.PhoneID)

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                contact.PhoneNumber,
		"type":              "reaction",
		"reaction": map[string]any{
			"message_id": message.WhatsAppMessageID,
			"emoji":      emoji, // Empty string removes the reaction
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		a.Log.Error("Failed to marshal reaction payload", "error", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		a.Log.Error("Failed to create reaction request", "error", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+account.AccessToken)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		a.Log.Error("Failed to send reaction", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		a.Log.Error("WhatsApp API reaction error", "status", resp.StatusCode, "body", string(body))
		return
	}

	a.Log.Info("Reaction sent successfully", "message_id", message.WhatsAppMessageID, "emoji", emoji)
}

// AssignContactRequest represents the request to assign a contact to a user
type AssignContactRequest struct {
	UserID *uuid.UUID `json:"user_id"` // nil to unassign
}

// AssignContact assigns a contact to a user (agent)
// Only users with write permission can assign contacts
func (a *App) AssignContact(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Only users with write permission can assign contacts
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "You do not have permission to assign contacts", nil, "")
	}

	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	var req AssignContactRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	// Get contact
	contact, err := findByIDAndOrg[models.Contact](a.DB, r, contactID, orgID, "Contact")
	if err != nil {
		return nil
	}

	// If assigning to a user, verify they exist in the same org
	if req.UserID != nil {
		var user models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", req.UserID, orgID).First(&user).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "User not found", nil, "")
		}
	}

	// Update contact assignment
	if err := a.DB.Model(contact).Update("assigned_user_id", req.UserID).Error; err != nil {
		a.Log.Error("Failed to assign contact", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to assign contact", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"message":          "Contact assigned successfully",
		"assigned_user_id": req.UserID,
	})
}

// ContactSessionDataResponse represents the session data for a contact's info panel
type ContactSessionDataResponse struct {
	SessionID   *uuid.UUID     `json:"session_id,omitempty"`
	FlowID      *uuid.UUID     `json:"flow_id,omitempty"`
	FlowName    string         `json:"flow_name,omitempty"`
	SessionData map[string]any `json:"session_data"`
	PanelConfig map[string]any `json:"panel_config"`
}

// GetContactSessionData returns session data and panel configuration for a contact
// Used by the contact info panel in the chat view
func (a *App) GetContactSessionData(r *fastglue.Request) error {
	orgID, userID, err := a.requireAnyPermission(r, perm(models.ResourceContacts, models.ActionRead), perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return nil
	}
	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	// Verify contact belongs to org (users without full read permission can only access assigned contacts)
	var contact models.Contact
	query := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID)
	query = a.scopeAssignedContact(query, userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	response := ContactSessionDataResponse{
		SessionData: make(map[string]any),
		PanelConfig: map[string]any{"sections": []any{}},
	}

	// Get the most recent completed or active session for this contact
	var session models.ChatbotSession
	err = a.DB.Where("contact_id = ? AND organization_id = ?", contactID, orgID).
		Where("status IN ?", []models.SessionStatus{models.SessionStatusActive, models.SessionStatusCompleted}).
		Order("created_at DESC").
		First(&session).Error

	if err == nil {
		response.SessionID = &session.ID
		response.FlowID = session.CurrentFlowID

		// Get the flow to retrieve panel config
		// First try current_flow_id, then fall back to _flow_id in session_data
		var flowID *uuid.UUID
		if session.CurrentFlowID != nil {
			flowID = session.CurrentFlowID
		} else if flowIDStr, ok := session.SessionData["_flow_id"].(string); ok {
			if parsedID, err := uuid.Parse(flowIDStr); err == nil {
				flowID = &parsedID
			}
		}

		if flowID != nil {
			// Use cached flow to avoid DB query
			flow, err := a.getChatbotFlowByIDCached(orgID, *flowID)
			if err == nil && flow != nil {
				response.FlowName = flow.Name
				response.FlowID = flowID

				// Use panel config directly from flow (it's already JSONB/map)
				if len(flow.PanelConfig) > 0 {
					response.PanelConfig = flow.PanelConfig

					// Only include session data for configured fields (reduce payload)
					if session.SessionData != nil {
						configuredKeys := make(map[string]bool)
						if sections, ok := flow.PanelConfig["sections"].([]any); ok {
							for _, sec := range sections {
								if section, ok := sec.(map[string]any); ok {
									if fields, ok := section["fields"].([]any); ok {
										for _, f := range fields {
											if field, ok := f.(map[string]any); ok {
												if key, ok := field["key"].(string); ok {
													configuredKeys[key] = true
												}
											}
										}
									}
								}
							}
						}
						// Copy only configured fields to response
						for key := range configuredKeys {
							if val, exists := session.SessionData[key]; exists {
								response.SessionData[key] = val
							}
						}
					}
				}
			}
		}
	}

	return r.SendEnvelope(response)
}

// UpdateContactTagsRequest represents the request body for updating contact tags
type UpdateContactTagsRequest struct {
	Tags []string `json:"tags"`
}

// UpdateContactTags updates the tags on a contact
func (a *App) UpdateContactTags(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Check permission - need contacts:write to update tags on contacts
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "You do not have permission to update contact tags", nil, "")
	}

	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	var req UpdateContactTagsRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	// Get contact
	contact, err := findByIDAndOrg[models.Contact](a.DB, r, contactID, orgID, "Contact")
	if err != nil {
		return nil
	}

	// What is changing, before it changes. Tagging one contact used to emit
	// nothing while tagging a hundred in bulk emitted an event each, so the
	// same change was on the timeline, the webhook and the automation trigger
	// only when it was done the less common way (plan 10, 4.1).
	added, removed := tagDifference(contact.Tags, req.Tags)

	// Convert tags to JSONBArray
	tagsArray := make(models.JSONBArray, len(req.Tags))
	for i, tag := range req.Tags {
		tagsArray[i] = tag
	}

	// Update contact tags
	if err := a.DB.Model(contact).Update("tags", tagsArray).Error; err != nil {
		a.Log.Error("Failed to update contact tags", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update contact tags", nil, "")
	}

	for _, tag := range added {
		a.PublishEvent(crmevents.New(orgID, "contact.tag_added",
			crmevents.UserActor(userID, ""), map[string]any{"tag": tag}).ForContact(contactID))
	}
	for _, tag := range removed {
		a.PublishEvent(crmevents.New(orgID, "contact.tag_removed",
			crmevents.UserActor(userID, ""), map[string]any{"tag": tag}).ForContact(contactID))
	}

	// Reload contact to get updated tags
	if err := a.DB.First(contact, contactID).Error; err != nil {
		a.Log.Error("Failed to reload contact", "error", err)
	}

	// Build response with tag details
	tags := []string{}
	if contact.Tags != nil {
		for _, t := range contact.Tags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	return r.SendEnvelope(map[string]any{
		"message": "Contact tags updated",
		"tags":    tags,
	})
}

// tagDifference reports which tags a change adds and which it removes.
//
// The stored list is []any because it comes back from JSONB; the requested one
// is []string. Comparing them as sets means reordering the same tags is not
// reported as a change, which it is not.
func tagDifference(before models.JSONBArray, after []string) (added, removed []string) {
	had := make(map[string]bool, len(before))
	for _, raw := range before {
		if tag, ok := raw.(string); ok {
			had[tag] = true
		}
	}
	wants := make(map[string]bool, len(after))
	for _, tag := range after {
		wants[tag] = true
		if !had[tag] {
			added = append(added, tag)
		}
	}
	for tag := range had {
		if !wants[tag] {
			removed = append(removed, tag)
		}
	}
	sort.Strings(removed)
	return added, removed
}

// CreateContactRequest represents the request body for creating a contact
type CreateContactRequest struct {
	PhoneNumber     string         `json:"phone_number"`
	ProfileName     string         `json:"profile_name"`
	WhatsAppAccount string         `json:"whatsapp_account"`
	Tags            []string       `json:"tags"`
	Metadata        map[string]any `json:"metadata"`

	// Fields carries custom field values keyed by field key (plan 01).
	Fields map[string]any `json:"fields"`
}

// CreateContact creates a contact by hand, from the UI or the API.
//
// It goes through the contact lifecycle service rather than inserting a row
// (plan 10, S2). Creating one here used to mean the record started with no
// source and no lifecycle stage, published no contact.created event — so no
// timeline entry, no webhook and no automation trigger — and restored a
// contact somebody had deliberately deleted, which is the one thing the
// lifecycle service exists to refuse.
func (a *App) CreateContact(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Check permission
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "You do not have permission to create contacts", nil, "")
	}

	var req CreateContactRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	if req.PhoneNumber == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "phone_number is required", nil, "")
	}

	fieldSvc := customfields.New(a.DB)
	defs, err := fieldSvc.DefinitionsByKey(context.Background(), orgID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("Failed to load contact fields", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create contact", nil, "")
	}

	fields := customfields.ApplyDefaults(defs, req.Fields)
	if missing := customfields.MissingRequired(defs, fields); len(missing) > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"These fields are required: "+strings.Join(missing, ", "),
			map[string]any{"missing_fields": missing}, "")
	}

	// A contact created by hand and one created by an integration are worth
	// telling apart: reports split acquisition by source, and "who typed this
	// in" is the first question about a record nobody recognises.
	source := contacts.SourceManual
	if method, ok := r.RequestCtx.UserValue(middleware.ContextKeyAuthMethod).(string); ok &&
		method == middleware.AuthMethodAPIKey {
		source = contacts.SourceAPI
	}

	contact, outcome, err := a.Contacts().Resolve(context.Background(), orgID,
		contacts.Identity{Phone: req.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing:    true,
			AllowRestore:       false,
			UpdateName:         false,
			ProfileName:        req.ProfileName,
			Source:             source,
			DefaultCountryCode: a.orgDefaultCountryCode(orgID),
			Actor:              crmevents.UserActor(userID, ""),
		})
	if err != nil {
		a.Log.Error("Failed to create contact", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create contact", nil, "")
	}
	if outcome != contacts.OutcomeCreated {
		// The number already belongs to somebody, possibly under a different
		// formatting or as the survivor of a merge. Saying so beats creating
		// a second record for one person.
		return r.SendErrorEnvelope(fasthttp.StatusConflict,
			"Contact with this phone number already exists",
			map[string]any{"contact_id": contact.ID}, "")
	}

	updates := map[string]any{}
	if req.WhatsAppAccount != "" {
		// The column is whats_app_account: GORM derived it from the Go field
		// name, and a map update names columns rather than fields.
		updates["whats_app_account"] = req.WhatsAppAccount
	}
	if req.Tags != nil {
		tagsArray := make(models.JSONBArray, len(req.Tags))
		for i, tag := range req.Tags {
			tagsArray[i] = tag
		}
		updates["tags"] = tagsArray
	}
	if req.Metadata != nil {
		updates["metadata"] = models.JSONB(req.Metadata)
	}

	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&models.Contact{}).Where("id = ?", contact.ID).
				Updates(updates).Error; err != nil {
				return err
			}
		}
		if len(fields) > 0 {
			if _, err := customfields.New(tx).SetValues(tx, orgID, contact.ID,
				models.FieldEntityContact, fields, &userID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		a.Log.Error("Failed to apply contact details", "error", err, "contact_id", contact.ID)
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	if err := a.DB.First(contact, contact.ID).Error; err != nil {
		a.Log.Error("Failed to reload contact", "error", err, "contact_id", contact.ID)
	}

	a.logAudit(orgID, userID,
		models.ResourceContacts, contact.ID, models.AuditActionCreated, nil, contact)

	return r.SendEnvelope(a.buildContactResponse(contact, orgID))
}

// UpdateContactRequest represents the request body for updating a contact.
// AssignedUserID uses *string so we can distinguish "not sent" (nil) from
// "sent as null" (pointer to empty string) to allow clearing the field.
type UpdateContactRequest struct {
	ProfileName        *string         `json:"profile_name"`
	WhatsAppAccount    *string         `json:"whatsapp_account"`
	Tags               []string        `json:"tags"`
	Metadata           *map[string]any `json:"metadata"`
	AssignedUserID     *uuid.UUID      `json:"assigned_user_id"`
	ClearAssignedAgent *bool           `json:"clear_assigned_agent"`

	// Fields carries custom field values keyed by field key (plan 01).
	Fields map[string]any `json:"fields"`
}

// UpdateContact updates an existing contact
func (a *App) UpdateContact(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Check permission
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionWrite, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "You do not have permission to update contacts", nil, "")
	}

	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	var req UpdateContactRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	// Get contact
	contact, err := findByIDAndOrg[models.Contact](a.DB, r, contactID, orgID, "Contact")
	if err != nil {
		return nil
	}
	oldContact := *contact

	// Build updates map
	updates := map[string]any{}

	if req.ProfileName != nil {
		updates["profile_name"] = *req.ProfileName
	}
	if req.WhatsAppAccount != nil {
		updates["whats_app_account"] = *req.WhatsAppAccount
	}
	if req.Tags != nil {
		tagsArray := make(models.JSONBArray, len(req.Tags))
		for i, tag := range req.Tags {
			tagsArray[i] = tag
		}
		updates["tags"] = tagsArray
	}
	if req.Metadata != nil {
		updates["metadata"] = models.JSONB(*req.Metadata)
	}
	if req.ClearAssignedAgent != nil && *req.ClearAssignedAgent {
		updates["assigned_user_id"] = nil
	} else if req.AssignedUserID != nil {
		var user models.User
		if err := a.DB.Where("id = ? AND organization_id = ?", req.AssignedUserID, orgID).First(&user).Error; err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Assigned user not found", nil, "")
		}
		updates["assigned_user_id"] = req.AssignedUserID
	}

	if len(updates) == 0 && len(req.Fields) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "No fields to update", nil, "")
	}

	var fieldsBefore map[string]any
	if len(req.Fields) > 0 {
		fieldsBefore = a.fieldValuesBefore(orgID, contact.ID)
	}

	// Column updates and custom field values are written together, so a
	// rejected field value cannot leave half an edit applied.
	var changedFields []string
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(contact).Updates(updates).Error; err != nil {
				return err
			}
		}
		if len(req.Fields) > 0 {
			changed, err := customfields.New(tx).SetValues(tx, orgID, contact.ID,
				models.FieldEntityContact, req.Fields, &userID)
			if err != nil {
				return err
			}
			changedFields = changed
		}
		return nil
	}); err != nil {
		// A rejected value is the caller's mistake and names the field, so it
		// is worth returning rather than flattening into a 500.
		var invalid *customfields.ValidationError
		if errors.As(err, &invalid) {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, invalid.Error(), nil, "")
		}
		a.Log.Error("Failed to update contact", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update contact", nil, "")
	}

	// Reload contact
	a.DB.First(contact, contactID)

	a.logAudit(orgID, userID,
		models.ResourceContacts, contact.ID, models.AuditActionUpdated, &oldContact, contact)

	// One activity entry per field that actually changed, so the timeline says
	// what was edited rather than just "contact updated".
	a.publishFieldChanges(orgID, crmevents.UserActor(userID, ""), contact.ID, fieldsBefore, changedFields)

	// One contact.updated for subscribers, carrying the record and its typed
	// fields (plan 01). Integrations want "here is the contact now", not a
	// delivery per field — during an import that would be thousands.
	a.publishContactUpdated(orgID, userID, contact, changedFields)

	return r.SendEnvelope(a.buildContactResponse(contact, orgID))
}

// DeleteContact soft-deletes a contact
func (a *App) DeleteContact(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	// Check permission
	if !a.HasPermission(userID, models.ResourceContacts, models.ActionDelete, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "You do not have permission to delete contacts", nil, "")
	}

	contactID, err := parsePathUUID(r, "id", "contact")
	if err != nil {
		return nil
	}

	// Get contact
	contact, err := findByIDAndOrg[models.Contact](a.DB, r, contactID, orgID, "Contact")
	if err != nil {
		return nil
	}

	// Through the lifecycle service (plan 10, S2), never a bare soft delete. It
	// records that a person did this, which is what stops the next message from
	// that number quietly restoring the record, and it ends the chatbot
	// session, the queued transfer and the open conversation the contact left
	// behind.
	if err := a.Contacts().Delete(context.Background(), orgID, contactID,
		contacts.ReasonUser, crmevents.UserActor(userID, "")); err != nil {
		a.Log.Error("Failed to delete contact", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete contact", nil, "")
	}

	a.logAudit(orgID, userID,
		models.ResourceContacts, contactID, models.AuditActionDeleted, contact, nil)

	return r.SendEnvelope(map[string]any{
		"message": "Contact deleted successfully",
	})
}

// buildContactResponse creates a ContactResponse from a Contact model
func (a *App) buildContactResponse(contact *models.Contact, orgID uuid.UUID) ContactResponse {
	// Count unread messages
	var unreadCount int64
	a.DB.Model(&models.Message{}).
		Where("contact_id = ? AND direction = ? AND status != ?", contact.ID, models.DirectionIncoming, models.MessageStatusRead).
		Count(&unreadCount)

	tags := []string{}
	if contact.Tags != nil {
		for _, t := range contact.Tags {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	phoneNumber := contact.PhoneNumber
	profileName := contact.ProfileName
	shouldMask := a.ShouldMaskPhoneNumbers(orgID)
	if shouldMask {
		phoneNumber = utils.MaskPhoneNumber(phoneNumber)
		profileName = utils.MaskIfPhoneNumber(profileName)
	}

	// 24-hour service window: open if customer messaged within the last 24 hours.
	serviceWindowOpen := contact.LastInboundAt != nil && time.Since(*contact.LastInboundAt) < 24*time.Hour

	// Custom field values. A failure here degrades the record rather than
	// failing the request: the contact is still worth returning without them.
	fields, err := customfields.New(a.DB).Values(context.Background(), orgID, contact.ID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("Failed to load contact fields", "error", err, "contact_id", contact.ID)
		fields = nil
	}

	return ContactResponse{
		ID:                 contact.ID,
		PhoneNumber:        phoneNumber,
		Name:               profileName,
		ProfileName:        profileName,
		Status:             "active",
		Tags:               tags,
		Metadata:           contact.Metadata,
		LastMessageAt:      contact.LastMessageAt,
		LastMessagePreview: contact.LastMessagePreview,
		UnreadCount:        int(unreadCount),
		AssignedUserID:     contact.AssignedUserID,
		WhatsAppAccount:    contact.WhatsAppAccount,
		LastInboundAt:      contact.LastInboundAt,
		ServiceWindowOpen:  serviceWindowOpen,
		MarketingOptOut:    contact.MarketingOptOut,
		Fields:             fields,
		Source:             contact.Source,
		MergedIntoID:       contact.MergedIntoID,
		CreatedAt:          contact.CreatedAt,
		UpdatedAt:          contact.UpdatedAt,
	}
}

// publishContactUpdated emits the subscriber-facing "this contact changed"
// event (plan 01).
//
// The payload carries the typed custom fields as well as the built-in columns,
// because an integration that has to call back for every field it cares about
// is not being told anything useful by the webhook.
func (a *App) publishContactUpdated(orgID, userID uuid.UUID, contact *models.Contact, changed []string) {
	fields, err := customfields.New(a.DB).Values(
		context.Background(), orgID, contact.ID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("load contact fields for contact.updated", "error", err, "contact", contact.ID)
		fields = map[string]any{}
	}

	name, phone := a.MaskContactFields(orgID, contact.ProfileName, contact.PhoneNumber)

	a.PublishEvent(crmevents.New(orgID, string(models.WebhookEventContactUpdated),
		crmevents.UserActor(userID, ""), map[string]any{
			"contact_id":     contact.ID.String(),
			"contact_name":   name,
			"contact_phone":  phone,
			"tags":           contact.Tags,
			"source":         contact.Source,
			"assigned_to":    contact.AssignedUserID,
			"fields":         fields,
			"changed_fields": changed,
		}).ForContact(contact.ID))
}

// fieldValuesBefore reads a contact's custom field values ahead of an edit, so
// the change can be announced with what it was as well as what it became.
// A failed read yields no "from" values rather than failing the edit.
func (a *App) fieldValuesBefore(orgID, contactID uuid.UUID) map[string]any {
	values, err := customfields.New(a.DB).Values(context.Background(), orgID, contactID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("Failed to read field values before an edit", "error", err, "contact_id", contactID)
		return nil
	}
	return values
}

// publishFieldChanges announces each changed custom field with its old and new
// value (plan 01, §4.3), and the lifecycle stage with its own event as well.
//
// The events used to carry only the field's key. An automation trigger
// narrowed to "lifecycle changed to customer" therefore had nothing to compare
// and the funnel report, which reads the new value from the activity log,
// never counted a stage somebody set by hand.
//
// "Became a customer" gets its own event because it is the change reporting
// counts and automation reacts to; digging it out of a generic field_changed
// payload meant every consumer re-implemented the same filter.
func (a *App) publishFieldChanges(orgID uuid.UUID, actor crmevents.Actor, contactID uuid.UUID, before map[string]any, changed []string) {
	if len(changed) == 0 {
		return
	}
	after, err := customfields.New(a.DB).Values(context.Background(), orgID, contactID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("Failed to read field values for change events", "error", err, "contact_id", contactID)
	}

	for _, key := range changed {
		a.PublishEvent(crmevents.New(orgID, "contact.field_changed", actor, map[string]any{
			"field": key, "from": before[key], "to": after[key],
		}).ForContact(contactID))

		if key == models.FieldKeyLifecycleStage {
			a.PublishEvent(crmevents.New(orgID, "contact.lifecycle_stage_changed", actor, map[string]any{
				"stage": after[key], "from": before[key], "to": after[key],
			}).ForContact(contactID))
		}
	}
}

// messagesAround returns the page containing one message, with context on both
// sides of it (plan 02).
//
// `around` accepts a message id or an RFC3339 timestamp, because a timeline
// burst knows the instant it covers but not necessarily a specific message id.
func (a *App) messagesAround(r *fastglue.Request, base *gorm.DB, around string, limit int, total int64) error {
	anchor, ok := a.resolveAnchor(base, around)
	if !ok {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"around must be a message id or an RFC3339 timestamp", nil, "")
	}

	// Half the page on each side, so the message you clicked is in the middle
	// rather than clinging to an edge with nothing above it.
	half := limit / 2
	if half < 1 {
		half = 1
	}

	var older []models.Message
	if err := base.Session(&gorm.Session{}).
		Where("created_at < ?", anchor).
		Preload("ReplyToMessage").
		Order("created_at DESC").Limit(half).Find(&older).Error; err != nil {
		a.Log.Error("Failed to load messages before anchor", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list messages", nil, "")
	}

	var newer []models.Message
	if err := base.Session(&gorm.Session{}).
		Where("created_at >= ?", anchor).
		Preload("ReplyToMessage").
		Order("created_at ASC").Limit(limit - len(older)).Find(&newer).Error; err != nil {
		a.Log.Error("Failed to load messages after anchor", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list messages", nil, "")
	}

	// older came back newest-first; flip it so the page reads chronologically.
	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}
	messages := append(older, newer...)

	return r.SendEnvelope(map[string]any{
		"messages": a.buildMessagesResponse(messages),
		"total":    total,
		// There is more in both directions; the client pages with before/after
		// from the ends of what it got.
		"has_more": true,
		"anchor":   anchor.Format(time.RFC3339Nano),
	})
}

// resolveAnchor turns an `around` parameter into an instant.
func (a *App) resolveAnchor(base *gorm.DB, around string) (time.Time, bool) {
	if id, err := uuid.Parse(around); err == nil {
		var msg models.Message
		// Through the scoped query, so a message id from a contact the viewer
		// cannot see does not become a way to read their thread.
		if err := base.Session(&gorm.Session{}).Where("id = ?", id).First(&msg).Error; err != nil {
			return time.Time{}, false
		}
		return msg.CreatedAt, true
	}
	if at, err := time.Parse(time.RFC3339, around); err == nil {
		return at, true
	}
	return time.Time{}, false
}
