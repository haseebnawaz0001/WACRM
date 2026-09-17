package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/segments"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Segments returns the segment service.
func (a *App) Segments() *segments.Service { return segments.New(a.DB) }

// SegmentResponse is the API shape of a saved audience.
type SegmentResponse struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Filter      contactquery.Filter `json:"filter"`
	Visibility  string              `json:"visibility"`
	CreatedByID string              `json:"created_by_id,omitempty"`
	// ContactCount is the last count taken, with when it was taken beside it:
	// a number with no age is a number people quote long after it was true.
	ContactCount *int       `json:"contact_count,omitempty"`
	CountedAt    *time.Time `json:"counted_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toSegmentResponse(segment models.Segment) SegmentResponse {
	out := SegmentResponse{
		ID:           segment.ID.String(),
		Name:         segment.Name,
		Description:  segment.Description,
		Visibility:   segment.Visibility,
		ContactCount: segment.ContactCount,
		CountedAt:    segment.CountedAt,
		LastUsedAt:   segment.LastUsedAt,
		CreatedAt:    segment.CreatedAt,
	}
	if segment.CreatedByID != uuid.Nil {
		out.CreatedByID = segment.CreatedByID.String()
	}
	if filter, err := contactquery.ParseFilter(rawJSON(segment.Filter)); err == nil {
		out.Filter = filter
	}
	return out
}

// rawJSON re-encodes a stored JSONB column so the shared filter parser can read
// it, keeping one parsing path rather than a second, subtly different one.
func rawJSON(value models.JSONB) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

// ListSegments returns the segments the caller may see.
func (a *App) ListSegments(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionRead)
	if err != nil {
		return err
	}

	rows, err := a.Segments().List(context.Background(), orgID, userID)
	if err != nil {
		a.Log.Error("Failed to list segments", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load segments", nil, "")
	}

	search := strings.ToLower(strings.TrimSpace(string(r.RequestCtx.QueryArgs().Peek("search"))))
	visibility := string(r.RequestCtx.QueryArgs().Peek("visibility"))

	items := make([]SegmentResponse, 0, len(rows))
	for _, row := range rows {
		if search != "" && !strings.Contains(strings.ToLower(row.Name), search) {
			continue
		}
		if visibility != "" && row.Visibility != visibility {
			continue
		}
		items = append(items, toSegmentResponse(row))
	}
	return r.SendEnvelope(map[string]any{"segments": items})
}

type segmentRequest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Filter      json.RawMessage `json:"filter"`
	Visibility  string          `json:"visibility"`
}

// CreateSegment saves a new audience.
func (a *App) CreateSegment(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionWrite)
	if err != nil {
		return err
	}
	return a.saveSegment(r, orgID, userID, nil)
}

// UpdateSegment edits an audience.
func (a *App) UpdateSegment(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionWrite)
	if err != nil {
		return err
	}
	segmentID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid segment id", nil, "")
	}
	return a.saveSegment(r, orgID, userID, &segmentID)
}

func (a *App) saveSegment(r *fastglue.Request, orgID, userID uuid.UUID, id *uuid.UUID) error {
	var req segmentRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	filter, err := contactquery.ParseFilter(req.Filter)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		a.Log.Error("Failed to build contact filter registry", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	segment, err := a.Segments().Save(context.Background(), registry, segments.SaveInput{
		OrgID:       orgID,
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Filter:      filter,
		Visibility:  req.Visibility,
		ActorID:     userID,
	})
	if err != nil {
		// A rejected filter, a duplicate name or a cycle are all the caller's
		// mistake, and each message names what is wrong.
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	action := models.AuditActionCreated
	if id != nil {
		action = models.AuditActionUpdated
	}
	a.logAudit(orgID, userID, models.ResourceSegments, segment.ID, action, nil,
		map[string]any{"name": segment.Name})

	return r.SendEnvelope(map[string]any{"segment": toSegmentResponse(*segment)})
}

// GetSegment returns one audience.
func (a *App) GetSegment(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionRead)
	if err != nil {
		return err
	}
	segment, err := a.segmentFor(r, orgID, userID)
	if err != nil {
		return nil
	}
	return r.SendEnvelope(map[string]any{"segment": toSegmentResponse(*segment)})
}

// segmentFor loads the segment named in the path, enforcing that a private one
// belongs to the caller.
//
// On failure it writes the error envelope and returns errEnvelopeSent, so the
// caller returns nil early — the same contract requireAuth uses. Returning the
// envelope call's own nil error would hand the caller a nil segment and no
// error, which is a panic waiting to happen.
func (a *App) segmentFor(r *fastglue.Request, orgID, userID uuid.UUID) (*models.Segment, error) {
	segmentID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		_ = r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid segment id", nil, "")
		return nil, errEnvelopeSent
	}

	segment, err := a.Segments().Get(context.Background(), orgID, segmentID)
	if err != nil {
		_ = r.SendErrorEnvelope(fasthttp.StatusNotFound, "Segment not found", nil, "")
		return nil, errEnvelopeSent
	}
	if segment.Visibility == models.SegmentPrivate && segment.CreatedByID != userID {
		// Not found rather than forbidden: someone else's private segment
		// should not be discoverable by probing ids.
		_ = r.SendErrorEnvelope(fasthttp.StatusNotFound, "Segment not found", nil, "")
		return nil, errEnvelopeSent
	}
	return segment, nil
}

// DeleteSegment removes an audience.
func (a *App) DeleteSegment(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionDelete)
	if err != nil {
		return err
	}
	segmentID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid segment id", nil, "")
	}

	// The segments service blocks on other segments and pending campaigns.
	// Automation rules can name one too — a chatbot CRM condition or a rule's
	// contact filter — and those were not checked (plan 10, S8).
	if dependents, depErr := entityrefs.FindDependents(a.DB, orgID, segmentID.String()); depErr != nil {
		a.Log.Error("Failed to check segment references", "error", depErr, "segment_id", segmentID)
	} else if rules := automationDependents(dependents); len(rules) > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict,
			"This segment is still used by "+entityrefs.DescribeDependents(rules),
			map[string]any{"dependents": rules}, "")
	}

	if err := a.Segments().Delete(context.Background(), orgID, segmentID); err != nil {
		if errors.Is(err, segments.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Segment not found", nil, "")
		}
		// A segment another segment or campaign points at cannot simply go:
		// the message names the dependents so the caller can unpick them.
		return r.SendErrorEnvelope(fasthttp.StatusConflict, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceSegments, segmentID, models.AuditActionDeleted, nil, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

// CountSegment recounts an audience now.
func (a *App) CountSegment(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionRead)
	if err != nil {
		return err
	}
	segment, err := a.segmentFor(r, orgID, userID)
	if err != nil {
		return nil
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	count, err := a.Segments().Count(context.Background(), registry, a.contactViewer(orgID, userID), segment.ID)
	if err != nil {
		a.Log.Error("Failed to count segment", "error", err, "segment_id", segment.ID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to count the segment", nil, "")
	}
	return r.SendEnvelope(map[string]any{"count": count})
}

// PreviewSegmentCount counts an unsaved filter, for the builder's live count.
func (a *App) PreviewSegmentCount(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionRead)
	if err != nil {
		return err
	}

	var req struct {
		Filter json.RawMessage `json:"filter"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	filter, err := contactquery.ParseFilter(req.Filter)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	query, err := contactquery.Apply(a.DB.Model(&models.Contact{}), registry,
		a.contactViewer(orgID, userID), filter)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		a.Log.Error("Failed to preview segment count", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to count", nil, "")
	}
	return r.SendEnvelope(map[string]any{"count": count})
}

// SegmentContacts lists an audience's members, in the same shape as the
// contacts list so the UI renders them with one component.
func (a *App) SegmentContacts(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceSegments, models.ActionRead)
	if err != nil {
		return err
	}
	segment, err := a.segmentFor(r, orgID, userID)
	if err != nil {
		return nil
	}

	var req contactSearchRequest
	if body := r.RequestCtx.PostBody(); len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
		}
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	// Browsing applies the viewer's own contact scope: an agent opening "VIP"
	// sees the VIP contacts they are allowed to see, not everybody's.
	viewer := a.contactViewer(orgID, userID)
	query, err := a.Segments().Apply(context.Background(), a.DB.Model(&models.Contact{}),
		registry, viewer, segment.ID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}
	query = applyContactSearchTerm(query, req.Search)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		a.Log.Error("Failed to count segment members", "error", err, "segment_id", segment.ID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load members", nil, "")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = contactSearchDefaultLimit
	}
	if limit > contactSearchMaxLimit {
		limit = contactSearchMaxLimit
	}
	page := req.Page
	if page < 1 {
		page = 1
	}

	var contacts []models.Contact
	if err := query.Order(contactSortClause(registry, req)).
		Offset((page - 1) * limit).Limit(limit).Find(&contacts).Error; err != nil {
		a.Log.Error("Failed to load segment members", "error", err, "segment_id", segment.ID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load members", nil, "")
	}

	items, err := a.buildContactSearchResults(orgID, userID, contacts, req.Include)
	if err != nil {
		a.Log.Error("Failed to load segment member extras", "error", err, "segment_id", segment.ID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load members", nil, "")
	}

	// Opening a segment is using it, and the count refresher prioritises the
	// ones people actually look at.
	a.Segments().MarkUsed(context.Background(), orgID, segment.ID)

	return r.SendEnvelope(map[string]any{
		"contacts": items, "total": total, "page": page, "limit": limit,
		"segment": toSegmentResponse(*segment),
	})
}

// automationDependents narrows a dependency list to rules.
//
// Segments referencing segments are already the segments service's own check,
// with a better message: it names them without a second query.
func automationDependents(all []entityrefs.Dependent) []entityrefs.Dependent {
	out := make([]entityrefs.Dependent, 0, len(all))
	for _, d := range all {
		if d.Kind == "automation" {
			out = append(out, d)
		}
	}
	return out
}
