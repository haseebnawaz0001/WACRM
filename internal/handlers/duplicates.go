package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/dedupe"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Dedupe returns the duplicate-detection service.
func (a *App) Dedupe() *dedupe.Service { return dedupe.New(a.DB) }

// DuplicateContactSummary is one side of a suggested pair, with enough detail
// to decide without opening both records.
type DuplicateContactSummary struct {
	ID          string     `json:"id"`
	ProfileName string     `json:"profile_name"`
	PhoneNumber string     `json:"phone_number"`
	Tags        []string   `json:"tags"`
	Source      string     `json:"source,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastMessage *time.Time `json:"last_message_at,omitempty"`
	// Messages is how much history would move if this one were folded in,
	// which is usually what decides which record survives.
	Messages int64 `json:"message_count"`
}

// DuplicateCandidateResponse is one pair awaiting a decision.
type DuplicateCandidateResponse struct {
	ID    string `json:"id"`
	Score int    `json:"score"`
	// Reasons names the signals that matched, so somebody deciding can see why
	// rather than being asked to trust a number.
	Reasons    []string                `json:"reasons"`
	Status     string                  `json:"status"`
	DetectedAt time.Time               `json:"detected_at"`
	ContactA   DuplicateContactSummary `json:"contact_a"`
	ContactB   DuplicateContactSummary `json:"contact_b"`
}

// ListDuplicates returns the pairs waiting for somebody to decide.
func (a *App) ListDuplicates(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceContacts, models.ActionWrite)
	if err != nil {
		return err
	}

	limit := 50
	if n, ok := optionalIntArg(r.RequestCtx.QueryArgs(), "limit"); ok && n > 0 && n <= 200 {
		limit = n
	}

	candidates, err := a.Dedupe().PendingCandidates(context.Background(), orgID, limit)
	if err != nil {
		a.Log.Error("Failed to list duplicate candidates", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load duplicates", nil, "")
	}

	summaries, err := a.duplicateSummaries(orgID, candidates)
	if err != nil {
		a.Log.Error("Failed to summarise duplicate candidates", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load duplicates", nil, "")
	}

	items := make([]DuplicateCandidateResponse, 0, len(candidates))
	for _, candidate := range candidates {
		a, okA := summaries[candidate.ContactAID]
		b, okB := summaries[candidate.ContactBID]
		if !okA || !okB {
			// One side has since been deleted, so the pair is no longer a
			// question anybody needs to answer.
			continue
		}
		items = append(items, DuplicateCandidateResponse{
			ID:         candidate.ID.String(),
			Score:      candidate.Score,
			Reasons:    stringsFrom(candidate.Reasons),
			Status:     candidate.Status,
			DetectedAt: candidate.CreatedAt,
			ContactA:   a,
			ContactB:   b,
		})
	}
	return r.SendEnvelope(map[string]any{"candidates": items})
}

// duplicateSummaries loads both sides of every pair, with their message counts,
// in two queries rather than two per pair.
func (a *App) duplicateSummaries(orgID uuid.UUID, candidates []models.ContactDuplicateCandidate) (map[uuid.UUID]DuplicateContactSummary, error) {
	ids := make([]uuid.UUID, 0, len(candidates)*2)
	for _, candidate := range candidates {
		ids = append(ids, candidate.ContactAID, candidate.ContactBID)
	}
	if len(ids) == 0 {
		return map[uuid.UUID]DuplicateContactSummary{}, nil
	}

	var contacts []models.Contact
	if err := a.DB.Where("id IN ? AND organization_id = ?", ids, orgID).
		Find(&contacts).Error; err != nil {
		return nil, err
	}

	type countRow struct {
		ContactID uuid.UUID
		Count     int64
	}
	var counts []countRow
	if err := a.DB.Model(&models.Message{}).
		Select("contact_id, count(*) AS count").
		Where("contact_id IN ?", ids).
		Group("contact_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	byContact := make(map[uuid.UUID]int64, len(counts))
	for _, row := range counts {
		byContact[row.ContactID] = row.Count
	}

	out := make(map[uuid.UUID]DuplicateContactSummary, len(contacts))
	for _, contact := range contacts {
		out[contact.ID] = DuplicateContactSummary{
			ID:          contact.ID.String(),
			ProfileName: contact.ProfileName,
			PhoneNumber: contact.PhoneNumber,
			Tags:        stringsFrom(contact.Tags),
			Source:      contact.Source,
			CreatedAt:   contact.CreatedAt,
			LastMessage: contact.LastMessageAt,
			Messages:    byContact[contact.ID],
		}
	}
	return out, nil
}

// stringsFrom reads a loose JSON array column as strings.
func stringsFrom(raw models.JSONBArray) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ScanDuplicates looks for new pairs on demand.
func (a *App) ScanDuplicates(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionWrite)
	if err != nil {
		return err
	}

	found, err := a.Dedupe().Scan(context.Background(), orgID)
	if err != nil {
		a.Log.Error("Duplicate scan failed", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "The scan could not be run", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContacts, uuid.Nil, models.AuditActionUpdated, nil,
		map[string]any{"duplicate_scan": found})
	return r.SendEnvelope(map[string]any{"found": found})
}

// DismissDuplicate records that a pair is not the same person.
//
// A dismissal is permanent: re-suggesting a pair somebody has already said no
// to is how a review queue becomes something nobody opens.
func (a *App) DismissDuplicate(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionWrite)
	if err != nil {
		return err
	}
	candidateID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid candidate id", nil, "")
	}

	if err := a.Dedupe().Dismiss(context.Background(), orgID, candidateID, userID); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Candidate not found", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContacts, candidateID, models.AuditActionUpdated, nil,
		map[string]any{"duplicate": "dismissed"})
	return r.SendEnvelope(map[string]any{"dismissed": true})
}

// MergeContacts folds one contact into another.
//
// It needs contacts:delete rather than contacts:write: a merge removes a record
// from the list, and the person doing it has to be allowed to do that.
func (a *App) MergeContacts(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionDelete)
	if err != nil {
		return err
	}

	var req struct {
		PrimaryID   string `json:"primary_id"`
		SecondaryID string `json:"secondary_id"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	primaryID, err := uuid.Parse(req.PrimaryID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid primary contact id", nil, "")
	}
	secondaryID, err := uuid.Parse(req.SecondaryID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid secondary contact id", nil, "")
	}

	record, err := a.Dedupe().Merge(context.Background(), dedupe.MergeInput{
		OrgID:       orgID,
		PrimaryID:   primaryID,
		SecondaryID: secondaryID,
		ActorID:     userID,
	})
	if err != nil {
		// "Already merged", "same contact" and "not found" are all answers the
		// caller needs to read, not a 500.
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContacts, primaryID, models.AuditActionUpdated, nil,
		map[string]any{"merged_from": secondaryID.String()})

	return r.SendEnvelope(map[string]any{"merge": record})
}

// ContactMergeHistory returns what has been merged into a contact, so a record
// that suddenly has somebody else's history can explain itself.
func (a *App) ContactMergeHistory(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceContacts, models.ActionRead)
	if err != nil {
		return err
	}
	contactID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	var merges []models.ContactMerge
	if err := a.DB.Where("organization_id = ? AND primary_contact_id = ?", orgID, contactID).
		Order("created_at DESC").Find(&merges).Error; err != nil {
		a.Log.Error("Failed to load merge history", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load merge history", nil, "")
	}
	return r.SendEnvelope(map[string]any{"merges": merges})
}
