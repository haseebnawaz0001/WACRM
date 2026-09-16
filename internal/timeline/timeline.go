// Package timeline assembles one contact's history from every source that
// records something about them (plan 02).
//
// The information already existed, scattered: messages in one table, calls in
// another, notes in a third, state changes in contact_activities. Nobody could
// see the story of a customer without opening four screens and mentally sorting
// by time. This merges them into one ordered feed.
//
// High-volume sources are read directly rather than copied into the activity
// log. Duplicating every message would double the write cost of a conversation
// for a view that reads them once.
package timeline

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Item types.
const (
	TypeMessageBurst = "message_burst"
	TypeCall         = "call"
	TypeNote         = "note"
	TypeActivity     = "activity"
	TypeTask         = "task"
	TypeFieldChange  = "field_change"
	TypeTag          = "tag"
	TypeConversation = "conversation_status"
	TypeAssignment   = "assignment"
	TypeTransfer     = "transfer"
	TypeLifecycle    = "lifecycle_stage"
)

// BurstGap is how long a pause has to be before messages stop counting as one
// exchange. A back-and-forth is one event in the story; thirty separate rows
// for thirty replies buries everything else.
const BurstGap = 30 * time.Minute

// DefaultLimit is the page size.
const DefaultLimit = 50

// MaxLimit caps one page.
const MaxLimit = 200

// Actor is who caused an item.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Group describes a collapsed run of messages.
type Group struct {
	Count        int       `json:"count"`
	FromCustomer int       `json:"from_customer"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
}

// Item is one entry in the feed.
type Item struct {
	// ID is "<source>:<uuid>", stable so the UI can key on it across refetches.
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	OccurredAt time.Time      `json:"occurred_at"`
	Actor      Actor          `json:"actor"`
	Summary    string         `json:"summary"`
	Data       map[string]any `json:"data,omitempty"`
	Group      *Group         `json:"group,omitempty"`
}

// Opts bounds a timeline query.
type Opts struct {
	// Before returns only items older than this instant, for paging back.
	Before *time.Time
	Limit  int
	// Types restricts to certain item types; empty means everything.
	Types []string

	// HideActivity drops activity entries whose crmevents type is listed.
	//
	// This is how the viewer's permissions reach the timeline (plan 02): an
	// agent who cannot open the Deals board should not read a contact's deal
	// history here either, and gating only the contact would make the timeline
	// a way around every other permission in the product.
	HideActivity []string
}

// Service builds timelines.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Build returns one page of a contact's history, newest first.
//
// Each source is queried for its own page and the results merged, rather than
// unioned in SQL. The sources have genuinely different shapes, and a UNION over
// four tables with different columns is both harder to read and harder for the
// planner than four indexed reads.
func (s *Service) Build(ctx context.Context, orgID, contactID uuid.UUID, opts Opts) ([]Item, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	want := make(map[string]bool, len(opts.Types))
	for _, t := range opts.Types {
		want[t] = true
	}
	include := func(itemType string) bool {
		return len(want) == 0 || want[itemType]
	}

	var items []Item

	if include(TypeMessageBurst) {
		bursts, err := s.messageBursts(ctx, orgID, contactID, opts, limit)
		if err != nil {
			return nil, err
		}
		items = append(items, bursts...)
	}
	if include(TypeCall) {
		calls, err := s.calls(ctx, orgID, contactID, opts, limit)
		if err != nil {
			return nil, err
		}
		items = append(items, calls...)
	}
	if include(TypeNote) {
		notes, err := s.notes(ctx, orgID, contactID, opts, limit)
		if err != nil {
			return nil, err
		}
		items = append(items, notes...)
	}

	activities, err := s.activities(ctx, orgID, contactID, opts, limit)
	if err != nil {
		return nil, err
	}
	for _, item := range activities {
		if include(item.Type) {
			items = append(items, item)
		}
	}

	// Merge: newest first, with the id as a tiebreaker so a page boundary
	// between two items at the same instant is stable across requests.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})

	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// messageBursts collapses runs of messages into single entries.
func (s *Service) messageBursts(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	q := s.DB.WithContext(ctx).Model(&models.Message{}).
		Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	if opts.Before != nil {
		q = q.Where("created_at < ?", *opts.Before)
	}

	// Read more rows than the page needs: several messages collapse into one
	// item, so a page of items spans many messages.
	var rows []models.Message
	if err := q.Order("created_at DESC").Limit(limit * 20).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	var out []Item
	start := 0
	for i := 1; i <= len(rows); i++ {
		// rows are newest first, so the gap is previous minus current.
		endOfRun := i == len(rows) ||
			rows[i-1].CreatedAt.Sub(rows[i].CreatedAt) > BurstGap

		if !endOfRun {
			continue
		}

		run := rows[start:i]
		newest, oldest := run[0], run[len(run)-1]

		fromCustomer := 0
		for _, m := range run {
			if m.Direction == models.DirectionIncoming {
				fromCustomer++
			}
		}

		out = append(out, Item{
			ID:         "message:" + newest.ID.String(),
			Type:       TypeMessageBurst,
			OccurredAt: newest.CreatedAt,
			Actor:      Actor{Type: actorForMessage(newest)},
			Summary:    burstSummary(len(run), fromCustomer),
			Data: map[string]any{
				"preview":          newest.Content,
				"whatsapp_account": newest.WhatsAppAccount,
				"conversation_id":  newest.ConversationID,
			},
			Group: &Group{
				Count: len(run), FromCustomer: fromCustomer,
				From: oldest.CreatedAt, To: newest.CreatedAt,
			},
		})
		start = i
	}

	return out, nil
}

func actorForMessage(m models.Message) string {
	if m.Direction == models.DirectionIncoming {
		return "contact"
	}
	if m.SenderType != "" {
		return string(m.SenderType)
	}
	return "system"
}

func burstSummary(total, fromCustomer int) string {
	if total == 1 {
		if fromCustomer == 1 {
			return "1 message from the customer"
		}
		return "1 message sent"
	}
	return fmt.Sprintf("%d messages · %d from the customer", total, fromCustomer)
}

// calls reads the call log directly.
func (s *Service) calls(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	q := s.DB.WithContext(ctx).Model(&models.CallLog{}).
		Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	if opts.Before != nil {
		q = q.Where("created_at < ?", *opts.Before)
	}

	var rows []models.CallLog
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(rows))
	for _, call := range rows {
		out = append(out, Item{
			ID:         "call:" + call.ID.String(),
			Type:       TypeCall,
			OccurredAt: call.CreatedAt,
			Actor:      Actor{Type: "system"},
			Summary:    fmt.Sprintf("%s call (%s)", call.Direction, call.Status),
			Data: map[string]any{
				"direction": call.Direction,
				"status":    call.Status,
				"duration":  call.Duration,
			},
		})
	}
	return out, nil
}

// notes reads internal notes.
func (s *Service) notes(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	q := s.DB.WithContext(ctx).Model(&models.ConversationNote{}).
		Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	if opts.Before != nil {
		q = q.Where("created_at < ?", *opts.Before)
	}

	var rows []models.ConversationNote
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(rows))
	for _, note := range rows {
		out = append(out, Item{
			ID:         "note:" + note.ID.String(),
			Type:       TypeNote,
			OccurredAt: note.CreatedAt,
			Actor:      Actor{Type: "user", ID: note.CreatedByID.String()},
			Summary:    "Internal note",
			Data:       map[string]any{"content": note.Content},
		})
	}
	return out, nil
}

// activities reads the recorded state changes and maps them to item types.
func (s *Service) activities(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	q := s.DB.WithContext(ctx).Model(&models.ContactActivity{}).
		Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	if opts.Before != nil {
		q = q.Where("occurred_at < ?", *opts.Before)
	}
	// Excluded in SQL rather than after the read, so a contact whose history is
	// mostly deal activity still fills a page for a viewer who cannot see deals.
	if len(opts.HideActivity) > 0 {
		q = q.Where("type NOT IN ?", opts.HideActivity)
	}

	var rows []models.ContactActivity
	if err := q.Order("occurred_at DESC").Limit(limit * 2).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		item := Item{
			ID:         "activity:" + row.ID.String(),
			Type:       itemTypeForActivity(row.Type),
			OccurredAt: row.OccurredAt,
			Actor:      Actor{Type: row.ActorType, Name: row.ActorName},
			Summary:    summaryForActivity(row),
			Data:       row.Data,
		}
		if row.ActorID != nil {
			item.Actor.ID = row.ActorID.String()
		}
		out = append(out, item)
	}
	return out, nil
}

// itemTypeForActivity groups activity types into what the UI renders.
func itemTypeForActivity(activityType string) string {
	switch activityType {
	case "conversation.created", "conversation.status_changed":
		return TypeConversation
	case "conversation.assigned", "contact.assigned":
		return TypeAssignment
	case "transfer.created", "transfer.assigned", "transfer.resumed", "transfer.expired":
		return TypeTransfer
	case "contact.tag_added", "contact.tag_removed":
		return TypeTag
	case "contact.field_changed":
		return TypeFieldChange
	case "contact.lifecycle_stage_changed":
		return TypeLifecycle
	case "task.created", "task.completed", "task.cancelled", "task.overdue":
		return TypeTask
	}
	return TypeActivity
}

// summaryForActivity renders the English fallback. The UI prefers its own
// translation keyed by type and data; this is what an API consumer and the
// export see.
func summaryForActivity(row models.ContactActivity) string {
	who := row.ActorName
	if who == "" {
		who = "System"
	}

	switch row.Type {
	case "contact.created":
		return "Contact created"
	case "contact.deleted":
		return "Contact deleted"
	case "contact.restored":
		return "Contact restored"
	case "contact.tag_added":
		return fmt.Sprintf("Tag %v added by %s", row.Data["tag"], who)
	case "contact.tag_removed":
		return fmt.Sprintf("Tag %v removed by %s", row.Data["tag"], who)
	case "contact.field_changed":
		return fmt.Sprintf("%v changed by %s", row.Data["field"], who)
	case "conversation.created":
		return "Conversation opened"
	case "conversation.status_changed":
		return fmt.Sprintf("Conversation %v by %s", row.Data["to"], who)
	case "conversation.assigned":
		return fmt.Sprintf("Conversation assigned by %s", who)
	case "task.created":
		return fmt.Sprintf("Task %q created by %s", row.Data["title"], who)
	case "task.completed":
		return fmt.Sprintf("Task %q completed by %s", row.Data["title"], who)
	case "task.overdue":
		return fmt.Sprintf("Task %q is overdue", row.Data["title"])
	case "task.cancelled":
		return fmt.Sprintf("Task %q cancelled by %s", row.Data["title"], who)
	case "contact.assigned":
		if name, ok := row.Data["assignee"]; ok {
			return fmt.Sprintf("Contact assigned to %v by %s", name, who)
		}
		return fmt.Sprintf("Contact assigned by %s", who)
	case "contact.merged":
		return fmt.Sprintf("Contact merged by %s", who)
	case "deal.created":
		return fmt.Sprintf("Deal %q created by %s", row.Data["title"], who)
	case "deal.stage_changed":
		// The stage name is carried on the event: resolving an id here would
		// mean a query per timeline row, and a deleted stage would render as
		// a blank.
		if to, ok := row.Data["to_stage"]; ok {
			return fmt.Sprintf("Deal moved to %v by %s", to, who)
		}
		return fmt.Sprintf("Deal moved to a new stage by %s", who)
	case "deal.won":
		return fmt.Sprintf("Deal won by %s", who)
	case "deal.lost":
		if reason, ok := row.Data["reason"]; ok && reason != "" {
			return fmt.Sprintf("Deal lost by %s — %v", who, reason)
		}
		return fmt.Sprintf("Deal lost by %s", who)
	case "transfer.created":
		return "Transferred to an agent"
	case "transfer.resumed":
		return "Handed back to the bot"
	}
	return row.Type
}
