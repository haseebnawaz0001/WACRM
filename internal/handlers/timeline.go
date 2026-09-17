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
	orgID, userID, err := a.requireAnyPermission(r,
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
	query := a.scopeAssignedContact(
		a.DB.Where("id = ? AND organization_id = ?", contactID, orgID), userID, orgID)
	if err := query.First(&contact).Error; err != nil {
		// Scoped, not just org-checked: the timeline is the contact's whole
		// history, and reaching it only needed chat:read and an id, so an agent
		// could read the full record of a contact the list refuses to show them
		// (plan 10, S9).
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

	// A date has no instant of its own, so the range is resolved in the
	// organization's timezone (plan 10, S11). Parsing these as UTC while the
	// picker built them from the viewer's calendar moves several hours of a
	// contact's history into the wrong day at each end.
	loc := a.OrgLocation(orgID)
	args := r.RequestCtx.QueryArgs()
	if from := optionalDateArg(args, "from"); from != nil {
		start := startOfDayIn(*from, loc)
		opts.From = &start
	}
	if to := optionalDateArg(args, "to"); to != nil {
		// Inclusive: "to the 9th" means the whole of the 9th, not midnight at
		// the start of it.
		end := endOfDayIn(*to, loc)
		opts.To = &end
	}

	opts.HideActivity = a.hiddenTimelineActivity(userID, orgID)

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

// hiddenTimelineActivity lists the activity types this viewer may not read
// (plan 02).
//
// The timeline gathers a contact's whole story, which means it would otherwise
// be a way around every other permission in the product: an agent with no
// access to the Deals board could still read a contact's deal history, and one
// without tasks could read the follow-ups. The contact-level check is not
// enough on its own.
func (a *App) hiddenTimelineActivity(userID, orgID uuid.UUID) []string {
	var hidden []string

	if !a.HasPermission(userID, models.ResourceDeals, models.ActionRead, orgID) {
		hidden = append(hidden,
			"deal.created", "deal.updated", "deal.deleted",
			"deal.stage_changed", "deal.won", "deal.lost")
	}
	if !a.HasPermission(userID, models.ResourceTasks, models.ActionRead, orgID) {
		hidden = append(hidden,
			"task.created", "task.completed", "task.cancelled",
			"task.overdue", "task.updated", "task.due")
	}

	return hidden
}

// startOfDayIn and endOfDayIn turn a calendar date into the instant range it
// covers in a given zone.
//
// A date is not an instant: "2026-09-18" is a different eleven-hour window in
// Auckland than in Los Angeles. Resolving it in the organization's zone is
// what makes a filtered timeline agree with the dates the viewer picked.
func startOfDayIn(day time.Time, loc *time.Location) time.Time {
	local := day.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func endOfDayIn(day time.Time, loc *time.Location) time.Time {
	local := day.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 999999999, loc)
}
