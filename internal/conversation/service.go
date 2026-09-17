// Package conversation owns the lifecycle of a customer conversation
// (plan 03).
//
// Before this there was no conversation record: a contact was "read" or not,
// and agent_transfers stood in for assignment. That meant no way to say a
// conversation was finished, no response-time measurement, and no workload
// model — an agent could not ask "what is mine and still open?".
//
// Every transition goes through this service so the state machine exists in one
// place. Each one takes a per-contact advisory lock first (plan 10, S5): two
// inbound webhooks for the same contact arrive in separate goroutines, and
// without serialising them both would find no conversation and both create one.
package conversation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// DefaultReopenWindow is how long after resolving a customer message reopens
// the same conversation rather than starting a new one.
const DefaultReopenWindow = 24 * time.Hour

// Settings are the per-organization inbox rules.
type Settings struct {
	// ReopenWindow: a customer message within this long after resolution
	// reopens the same conversation; later messages start a new one.
	ReopenWindow time.Duration

	// AutoPendingOnAgentReply moves Open to Pending when an agent replies.
	AutoPendingOnAgentReply bool

	// AutoResolveIdle closes a conversation nothing has happened in for this
	// long. Zero disables it.
	//
	// This is deliberately independent of the SLA feature (plan 10, S5).
	// Inactivity closing used to run only when SLA was switched on, so an
	// organization that did not want response-time alerting also got an inbox
	// that never finished anything — every conversation ever opened stayed in
	// the list.
	AutoResolveIdle time.Duration

	// PendingTimeout closes a conversation waiting on the customer for this
	// long. Zero disables it.
	PendingTimeout time.Duration
}

// DefaultSettings returns the built-in inbox rules.
func DefaultSettings() Settings {
	return Settings{ReopenWindow: DefaultReopenWindow}
}

// Service performs conversation transitions.
type Service struct {
	DB  *gorm.DB
	Set Settings

	// SettingsFor resolves the rules for one organization. It exists because
	// the service is constructed once per request without an organization in
	// hand, while the rules are per-organization. Nil falls back to Set.
	SettingsFor func(orgID uuid.UUID) Settings
}

// settings returns the rules for one organization.
func (s *Service) settings(orgID uuid.UUID) Settings {
	if s.SettingsFor != nil {
		return s.SettingsFor(orgID)
	}
	return s.Set
}

// New builds a Service with default settings.
func New(db *gorm.DB) *Service {
	return &Service{DB: db, Set: DefaultSettings()}
}

// ErrNotFound is returned when no active conversation exists.
var ErrNotFound = errors.New("conversation: no active conversation")

// lockContact serialises everything touching one contact's conversation.
//
// A transaction-scoped advisory lock rather than a row lock: the first inbound
// message has no row to lock yet, which is exactly the race that produces two
// conversations for one contact.
func lockContact(tx *gorm.DB, contactID uuid.UUID) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, contactID.String()).Error
}

// Active returns the contact's live conversation, if any.
func (s *Service) Active(ctx context.Context, orgID, contactID uuid.UUID) (*models.Conversation, error) {
	var c models.Conversation
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND contact_id = ? AND status <> ?",
			orgID, contactID, models.ConversationResolved).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// TouchInbound records a customer message, opening or reopening the
// conversation as needed.
//
// This is the entry point for every inbound path — messages, reactions,
// call-permission replies — so a conversation exists whenever a customer has
// been in touch, however they reached us.
func (s *Service) TouchInbound(ctx context.Context, orgID, contactID uuid.UUID, account string, at time.Time, botActive bool) (*models.Conversation, error) {
	handling := models.HandlingNone
	if botActive {
		handling = models.HandlingBot
	}
	return s.touchInbound(ctx, orgID, contactID, account, at, handling)
}

// touchInbound is TouchInbound in terms of the handling state (plan 10, S5).
func (s *Service) touchInbound(ctx context.Context, orgID, contactID uuid.UUID, account string, at time.Time, handling models.ConversationHandling) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}

		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}

		if existing != nil {
			updates := map[string]any{
				"status":                   models.ConversationOpen,
				"last_customer_message_at": at,
				"last_message_at":          at,
				"message_count":            gorm.Expr("message_count + 1"),
			}
			// waiting_since marks the oldest unanswered customer message, so
			// it is only set when nothing is already waiting; overwriting it
			// would reset the clock every time the customer chased us.
			if existing.WaitingSince == nil {
				updates["waiting_since"] = at
			}
			if existing.FirstCustomerMessageAt == nil {
				updates["first_customer_message_at"] = at
			}
			if existing.Status == models.ConversationSnoozed {
				// A customer writing in ends a snooze: the whole point of
				// snoozing is "not now", and they have made it now.
				updates["snoozed_until"] = nil
				updates["snoozed_by_id"] = nil
			}
			if account != "" {
				updates["whatsapp_account"] = account
			}

			if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
				Updates(updates).Error; err != nil {
				return err
			}
			if existing.Status != models.ConversationOpen {
				s.publish(tx, orgID, contactID, existing.ID, "conversation.status_changed", map[string]any{
					"from": string(existing.Status), "to": string(models.ConversationOpen),
				})
			}
			result, err = s.byID(tx, existing.ID)
			return err
		}

		// Nothing active. A recently resolved conversation is reopened rather
		// than replaced, so a customer replying to the same issue stays in one
		// thread instead of fragmenting the history.
		if reopened, err := s.reopenRecent(tx, orgID, contactID, at, account); err != nil {
			return err
		} else if reopened != nil {
			result = reopened
			return nil
		}

		created, err := s.create(tx, orgID, contactID, account, at, handling)
		if err != nil {
			return err
		}
		result = created
		return nil
	})

	return result, err
}

// reopenRecent reopens a conversation resolved inside the reopen window.
func (s *Service) reopenRecent(tx *gorm.DB, orgID, contactID uuid.UUID, at time.Time, account string) (*models.Conversation, error) {
	window := s.settings(orgID).ReopenWindow
	if window <= 0 {
		window = DefaultReopenWindow
	}

	var recent models.Conversation
	err := tx.Where("organization_id = ? AND contact_id = ? AND status = ? AND resolved_at > ?",
		orgID, contactID, models.ConversationResolved, at.Add(-window)).
		Order("resolved_at DESC").First(&recent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	updates := map[string]any{
		"status":                   models.ConversationOpen,
		"resolved_at":              nil,
		"resolved_by_id":           nil,
		"resolution_reason":        "",
		"reopened_count":           gorm.Expr("reopened_count + 1"),
		"last_customer_message_at": at,
		"last_message_at":          at,
		"waiting_since":            at,
		"message_count":            gorm.Expr("message_count + 1"),
	}
	if account != "" {
		updates["whatsapp_account"] = account
	}
	if err := tx.Model(&models.Conversation{}).Where("id = ?", recent.ID).Updates(updates).Error; err != nil {
		return nil, err
	}

	s.publish(tx, orgID, contactID, recent.ID, "conversation.status_changed", map[string]any{
		"from": string(models.ConversationResolved), "to": string(models.ConversationOpen),
		"reopened": true,
	})
	return s.byID(tx, recent.ID)
}

func (s *Service) create(tx *gorm.DB, orgID, contactID uuid.UUID, account string, at time.Time, handling models.ConversationHandling) (*models.Conversation, error) {
	c := &models.Conversation{
		BaseModel:              models.BaseModel{ID: uuid.New()},
		OrganizationID:         orgID,
		ContactID:              contactID,
		Status:                 models.ConversationOpen,
		Handling:               handling,
		WhatsAppAccount:        account,
		OpenedAt:               at,
		FirstCustomerMessageAt: &at,
		LastCustomerMessageAt:  &at,
		LastMessageAt:          &at,
		WaitingSince:           &at,
		MessageCount:           1,
	}
	if err := tx.Create(c).Error; err != nil {
		return nil, err
	}

	s.publish(tx, orgID, contactID, c.ID, "conversation.created", map[string]any{
		"status": string(models.ConversationOpen),
	})
	return c, nil
}

// RecordOutbound records a message we sent.
//
// Only a human agent reply sets first_response_at and clears waiting_since: a
// bot greeting or an automated template is not someone answering the customer,
// and counting it would make response times describe automation rather than
// service.
func (s *Service) RecordOutbound(ctx context.Context, orgID, contactID uuid.UUID, sender models.SenderType, senderID *uuid.UUID, at time.Time) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}

		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if errors.Is(err, ErrNotFound) {
			// An agent reaching out first legitimately starts a conversation;
			// a campaign blast does not, or every send would open one.
			if sender != models.SenderAgent {
				return nil
			}
			// An agent opening the conversation is handling it themselves.
			created, createErr := s.create(tx, orgID, contactID, "", at, models.HandlingHuman)
			if createErr != nil {
				return createErr
			}
			existing = created
		} else if err != nil {
			return err
		}

		updates := map[string]any{
			"last_message_at": at,
			"message_count":   gorm.Expr("message_count + 1"),
		}

		if sender.CountsAsAgentReply() {
			updates["last_agent_message_at"] = at
			updates["waiting_since"] = nil
			if existing.FirstResponseAt == nil {
				updates["first_response_at"] = at
				updates["first_responder_id"] = senderID
			}
			if s.Set.AutoPendingOnAgentReply && existing.Status == models.ConversationOpen {
				updates["status"] = models.ConversationPending
			}
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return err
		}
		result, err = s.byID(tx, existing.ID)
		return err
	})

	return result, err
}

// Resolve closes a conversation.
func (s *Service) Resolve(ctx context.Context, orgID, contactID uuid.UUID, reason string, actor crmevents.Actor) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}

		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		updates := map[string]any{
			"status":            models.ConversationResolved,
			"resolved_at":       now,
			"resolution_reason": reason,
			"waiting_since":     nil,
			// A resolved conversation is no longer snoozed; leaving the
			// timestamp would wake it up again after it was finished.
			"snoozed_until": nil,
			// Nobody is handling something that is finished.
			"handling": models.HandlingNone,
		}
		if actor.Type == crmevents.ActorUser {
			updates["resolved_by_id"] = actor.ID
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return err
		}

		// Resolving ends the chatbot session too (plan 10, S5).
		//
		// A session left active is a half-asked question: the flow is parked
		// on a prompt node, so the customer's next message — a new enquiry,
		// days later — is read as the answer to it and swallowed. The
		// conversation is finished; the script running inside it is finished
		// with it.
		if err := endActiveSession(tx, orgID, contactID, now); err != nil {
			return err
		}

		s.publishAs(tx, orgID, contactID, existing.ID, "conversation.status_changed", actor, map[string]any{
			"from": string(existing.Status), "to": string(models.ConversationResolved),
			"reason": reason,
		})
		result, err = s.byID(tx, existing.ID)
		return err
	})

	return result, err
}

// Snooze hides a conversation until a time, or until the customer writes.
func (s *Service) Snooze(ctx context.Context, orgID, contactID uuid.UUID, until time.Time, actor crmevents.Actor) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}
		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if err != nil {
			return err
		}

		updates := map[string]any{
			"status":        models.ConversationSnoozed,
			"snoozed_until": until,
			"snoozed_by_id": actor.ID,
		}
		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return err
		}

		s.publishAs(tx, orgID, contactID, existing.ID, "conversation.status_changed", actor, map[string]any{
			"from": string(existing.Status), "to": string(models.ConversationSnoozed),
			"until": until.UTC(),
		})
		result, err = s.byID(tx, existing.ID)
		return err
	})

	return result, err
}

// Assign sets who is handling a conversation.
//
// It never touches contacts.assigned_user_id: the owner is the relationship
// manager and the assignee is whoever is handling this conversation now
// (plan 10, S5).
func (s *Service) Assign(ctx context.Context, orgID, contactID uuid.UUID, assignee *uuid.UUID, teamID *uuid.UUID, actor crmevents.Actor) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}
		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if err != nil {
			return err
		}

		updates := map[string]any{"assignee_id": assignee}
		if teamID != nil {
			updates["team_id"] = teamID
		}
		// A human taking the conversation means the bot is no longer driving.
		if assignee != nil {
			updates["handling"] = models.HandlingHuman
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return err
		}

		data := map[string]any{}
		if assignee != nil {
			data["assignee_id"] = assignee.String()
		}
		s.publishAs(tx, orgID, contactID, existing.ID, "conversation.assigned", actor, data)

		result, err = s.byID(tx, existing.ID)
		return err
	})

	return result, err
}

// activeForUpdate loads the live conversation with a row lock.
func (s *Service) activeForUpdate(tx *gorm.DB, orgID, contactID uuid.UUID) (*models.Conversation, error) {
	var c models.Conversation
	err := tx.Set("gorm:query_option", "FOR UPDATE").
		Where("organization_id = ? AND contact_id = ? AND status <> ?",
			orgID, contactID, models.ConversationResolved).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) byID(tx *gorm.DB, id uuid.UUID) (*models.Conversation, error) {
	var c models.Conversation
	if err := tx.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) publish(tx *gorm.DB, orgID, contactID, conversationID uuid.UUID, eventType string, data map[string]any) {
	s.publishAs(tx, orgID, contactID, conversationID, eventType, crmevents.SystemActor(), data)
}

// publishAs records the event in the caller's transaction, so the state change
// and the event it announces commit together.
func (s *Service) publishAs(tx *gorm.DB, orgID, contactID, conversationID uuid.UUID, eventType string, actor crmevents.Actor, data map[string]any) {
	if !crmevents.IsKnown(eventType) {
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["conversation_id"] = conversationID.String()

	event := crmevents.New(orgID, eventType, actor, data).
		ForContact(contactID).
		About(crmevents.SubjectConversation, conversationID)

	_ = crmevents.PublishTx(tx, event)
}

// SetHandling records who is dealing with the conversation (plan 10, S5).
//
// It is deliberately a small, separate operation rather than part of Assign:
// handing to a team queue, a handoff suppressed out of hours and the bot taking
// the conversation back all change who is handling it without changing the
// assignee. Those are the cases the boolean it replaced could not express.
//
// A conversation that does not exist is not an error. Transfers can be created
// for a contact whose conversation was resolved a moment earlier, and refusing
// the transfer over the bookkeeping would be the wrong trade.
func (s *Service) SetHandling(ctx context.Context, orgID, contactID uuid.UUID, handling models.ConversationHandling) error {
	err := s.DB.WithContext(ctx).Model(&models.Conversation{}).
		Where("organization_id = ? AND contact_id = ? AND status <> ?",
			orgID, contactID, models.ConversationResolved).
		Update("handling", handling).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// endActiveSession cancels the chatbot session a contact is in the middle of.
//
// It lives here rather than in the chatbot package because it must happen in
// the same transaction as the resolve: a conversation that closed while its
// session survived is exactly the state that swallows the next message.
func endActiveSession(tx *gorm.DB, orgID, contactID uuid.UUID, at time.Time) error {
	return tx.Model(&models.ChatbotSession{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			orgID, contactID, models.SessionStatusActive).
		Updates(map[string]any{
			"status":       models.SessionStatusCompleted,
			"completed_at": at,
		}).Error
}

// IdleResult reports what a sweep closed.
type IdleResult struct {
	Idle    int
	Pending int
}

// SweepIdle closes conversations nothing is happening in (plan 10, S5).
//
// Two different kinds of stale:
//   - AutoResolveIdle: no message either way for this long. The exchange
//     petered out, which is how most support conversations actually end.
//   - PendingTimeout: we replied and the customer never came back.
//
// Both run without the SLA feature being enabled. Tying inactivity closing to
// SLA meant an organization that did not want response-time alerting got an
// inbox that never finished anything, and agents learned to ignore the count.
func (s *Service) SweepIdle(ctx context.Context, orgID uuid.UUID, now time.Time) (IdleResult, error) {
	set := s.settings(orgID)
	var out IdleResult

	if set.AutoResolveIdle > 0 {
		n, err := s.resolveStale(ctx, orgID,
			[]models.ConversationStatus{models.ConversationOpen, models.ConversationPending},
			now.Add(-set.AutoResolveIdle), models.ResolutionClientInactivity)
		if err != nil {
			return out, err
		}
		out.Idle = n
	}

	if set.PendingTimeout > 0 {
		n, err := s.resolveStale(ctx, orgID,
			[]models.ConversationStatus{models.ConversationPending},
			now.Add(-set.PendingTimeout), models.ResolutionPendingTimeout)
		if err != nil {
			return out, err
		}
		out.Pending = n
	}

	return out, nil
}

// resolveStale closes every conversation in the given statuses whose last
// message predates the cutoff.
//
// Each one goes through Resolve rather than a bulk UPDATE, so the events, the
// chatbot session ending and the per-contact lock all apply. A sweep that took
// a shortcut here would produce conversations that are resolved in the table
// and still running everywhere else.
func (s *Service) resolveStale(ctx context.Context, orgID uuid.UUID, statuses []models.ConversationStatus, cutoff time.Time, reason string) (int, error) {
	const batch = 200

	var rows []models.Conversation
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND status IN ?", orgID, statuses).
		// A snoozed conversation has an explicit wake-up time; it is waiting
		// on purpose, not going stale.
		Where("snoozed_until IS NULL").
		Where("coalesce(last_message_at, opened_at) < ?", cutoff).
		Limit(batch).Find(&rows).Error
	if err != nil {
		return 0, err
	}

	closed := 0
	for _, row := range rows {
		if _, err := s.Resolve(ctx, orgID, row.ContactID, reason, crmevents.SystemActor()); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return closed, err
		}
		closed++
	}
	return closed, nil
}

// SetPending records that we have replied and are waiting on the customer
// (plan 03).
//
// Open and Pending are the difference between "this needs me" and "this needs
// them". Without the distinction an agent's inbox counts every conversation
// they are waiting on as work, so the number never goes down and stops meaning
// anything.
func (s *Service) SetPending(ctx context.Context, orgID, contactID uuid.UUID, actor crmevents.Actor) (*models.Conversation, error) {
	return s.setStatus(ctx, orgID, contactID, models.ConversationPending, actor, func(c *models.Conversation) map[string]any {
		// Waiting on them, not on us.
		return map[string]any{"waiting_since": nil}
	})
}

// Reopen puts a resolved conversation back in the inbox.
//
// Resolving one by mistake is a click. Without a way back the agent had to wait
// for the customer to write again, or start a conversation that looked like a
// new issue when it was the same one.
func (s *Service) Reopen(ctx context.Context, orgID, conversationID uuid.UUID, actor crmevents.Actor) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		conv, err := s.byID(tx, conversationID)
		if err != nil {
			return err
		}
		if conv.OrganizationID != orgID {
			return ErrNotFound
		}
		if err := lockContact(tx, conv.ContactID); err != nil {
			return err
		}

		// One active conversation per contact is the invariant the unique
		// index enforces; reopening into a contact who already has a live one
		// would violate it, and the live one is the right place for the reply.
		if existing, err := s.activeForUpdate(tx, orgID, conv.ContactID); err == nil && existing != nil {
			result = existing
			return nil
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", conv.ID).
			Updates(map[string]any{
				"status":            models.ConversationOpen,
				"resolved_at":       nil,
				"resolved_by_id":    nil,
				"resolution_reason": "",
				"snoozed_until":     nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.Conversation{}).Where("id = ?", conv.ID).
			UpdateColumn("reopened_count", gorm.Expr("reopened_count + 1")).Error; err != nil {
			return err
		}

		s.publishAs(tx, orgID, conv.ContactID, conv.ID, "conversation.status_changed", actor, map[string]any{
			"from": string(models.ConversationResolved), "to": string(models.ConversationOpen),
			"reason": "reopened",
		})

		result, err = s.byID(tx, conv.ID)
		return err
	})

	return result, err
}

// setStatus moves the active conversation to a status, applying any extra
// column changes the transition implies.
func (s *Service) setStatus(
	ctx context.Context,
	orgID, contactID uuid.UUID,
	status models.ConversationStatus,
	actor crmevents.Actor,
	extra func(*models.Conversation) map[string]any,
) (*models.Conversation, error) {
	var result *models.Conversation

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockContact(tx, contactID); err != nil {
			return err
		}

		existing, err := s.activeForUpdate(tx, orgID, contactID)
		if err != nil {
			return err
		}
		if existing.Status == status {
			result = existing
			return nil
		}

		updates := map[string]any{"status": status}
		if extra != nil {
			for key, value := range extra(existing) {
				updates[key] = value
			}
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
			return err
		}

		s.publishAs(tx, orgID, contactID, existing.ID, "conversation.status_changed", actor, map[string]any{
			"from": string(existing.Status), "to": string(status),
		})

		result, err = s.byID(tx, existing.ID)
		return err
	})

	return result, err
}
