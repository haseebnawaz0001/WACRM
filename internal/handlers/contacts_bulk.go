package handlers

import (
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// MaxBulkContacts is how many contacts one bulk action may touch.
//
// A thousand is a working day's worth of cleanup and small enough that the
// request finishes inside an HTTP timeout. Anything larger is a data migration
// and belongs in an import, where it can be staged and checked.
const MaxBulkContacts = 1000

// BulkContactUpdateRequest is a change applied to a set of contacts.
//
// Every field is optional and nil means "leave it alone". Tags are add/remove
// rather than replace, because a bulk edit that replaced the tag list would
// silently strip tags the selected contacts had for other reasons.
type BulkContactUpdateRequest struct {
	ContactIDs []string `json:"contact_ids"`

	AddTags    []string `json:"add_tags"`
	RemoveTags []string `json:"remove_tags"`

	// AssignedUserID sets the relationship owner. The empty string clears it,
	// which a nil pointer cannot express.
	AssignedUserID *string `json:"assigned_user_id"`

	// Fields sets typed custom field values (plan 01).
	Fields map[string]any `json:"fields"`
}

// BulkUpdateContacts applies one change to many contacts (plan 01).
//
// The list page could select rows and do nothing with them, so retagging fifty
// contacts after an import meant fifty round trips through the detail page —
// which is why people did it in a spreadsheet and re-imported instead, losing
// everything the record had accumulated.
func (a *App) BulkUpdateContacts(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContacts, models.ActionWrite)
	if err != nil {
		return nil
	}

	var req BulkContactUpdateRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}

	ids, err := parseContactIDs(req.ContactIDs)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}
	if len(ids) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "contact_ids is required", nil, "")
	}
	if len(ids) > MaxBulkContacts {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"Too many contacts in one request", map[string]any{"max": MaxBulkContacts}, "")
	}

	// Only contacts this viewer may actually edit. Selecting rows they cannot
	// see is not possible through the UI, but the ids arrive in a request body
	// and a bulk endpoint is exactly where that matters.
	var contacts []models.Contact
	scoped := a.scopeAssignedContact(
		a.DB.Where("id IN ? AND organization_id = ?", ids, orgID), userID, orgID)
	if err := scoped.Find(&contacts).Error; err != nil {
		a.Log.Error("Failed to load contacts for bulk update", "error", err)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update contacts", nil, "")
	}

	var owner *uuid.UUID
	if req.AssignedUserID != nil && *req.AssignedUserID != "" {
		parsed, parseErr := uuid.Parse(*req.AssignedUserID)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid assigned_user_id", nil, "")
		}
		// A contact cannot be owned by somebody outside the organization.
		var count int64
		a.DB.Model(&models.UserOrganization{}).
			Where("user_id = ? AND organization_id = ?", parsed, orgID).Count(&count)
		if count == 0 {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "That user is not in this organization", nil, "")
		}
		owner = &parsed
	}

	updated := 0
	for i := range contacts {
		if a.applyBulkChange(orgID, userID, &contacts[i], req, owner) {
			updated++
		}
	}

	return r.SendEnvelope(map[string]any{
		"updated": updated,
		// The difference between requested and updated is contacts the viewer
		// cannot see. Reporting it is honest; silently updating fewer rows than
		// asked is how people lose confidence in a bulk action.
		"skipped": len(ids) - updated,
	})
}

// applyBulkChange applies the request to one contact, returning whether
// anything changed.
//
// Per contact rather than one bulk UPDATE because each change emits its own
// events — a tag added to forty contacts is forty timeline entries and forty
// automation triggers, which is what the people who set those rules up expect.
func (a *App) applyBulkChange(orgID, userID uuid.UUID, contact *models.Contact, req BulkContactUpdateRequest, owner *uuid.UUID) bool {
	changed := false

	if len(req.AddTags) > 0 || len(req.RemoveTags) > 0 {
		if a.applyBulkTags(orgID, userID, contact, req.AddTags, req.RemoveTags) {
			changed = true
		}
	}

	if req.AssignedUserID != nil {
		if a.applyBulkOwner(orgID, userID, contact, owner) {
			changed = true
		}
	}

	if len(req.Fields) > 0 {
		changedKeys, err := customfields.New(a.DB).SetValues(
			a.DB, orgID, contact.ID, models.FieldEntityContact, req.Fields, &userID)
		if err != nil {
			a.Log.Error("Failed to set fields in bulk update",
				"error", err, "contact_id", contact.ID)
		} else if len(changedKeys) > 0 {
			changed = true
			for _, key := range changedKeys {
				a.PublishEvent(crmevents.New(orgID, "contact.field_changed",
					crmevents.UserActor(userID, ""), map[string]any{"field": key}).
					ForContact(contact.ID))
				if key == models.FieldKeyLifecycleStage {
					a.publishLifecycleStageChanged(orgID, userID, contact.ID)
				}
			}
		}
	}

	return changed
}

// applyBulkTags adds and removes tags on one contact, emitting one event per
// tag actually moved.
func (a *App) applyBulkTags(orgID, userID uuid.UUID, contact *models.Contact, add, remove []string) bool {
	current := make([]string, 0, len(contact.Tags))
	for _, raw := range contact.Tags {
		if tag, ok := raw.(string); ok {
			current = append(current, tag)
		}
	}

	has := func(tag string) bool {
		for _, existing := range current {
			if strings.EqualFold(existing, tag) {
				return true
			}
		}
		return false
	}

	added, removed := []string{}, []string{}
	next := append([]string(nil), current...)

	for _, tag := range add {
		tag = strings.TrimSpace(tag)
		if tag == "" || has(tag) {
			continue
		}
		next = append(next, tag)
		added = append(added, tag)
		current = next
	}

	for _, tag := range remove {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		kept := next[:0]
		for _, existing := range next {
			if strings.EqualFold(existing, tag) {
				removed = append(removed, existing)
				continue
			}
			kept = append(kept, existing)
		}
		next = kept
	}

	if len(added) == 0 && len(removed) == 0 {
		return false
	}

	asAny := make(models.JSONBArray, 0, len(next))
	for _, tag := range next {
		asAny = append(asAny, tag)
	}
	if err := a.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("tags", asAny).Error; err != nil {
		a.Log.Error("Failed to update tags in bulk", "error", err, "contact_id", contact.ID)
		return false
	}

	for _, tag := range added {
		a.PublishEvent(crmevents.New(orgID, "contact.tag_added",
			crmevents.UserActor(userID, ""), map[string]any{"tag": tag}).ForContact(contact.ID))
	}
	for _, tag := range removed {
		a.PublishEvent(crmevents.New(orgID, "contact.tag_removed",
			crmevents.UserActor(userID, ""), map[string]any{"tag": tag}).ForContact(contact.ID))
	}
	return true
}

// applyBulkOwner reassigns one contact, emitting contact.assigned when it moved.
func (a *App) applyBulkOwner(orgID, userID uuid.UUID, contact *models.Contact, owner *uuid.UUID) bool {
	if sameOwner(contact.AssignedUserID, owner) {
		return false
	}

	if err := a.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("assigned_user_id", owner).Error; err != nil {
		a.Log.Error("Failed to reassign contact in bulk", "error", err, "contact_id", contact.ID)
		return false
	}

	data := map[string]any{}
	if owner != nil {
		data["to"] = owner.String()
	}
	a.PublishEvent(crmevents.New(orgID, "contact.assigned",
		crmevents.UserActor(userID, ""), data).ForContact(contact.ID))
	return true
}

func sameOwner(a, b *uuid.UUID) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// parseContactIDs converts the request's ids, refusing the whole request on a
// malformed one rather than silently editing a subset.
func parseContactIDs(raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, errBadContactID
		}
		out = append(out, id)
	}
	return out, nil
}

var errBadContactID = &bulkError{"contact_ids contains a value that is not an id"}

type bulkError struct{ msg string }

func (e *bulkError) Error() string { return e.msg }
