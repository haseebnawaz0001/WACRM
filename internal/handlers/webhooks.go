package handlers

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/safehttp"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// validateWebhookURL validates an outbound webhook URL. The rules live in
// internal/safehttp so webhooks, custom actions and IVR callbacks cannot drift
// apart on what counts as an internal address.
func validateWebhookURL(rawURL string) error { return safehttp.ValidateURL(rawURL) }

// SSRFSafeDialer returns the shared SSRF-safe DialContext.
func SSRFSafeDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return safehttp.Dialer()
}

// WebhookRequest represents the request body for creating/updating a webhook
type WebhookRequest struct {
	Name     string            `json:"name"`
	URL      string            `json:"url"`
	Events   []string          `json:"events"`
	Headers  map[string]string `json:"headers"`
	Secret   string            `json:"secret"`
	IsActive bool              `json:"is_active"`
}

// WebhookResponse represents the API response for a webhook
type WebhookResponse struct {
	ID        uuid.UUID         `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Events    []string          `json:"events"`
	Headers   map[string]string `json:"headers"`
	IsActive  bool              `json:"is_active"`
	HasSecret bool              `json:"has_secret"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// webhookEventLabels gives each catalog event a human name for the picker.
//
// Only the wording lives here. Which events exist is the catalog's business, so
// a new event cannot be silently unsubscribable — the previous hand-maintained
// list had drifted to the point that none of the CRM events plans 03, 04 and 07
// added could be subscribed to at all.
var webhookEventLabels = map[string]struct{ Label, Description string }{
	"message.incoming":    {"Message Incoming", "A new message is received from a contact"},
	"message.outgoing":    {"Message Outgoing", "A message is sent to a contact (includes echoes)"},
	"message.sent":        {"Message Sent", "An agent sends a message"},
	"contact.created":     {"Contact Created", "A new contact is created"},
	"contact.updated":     {"Contact Updated", "A contact's details or custom fields change"},
	"contact.deleted":     {"Contact Deleted", "A contact is deleted"},
	"contact.restored":    {"Contact Restored", "A deleted contact is restored"},
	"contact.merged":      {"Contact Merged", "Two contacts are merged"},
	"contact.assigned":    {"Contact Assigned", "A contact's owner changes"},
	"contact.tag_added":   {"Tag Added", "A tag is added to a contact"},
	"contact.tag_removed": {"Tag Removed", "A tag is removed from a contact"},

	"conversation.created":        {"Conversation Started", "A new conversation opens"},
	"conversation.status_changed": {"Conversation Status Changed", "A conversation is resolved, snoozed, pending or reopened"},
	"conversation.assigned":       {"Conversation Assigned", "A conversation is assigned to an agent or team"},

	"task.created":   {"Task Created", "A follow-up task is created"},
	"task.completed": {"Task Completed", "A task is completed"},
	"task.overdue":   {"Task Overdue", "A task passes its deadline"},

	"deal.created":       {"Deal Created", "A deal is added to a pipeline"},
	"deal.updated":       {"Deal Updated", "A deal's details change"},
	"deal.stage_changed": {"Deal Stage Changed", "A deal moves to another stage"},
	"deal.won":           {"Deal Won", "A deal is marked won"},
	"deal.lost":          {"Deal Lost", "A deal is marked lost"},
	"deal.deleted":       {"Deal Deleted", "A deal is deleted"},

	"transfer.created":  {"Transfer Created", "A transfer to a human agent is requested"},
	"transfer.assigned": {"Transfer Assigned", "A transfer is assigned to an agent"},
	"transfer.resumed":  {"Transfer Resumed", "The chatbot resumes (transfer closed)"},
}

// availableWebhookEvents builds the picker list from the catalog.
func availableWebhookEvents() []map[string]string {
	types := crmevents.WebhookEventTypes()
	out := make([]map[string]string, 0, len(types))
	for _, t := range types {
		meta, ok := webhookEventLabels[t]
		if !ok {
			// A catalog event with no wording yet is still offered, labelled
			// by its type: unsubscribable is worse than unpolished.
			meta.Label = t
		}
		out = append(out, map[string]string{
			"value": t, "label": meta.Label, "description": meta.Description,
		})
	}
	return out
}

// isKnownWebhookEvent reports whether an event may be subscribed to.
func isKnownWebhookEvent(eventType string) bool {
	spec, ok := crmevents.Lookup(eventType)
	return ok && spec.Webhook
}

// ListWebhooks returns all webhooks for the organization
func (a *App) ListWebhooks(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionRead)
	if err != nil {
		return nil
	}

	pg := parsePagination(r)
	search := string(r.RequestCtx.QueryArgs().Peek("search"))

	query := a.DB.Where("organization_id = ?", orgID)

	// Apply search filter - search by name or URL (case-insensitive)
	if search != "" {
		searchPattern := "%" + search + "%"
		query = query.Where("name ILIKE ? OR url ILIKE ?", searchPattern, searchPattern)
	}

	var total int64
	query.Model(&models.Webhook{}).Count(&total)

	var webhooks []models.Webhook
	if err := pg.Apply(query.Model(&models.Webhook{}).Order("created_at DESC")).
		Find(&webhooks).Error; err != nil {
		a.Log.Error("Failed to list webhooks", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to list webhooks", nil, "")
	}

	result := make([]WebhookResponse, len(webhooks))
	for i, wh := range webhooks {
		result[i] = webhookToResponse(wh)
	}

	return r.SendEnvelope(map[string]any{
		"webhooks":         result,
		"available_events": availableWebhookEvents(),
		"total":            total,
		"page":             pg.Page,
		"limit":            pg.Limit,
	})
}

// GetWebhook returns a single webhook by ID
func (a *App) GetWebhook(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionRead)
	if err != nil {
		return nil
	}

	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}

	webhook, err := findByIDAndOrg[models.Webhook](a.DB, r, webhookID, orgID, "Webhook")
	if err != nil {
		return nil
	}

	return r.SendEnvelope(webhookToResponse(*webhook))
}

// CreateWebhook creates a new webhook
func (a *App) CreateWebhook(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionWrite)
	if err != nil {
		return nil
	}

	var req WebhookRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	if req.Name == "" || req.URL == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "name and url are required", nil, "")
	}

	if err := validateWebhookURL(req.URL); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	if len(req.Events) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "at least one event must be selected", nil, "")
	}
	// An event that is not in the catalog can never be delivered, so accepting
	// it would create a subscription that silently does nothing (plan 00, F11).
	for _, e := range req.Events {
		if !isKnownWebhookEvent(e) {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
				"unknown event type: "+e, nil, "")
		}
	}

	// Convert headers to JSONB
	headers := models.JSONB{}
	for k, v := range req.Headers {
		headers[k] = v
	}

	// Auto-generate secret if not provided
	secret := req.Secret
	if secret == "" {
		secret = generateVerifyToken() // Reuse the 32-byte hex generator
	}

	webhook := models.Webhook{
		OrganizationID: orgID,
		Name:           req.Name,
		URL:            req.URL,
		Events:         req.Events,
		Headers:        headers,
		Secret:         secret,
		IsActive:       true,
	}

	if err := a.DB.Create(&webhook).Error; err != nil {
		a.Log.Error("Failed to create webhook", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create webhook", nil, "")
	}

	// Invalidate cache
	a.InvalidateWebhooksCache(orgID)

	a.logAudit(orgID, userID,
		"webhook", webhook.ID, models.AuditActionCreated, nil, webhookAuditSnapshot(&webhook))

	return r.SendEnvelope(webhookToResponse(webhook))
}

// UpdateWebhook updates an existing webhook
func (a *App) UpdateWebhook(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionWrite)
	if err != nil {
		return nil
	}

	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}

	webhook, err := findByIDAndOrg[models.Webhook](a.DB, r, webhookID, orgID, "Webhook")
	if err != nil {
		return nil
	}

	oldSnap := webhookAuditSnapshot(webhook)

	var req WebhookRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	if req.Name != "" {
		webhook.Name = req.Name
	}
	if req.URL != "" {
		if err := validateWebhookURL(req.URL); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
		}
		webhook.URL = req.URL
	}
	if len(req.Events) > 0 {
		for _, e := range req.Events {
			if !isKnownWebhookEvent(e) {
				return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
					"unknown event type: "+e, nil, "")
			}
		}
		webhook.Events = req.Events
	}

	// Update headers if provided
	if req.Headers != nil {
		headers := models.JSONB{}
		for k, v := range req.Headers {
			headers[k] = v
		}
		webhook.Headers = headers
	}

	// Update secret if provided (empty string clears it)
	if req.Secret != "" {
		webhook.Secret = req.Secret
	}

	webhook.IsActive = req.IsActive

	if err := a.DB.Save(webhook).Error; err != nil {
		a.Log.Error("Failed to update webhook", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update webhook", nil, "")
	}

	// Invalidate cache
	a.InvalidateWebhooksCache(orgID)

	a.logAudit(orgID, userID,
		"webhook", webhook.ID, models.AuditActionUpdated, oldSnap, webhookAuditSnapshot(webhook))

	return r.SendEnvelope(webhookToResponse(*webhook))
}

// DeleteWebhook deletes a webhook
func (a *App) DeleteWebhook(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionDelete)
	if err != nil {
		return nil
	}

	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}

	var webhook models.Webhook
	if err := a.DB.Where("id = ? AND organization_id = ?", webhookID, orgID).First(&webhook).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Webhook not found", nil, "")
	}

	if err := a.DB.Delete(&webhook).Error; err != nil {
		a.Log.Error("Failed to delete webhook", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete webhook", nil, "")
	}

	// Invalidate cache
	a.InvalidateWebhooksCache(orgID)

	a.logAudit(orgID, userID,
		"webhook", webhookID, models.AuditActionDeleted, webhookAuditSnapshot(&webhook), nil)

	return r.SendEnvelope(map[string]string{"message": "Webhook deleted successfully"})
}

// TestWebhook sends a test event to a webhook
func (a *App) TestWebhook(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceWebhooks, models.ActionWrite)
	if err != nil {
		return nil
	}

	webhookID, err := parsePathUUID(r, "id", "webhook")
	if err != nil {
		return nil
	}

	webhook, err := findByIDAndOrg[models.Webhook](a.DB, r, webhookID, orgID, "Webhook")
	if err != nil {
		return nil
	}

	// Send a test event synchronously
	testData := map[string]any{
		"test":      true,
		"message":   "This is a test webhook from WA CRM",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	payload := OutboundWebhookPayload{
		Event:     "test",
		Timestamp: time.Now().UTC(),
		Data:      testData,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		a.Log.Error("Failed to create test payload", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create test payload", nil, "")
	}

	// Use timeout context for test webhook request
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := a.sendWebhookRequest(ctx, *webhook, jsonData); err != nil {
		a.Log.Error("Webhook test failed", "error", err, "webhook_id", webhook.ID)
		return r.SendErrorEnvelope(fasthttp.StatusBadGateway, "Webhook test failed", nil, "")
	}

	return r.SendEnvelope(map[string]string{"message": "Test webhook sent successfully"})
}

func webhookAuditSnapshot(wh *models.Webhook) map[string]any {
	if wh == nil {
		return nil
	}
	events := make([]string, len(wh.Events))
	copy(events, wh.Events)
	return map[string]any{
		"name":      wh.Name,
		"url":       wh.URL,
		"events":    events,
		"is_active": wh.IsActive,
	}
}

func webhookToResponse(wh models.Webhook) WebhookResponse {
	// Convert events
	events := make([]string, len(wh.Events))
	copy(events, wh.Events)

	// Convert headers
	headers := make(map[string]string)
	for k, v := range wh.Headers {
		if strVal, ok := v.(string); ok {
			headers[k] = strVal
		}
	}

	return WebhookResponse{
		ID:        wh.ID,
		Name:      wh.Name,
		URL:       wh.URL,
		Events:    events,
		Headers:   headers,
		IsActive:  wh.IsActive,
		HasSecret: wh.Secret != "",
		CreatedAt: wh.CreatedAt.Format(time.RFC3339),
		UpdatedAt: wh.UpdatedAt.Format(time.RFC3339),
	}
}
