package handlers

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/segments"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// contactSearchRequest is the body of POST /api/contacts/search.
type contactSearchRequest struct {
	Filter json.RawMessage `json:"filter"`
	Search string          `json:"search"`
	Sort   []struct {
		Field string `json:"field"`
		Dir   string `json:"dir"`
	} `json:"sort"`
	Page    int      `json:"page"`
	Limit   int      `json:"limit"`
	Include []string `json:"include"`
}

// contactSearchLimits bound one page.
const (
	contactSearchDefaultLimit = 50
	contactSearchMaxLimit     = 200
	contactSearchMaxTerm      = 200
)

// SearchContacts is the contacts list v2 (plan 01 + plan 00 F6).
//
// The old list could filter by a search string and a tag OR-list, sorted on a
// fixed column and re-sorted in the browser, and counted unread messages with
// one query per row. This one compiles a structured filter, sorts server-side
// against a whitelist, and fetches field values and unread counts for the whole
// page in one query each.
func (a *App) SearchContacts(r *fastglue.Request) error {
	// Same rule as GetContact: contacts:read sees everything, chat:read sees
	// the contacts they are handling. Requiring contacts:read outright would
	// lock agents out of searching their own inbox.
	orgID, userID, err := a.requireAnyPermission(r,
		perm(models.ResourceContacts, models.ActionRead),
		perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return err
	}

	var req contactSearchRequest
	if body := r.RequestCtx.PostBody(); len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
		}
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		a.Log.Error("Failed to build contact filter registry", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	filter, err := contactquery.ParseFilter(req.Filter)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	viewer := a.contactViewer(orgID, userID)
	query, err := contactquery.Apply(a.DB.Model(&models.Contact{}), registry, viewer, filter)
	if err != nil {
		// A rejected filter is the caller's mistake, and the message names the
		// offending rule so a builder UI can point at it.
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	query = applyContactSearchTerm(query, req.Search)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		a.Log.Error("Failed to count contacts", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to search contacts", nil, "")
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
	if err := query.
		Order(contactSortClause(registry, req)).
		Offset((page - 1) * limit).Limit(limit).
		Find(&contacts).Error; err != nil {
		a.Log.Error("Failed to search contacts", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to search contacts", nil, "")
	}

	items, err := a.buildContactSearchResults(orgID, contacts, req.Include)
	if err != nil {
		a.Log.Error("Failed to load contact page extras", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to search contacts", nil, "")
	}

	return r.SendEnvelope(map[string]any{
		"contacts": items,
		"total":    total,
		"page":     page,
		"limit":    limit,
	})
}

// contactRegistry builds the filter registry for one organization, including
// its custom fields and its pipelines.
//
// It is built per request rather than once at startup because two
// organizations legitimately have different fields and different stages, and a
// shared registry would leak one org's schema into another's filter builder.
func (a *App) contactRegistry(orgID uuid.UUID) (*contactquery.Registry, error) {
	defs, err := customfields.New(a.DB).Definitions(context.Background(), orgID, models.FieldEntityContact)
	if err != nil {
		return nil, err
	}
	registry := contactquery.NewRegistry()
	customfields.RegisterFields(registry, defs)

	// Deal filters (plan 07): "everyone with an open deal in Negotiation" is
	// a campaign audience, so the stages have to be filterable.
	pipelines, err := a.Deals().ListPipelines(context.Background(), orgID, false)
	if err != nil {
		return nil, err
	}
	if len(pipelines) > 0 {
		var stages []models.PipelineStage
		for _, pipeline := range pipelines {
			stages = append(stages, pipeline.Stages...)
		}
		deals.RegisterFields(registry, stages, pipelines)
	}

	// Saved audiences are themselves filterable (plan 05), which is what lets
	// one segment be defined as "everyone in VIP who has an open deal".
	var saved []models.Segment
	if err := a.DB.Where("organization_id = ?", orgID).Find(&saved).Error; err != nil {
		return nil, err
	}
	segments.RegisterField(registry, saved)

	return registry, nil
}

// contactViewer describes who is running a contact query.
func (a *App) contactViewer(orgID, userID uuid.UUID) contactquery.Viewer {
	return contactquery.Viewer{
		OrgID:             orgID,
		UserID:            userID,
		Location:          a.OrgLocation(orgID),
		CanSeeAllContacts: a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID),
	}
}

// applyContactSearchTerm adds the free-text search across name and phone.
func applyContactSearchTerm(query *gorm.DB, term string) *gorm.DB {
	term = strings.TrimSpace(term)
	if term == "" {
		return query
	}
	if len(term) > contactSearchMaxTerm {
		term = term[:contactSearchMaxTerm]
	}

	// Escaped so a typed "%" matches a literal percent rather than everything.
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
	pattern := "%" + escaped + "%"
	return query.Where(
		"contacts.profile_name ILIKE ? OR contacts.phone_number ILIKE ? OR contacts.phone_normalized ILIKE ?",
		pattern, pattern, pattern)
}

// contactSortClause builds the ORDER BY from the request, against the
// registry's whitelist.
//
// Sort keys reach SQL, so an unknown or non-sortable key is ignored rather than
// interpolated. The fallback is the inbox ordering the list has always used.
func contactSortClause(registry *contactquery.Registry, req contactSearchRequest) string {
	const fallback = "contacts.last_message_at DESC NULLS LAST, contacts.created_at DESC"

	clauses := make([]string, 0, len(req.Sort))
	for _, s := range req.Sort {
		column, ok := registry.SortColumn(s.Field)
		if !ok {
			continue
		}
		direction := "ASC"
		if strings.EqualFold(s.Dir, "desc") {
			direction = "DESC"
		}
		clauses = append(clauses, column+" "+direction+" NULLS LAST")
	}
	if len(clauses) == 0 {
		return fallback
	}
	// A stable tiebreaker keeps pagination deterministic when the sort column
	// has duplicates; without it a row can appear on two pages.
	clauses = append(clauses, "contacts.id DESC")
	return strings.Join(clauses, ", ")
}

// ContactSearchResult is one row of the contacts list.
type ContactSearchResult struct {
	ID              string         `json:"id"`
	PhoneNumber     string         `json:"phone_number"`
	ProfileName     string         `json:"profile_name"`
	WhatsAppAccount string         `json:"whatsapp_account"`
	Tags            []string       `json:"tags"`
	AssignedUserID  string         `json:"assigned_user_id,omitempty"`
	Source          string         `json:"source,omitempty"`
	LastMessageAt   *string        `json:"last_message_at,omitempty"`
	LastMessagePrev string         `json:"last_message_preview,omitempty"`
	MarketingOptOut bool           `json:"marketing_opt_out"`
	Fields          map[string]any `json:"fields,omitempty"`
	UnreadCount     int64          `json:"unread_count,omitempty"`
	CreatedAt       string         `json:"created_at"`
}

// buildContactSearchResults assembles the page, loading requested extras in one
// query each rather than per row.
func (a *App) buildContactSearchResults(orgID uuid.UUID, contacts []models.Contact, include []string) ([]ContactSearchResult, error) {
	wants := make(map[string]bool, len(include))
	for _, name := range include {
		wants[name] = true
	}

	ids := make([]uuid.UUID, 0, len(contacts))
	for _, c := range contacts {
		ids = append(ids, c.ID)
	}

	var fieldValues map[uuid.UUID]map[string]any
	if wants["fields"] && len(ids) > 0 {
		var err error
		fieldValues, err = customfields.New(a.DB).ValuesFor(context.Background(), orgID, ids, models.FieldEntityContact)
		if err != nil {
			return nil, err
		}
	}

	var unread map[uuid.UUID]int64
	if wants["unread"] && len(ids) > 0 {
		var err error
		unread, err = a.unreadCountsFor(ids)
		if err != nil {
			return nil, err
		}
	}

	shouldMask := a.ShouldMaskPhoneNumbers(orgID)

	out := make([]ContactSearchResult, 0, len(contacts))
	for _, c := range contacts {
		name, phone := c.ProfileName, c.PhoneNumber
		if shouldMask {
			name, phone = a.MaskContactFields(orgID, c.ProfileName, c.PhoneNumber)
		}

		row := ContactSearchResult{
			ID:              c.ID.String(),
			PhoneNumber:     phone,
			ProfileName:     name,
			WhatsAppAccount: c.WhatsAppAccount,
			Tags:            contactTagStrings(c),
			Source:          c.Source,
			LastMessagePrev: c.LastMessagePreview,
			MarketingOptOut: c.MarketingOptOut,
			CreatedAt:       c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		if c.AssignedUserID != nil {
			row.AssignedUserID = c.AssignedUserID.String()
		}
		if c.LastMessageAt != nil {
			formatted := c.LastMessageAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			row.LastMessageAt = &formatted
		}
		if fieldValues != nil {
			row.Fields = fieldValues[c.ID]
		}
		if unread != nil {
			row.UnreadCount = unread[c.ID]
		}
		out = append(out, row)
	}
	return out, nil
}

// unreadCountsFor returns unread message counts for a page of contacts.
//
// One grouped query rather than one per row: counting per contact is the N+1
// that made the contacts list slow in proportion to the page size.
func (a *App) unreadCountsFor(contactIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	type row struct {
		ContactID uuid.UUID
		Count     int64
	}
	var rows []row

	if err := a.DB.Model(&models.Message{}).
		Select("contact_id, count(*) as count").
		Where("contact_id IN ? AND direction = ? AND status <> ?",
			contactIDs, models.DirectionIncoming, models.MessageStatusRead).
		Group("contact_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		out[r.ContactID] = r.Count
	}
	return out, nil
}

func contactTagStrings(c models.Contact) []string {
	tags := make([]string, 0, len(c.Tags))
	for _, raw := range c.Tags {
		if s, ok := raw.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

// GetContactFilterFields returns the filter registry for the builder UI.
//
// The UI renders from this rather than a hardcoded list, so a field an
// organization adds appears in the builder without a frontend change.
func (a *App) GetContactFilterFields(r *fastglue.Request) error {
	orgID, _, err := a.requireAnyPermission(r,
		perm(models.ResourceContacts, models.ActionRead),
		perm(models.ResourceChat, models.ActionRead))
	if err != nil {
		return err
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		a.Log.Error("Failed to build contact filter registry", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load filter fields", nil, "")
	}

	type fieldInfo struct {
		Key       string                `json:"key"`
		Label     string                `json:"label"`
		Type      string                `json:"type"`
		Operators []string              `json:"operators"`
		Options   []contactquery.Option `json:"options,omitempty"`
		Sortable  bool                  `json:"sortable"`
	}

	fields := registry.Fields()
	out := make([]fieldInfo, 0, len(fields))
	for _, f := range fields {
		out = append(out, fieldInfo{
			Key:       f.Key,
			Label:     f.LabelKey,
			Type:      string(f.Type),
			Operators: contactquery.OperatorsFor(f.Type),
			Options:   f.Options,
			Sortable:  f.Sortable,
		})
	}

	return r.SendEnvelope(map[string]any{
		"fields":   out,
		"sortable": registry.SortableFields(),
	})
}
