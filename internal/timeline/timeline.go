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
	"github.com/shridarpatil/whatomate/internal/crmevents"
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
	// TypeCampaignSend is a bulk message this contact received (plan 02).
	// Read straight from the recipients table rather than copied into the
	// activity log: a campaign to forty thousand people would otherwise write
	// forty thousand activity rows to be read one contact at a time.
	TypeCampaignSend = "campaign_send"
	// TypeChatbotSession is a run of a chatbot flow.
	TypeChatbotSession = "chatbot_session"
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

	// From and To bound the range a page is drawn from (plan 02).
	//
	// They are separate from Before, which is the paging cursor: a filtered
	// range still pages, and folding the two together would make "older than
	// this item" and "within this month" the same argument. Both are
	// inclusive, because they arrive from a date picker where the end date is
	// a day the viewer expects to see.
	From *time.Time
	To   *time.Time

	// HideActivity drops activity entries whose crmevents type is listed.
	//
	// This is how the viewer's permissions reach the timeline (plan 02): an
	// agent who cannot open the Deals board should not read a contact's deal
	// history here either, and gating only the contact would make the timeline
	// a way around every other permission in the product.
	HideActivity []string
}

// bound applies the cursor and the date range to one source's query.
//
// Each source keeps its own time column — messages by created_at, campaign
// sends by sent_at — so the column is a parameter rather than a constant. Doing
// it in one place is what keeps a new source from quietly ignoring the filter,
// which is how the range would end up applying to four sources out of six.
func (o Opts) bound(q *gorm.DB, column string) *gorm.DB {
	if o.Before != nil {
		q = q.Where(column+" < ?", *o.Before)
	}
	if o.From != nil {
		q = q.Where(column+" >= ?", *o.From)
	}
	if o.To != nil {
		q = q.Where(column+" <= ?", *o.To)
	}
	return q
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
	if include(TypeCampaignSend) {
		sends, err := s.campaignSends(ctx, orgID, contactID, opts, limit)
		if err != nil {
			return nil, err
		}
		items = append(items, sends...)
	}
	if include(TypeChatbotSession) {
		sessions, err := s.chatbotSessions(ctx, orgID, contactID, opts, limit)
		if err != nil {
			return nil, err
		}
		items = append(items, sessions...)
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
	q = opts.bound(q, "created_at")

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
				// The message this burst is anchored on, so clicking it can
				// open the chat around that exchange rather than at the
				// newest message (plan 02).
				"message_id": newest.ID,
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
	q = opts.bound(q, "created_at")

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
	q = opts.bound(q, "created_at")

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
	q = opts.bound(q, "occurred_at")
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
			Data:       withSubject(row),
		}
		if row.ActorID != nil {
			item.Actor.ID = row.ActorID.String()
		}
		out = append(out, item)
	}
	return out, nil
}

// withSubject copies an activity's data and names what it was about.
//
// The record an entry refers to — the task that was created, the deal that
// moved — is in a column the item never carried, so the timeline could say
// "Deal moved to Proposal" and offer no way to open that deal. The copy
// matters: adding keys to row.Data would mutate the map the row was decoded
// into, and that map is shared with anything else reading the same row.
func withSubject(row models.ContactActivity) map[string]any {
	data := make(map[string]any, len(row.Data)+2)
	for key, value := range row.Data {
		data[key] = value
	}
	if row.SubjectID != nil {
		data["subject_id"] = row.SubjectID.String()
		data["subject_type"] = row.SubjectType
	}
	return data
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
		// Name the actor by what it is rather than calling everything
		// "System": a customer reading "changed by System" when the chatbot
		// did it learns nothing (plan 02).
		switch row.ActorType {
		case crmevents.ActorBot:
			who = "the chatbot"
		case crmevents.ActorAutomation:
			who = "an automation"
		case crmevents.ActorContact:
			who = "the customer"
		case crmevents.ActorAPI:
			who = "the API"
		default:
			who = "System"
		}
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
	case "custom_action.executed":
		// The name is carried on the event rather than looked up: an action
		// deleted since would otherwise render as a blank line in a history
		// that is meant to say what was done.
		if name, ok := row.Data["action_name"]; ok && name != "" {
			return fmt.Sprintf("%v run by %s", name, who)
		}
		return fmt.Sprintf("Custom action run by %s", who)
	case "contact.lifecycle_stage_changed":
		if to, ok := row.Data["to"]; ok {
			return fmt.Sprintf("Lifecycle stage set to %v by %s", to, who)
		}
		return fmt.Sprintf("Lifecycle stage changed by %s", who)
	case "contact.opt_out_changed":
		if optedOut, ok := row.Data["opted_out"].(bool); ok && optedOut {
			return "Opted out of marketing"
		}
		return "Opted back in to marketing"
	case "campaign.replied":
		return "Replied to a campaign"
	case "chatbot.flow_completed":
		if name, ok := row.Data["flow_name"]; ok && name != "" {
			return fmt.Sprintf("Completed the %v flow", name)
		}
		return "Completed a chatbot flow"
	case "call.missed":
		return "Call missed"
	case "call.completed":
		return "Call completed"
	case "transfer.expired":
		return "Transfer expired before anyone picked it up"
	case "note.created":
		return fmt.Sprintf("Note added by %s", who)
	case "task.updated":
		return fmt.Sprintf("Task %q changed by %s", row.Data["title"], who)
	case "deal.updated":
		return fmt.Sprintf("Deal %q changed by %s", row.Data["title"], who)
	case "deal.deleted":
		return fmt.Sprintf("Deal %q deleted by %s", row.Data["title"], who)
	case "transfer.assigned":
		if agent, ok := row.Data["agent_name"]; ok && agent != "" {
			return fmt.Sprintf("Picked up by %v", agent)
		}
		return "Picked up by an agent"
	case "call.transfer_no_answer":
		return "Call transfer went unanswered"
	case "conversation.sla_breached":
		return "Response deadline passed"
	case "conversation.sla_escalated":
		return "Escalated after going unanswered"
	}
	return row.Type
}

// campaignSends reads the bulk messages this contact received.
//
// Read directly from the recipients table rather than copied into the activity
// log (plan 02): a campaign to forty thousand people would otherwise write
// forty thousand activity rows, each of which is only ever read one contact at
// a time. The recipients row is already the record.
func (s *Service) campaignSends(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	type row struct {
		ID           uuid.UUID
		CampaignID   uuid.UUID
		CampaignName string
		Status       string
		SentAt       time.Time
	}

	q := s.DB.WithContext(ctx).
		Table("bulk_message_recipients r").
		Select("r.id, r.campaign_id, c.name AS campaign_name, r.status, r.sent_at").
		Joins("JOIN bulk_message_campaigns c ON c.id = r.campaign_id").
		Where("c.organization_id = ? AND r.contact_id = ? AND r.sent_at IS NOT NULL", orgID, contactID).
		Where("r.deleted_at IS NULL")
	q = opts.bound(q, "r.sent_at")

	var rows []row
	if err := q.Order("r.sent_at DESC").Limit(limit).Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(rows))
	for _, send := range rows {
		out = append(out, Item{
			ID:         "campaign_send:" + send.ID.String(),
			Type:       TypeCampaignSend,
			OccurredAt: send.SentAt,
			Actor:      Actor{Type: "system"},
			Summary:    "Campaign: " + send.CampaignName,
			Data: map[string]any{
				"campaign_id":   send.CampaignID.String(),
				"campaign_name": send.CampaignName,
				"status":        send.Status,
			},
		})
	}
	return out, nil
}

// chatbotSessions reads the flow runs this contact went through.
//
// A session says what the automated part of the conversation did, which is
// otherwise invisible: the messages are in the thread, but "the qualification
// flow ran and finished" is the thing somebody reading the history wants.
func (s *Service) chatbotSessions(ctx context.Context, orgID, contactID uuid.UUID, opts Opts, limit int) ([]Item, error) {
	q := s.DB.WithContext(ctx).Model(&models.ChatbotSession{}).
		Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	q = opts.bound(q, "started_at")

	var rows []models.ChatbotSession
	if err := q.Order("started_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]Item, 0, len(rows))
	for _, session := range rows {
		data := map[string]any{"status": string(session.Status)}
		if session.CurrentFlowID != nil {
			data["flow_id"] = session.CurrentFlowID.String()
		}
		out = append(out, Item{
			ID:         "chatbot_session:" + session.ID.String(),
			Type:       TypeChatbotSession,
			OccurredAt: session.StartedAt,
			Actor:      Actor{Type: "bot"},
			Summary:    chatbotSessionSummary(session.Status),
			Data:       data,
		})
	}
	return out, nil
}

// chatbotSessionSummary describes a session in the words a person reading a
// history would use.
func chatbotSessionSummary(status models.SessionStatus) string {
	switch status {
	case models.SessionStatusCompleted:
		return "Chatbot flow completed"
	case models.SessionStatusCancelled:
		return "Chatbot flow ended early"
	case models.SessionStatusTimeout:
		return "Chatbot flow timed out"
	default:
		return "Chatbot flow started"
	}
}
