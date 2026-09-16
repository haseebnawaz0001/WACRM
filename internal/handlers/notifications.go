package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// NotificationResponse is the API shape of one notification.
type NotificationResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Body       string         `json:"body"`
	Link       string         `json:"link"`
	EntityType string         `json:"entity_type,omitempty"`
	EntityID   string         `json:"entity_id,omitempty"`
	Data       map[string]any `json:"data"`
	ReadAt     *time.Time     `json:"read_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

func toNotificationResponse(n models.Notification) NotificationResponse {
	out := NotificationResponse{
		ID:         n.ID.String(),
		Type:       n.Type,
		Title:      n.Title,
		Body:       n.Body,
		Link:       n.Link,
		EntityType: n.EntityType,
		Data:       n.Data,
		ReadAt:     n.ReadAt,
		CreatedAt:  n.CreatedAt,
	}
	if n.EntityID != nil {
		out.EntityID = n.EntityID.String()
	}
	return out
}

// Notify returns the notification service, wired to push over WebSocket.
func (a *App) Notify() *notify.Service {
	return &notify.Service{DB: a.DB, Signal: a}
}

// NotificationCreated pushes a stored notification to its owner's clients.
//
// It satisfies notify.Signaller. Delivery is best-effort and happens after the
// row is committed, so a disconnected user loses the live update but never the
// notification itself.
func (a *App) NotificationCreated(n *models.Notification) {
	if a.WSHub == nil {
		return
	}
	a.WSHub.BroadcastToUser(n.OrganizationID, n.UserID, websocket.WSMessage{
		Type:    websocket.TypeNotificationCreated,
		Payload: toNotificationResponse(*n),
	})
}

// ListNotifications returns the caller's notifications, newest first.
//
// There is no permission resource: a notification belongs to exactly one user,
// and the query is scoped to the caller, so there is nothing to authorise
// beyond being signed in.
func (a *App) ListNotifications(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	opts := notify.ListOpts{
		UnreadOnly: string(r.RequestCtx.QueryArgs().Peek("unread_only")) == "true",
		Limit:      notify.DefaultLimit,
	}
	if raw := string(r.RequestCtx.QueryArgs().Peek("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			opts.Limit = n
		}
	}
	if cursor := parseNotificationCursor(r); cursor != nil {
		opts.Before = cursor
	}

	rows, next, err := a.Notify().List(context.Background(), orgID, userID, opts)
	if err != nil {
		a.Log.Error("Failed to list notifications", "error", err, "user_id", userID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load notifications", nil, "")
	}

	items := make([]NotificationResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toNotificationResponse(row))
	}

	payload := map[string]any{"notifications": items}
	if next != nil {
		// The cursor is opaque to the client: two values joined so the next
		// request can resume exactly where this page stopped.
		payload["next_cursor"] = next.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + next.ID.String()
	}
	return r.SendEnvelope(payload)
}

// parseNotificationCursor reads the opaque cursor from the query string.
// A malformed cursor is ignored rather than rejected: the worst outcome is
// starting from the top, which is recoverable, whereas an error is not.
func parseNotificationCursor(r *fastglue.Request) *notify.Cursor {
	raw := string(r.RequestCtx.QueryArgs().Peek("cursor"))
	if raw == "" {
		return nil
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '|' {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, raw[:i])
		if err != nil {
			return nil
		}
		id, err := uuid.Parse(raw[i+1:])
		if err != nil {
			return nil
		}
		return &notify.Cursor{CreatedAt: at, ID: id}
	}
	return nil
}

// GetUnreadNotificationCount returns the caller's unread count for the bell badge.
func (a *App) GetUnreadNotificationCount(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	count, err := a.Notify().UnreadCount(context.Background(), orgID, userID)
	if err != nil {
		a.Log.Error("Failed to count notifications", "error", err, "user_id", userID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to count notifications", nil, "")
	}
	return r.SendEnvelope(map[string]any{"count": count})
}

// MarkNotificationRead marks one of the caller's notifications read.
func (a *App) MarkNotificationRead(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	id, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid notification id", nil, "")
	}

	// Scoped by user, so this cannot touch anyone else's notification.
	if err := a.Notify().MarkRead(context.Background(), orgID, userID, id); err != nil {
		a.Log.Error("Failed to mark notification read", "error", err, "notification_id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to mark notification read", nil, "")
	}
	return r.SendEnvelope(map[string]any{"message": "Notification marked read"})
}

// MarkAllNotificationsRead clears the caller's unread badge.
func (a *App) MarkAllNotificationsRead(r *fastglue.Request) error {
	orgID, userID, err := a.getOrgAndUserID(r)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusUnauthorized, "Unauthorized", nil, "")
	}

	if err := a.Notify().MarkAllRead(context.Background(), orgID, userID); err != nil {
		a.Log.Error("Failed to mark notifications read", "error", err, "user_id", userID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to mark notifications read", nil, "")
	}
	return r.SendEnvelope(map[string]any{"message": "All notifications marked read"})
}
