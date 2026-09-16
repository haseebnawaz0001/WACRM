package handlers

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/timeline"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// GetContactTimeline returns one contact's merged history (plan 02).
//
// The same permission rule as the contact record itself: someone who can see
// the contact can see what happened to it. A separate permission would mean a
// timeline that shows a contact the viewer cannot open.
func (a *App) GetContactTimeline(r *fastglue.Request) error {
	orgID, _, err := a.requireAnyPermission(r,
		perm(models.ResourceContacts, models.ActionRead),
		perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return err
	}

	contactID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID).
		First(&contact).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact not found", nil, "")
	}

	opts := timeline.Opts{}
	if raw := string(r.RequestCtx.QueryArgs().Peek("limit")); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 {
			opts.Limit = n
		}
	}
	if raw := string(r.RequestCtx.QueryArgs().Peek("types")); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				opts.Types = append(opts.Types, trimmed)
			}
		}
	}
	if raw := string(r.RequestCtx.QueryArgs().Peek("before")); raw != "" {
		if at, parseErr := time.Parse(time.RFC3339Nano, raw); parseErr == nil {
			opts.Before = &at
		}
	}

	items, err := timeline.New(a.DB).Build(context.Background(), orgID, contactID, opts)
	if err != nil {
		a.Log.Error("Failed to build timeline", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load timeline", nil, "")
	}

	payload := map[string]any{"items": items}
	if len(items) > 0 {
		// The cursor is the oldest item on this page, so the next request
		// resumes exactly where this one stopped.
		payload["next_before"] = items[len(items)-1].OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return r.SendEnvelope(payload)
}
