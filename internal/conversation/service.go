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
}

// DefaultSettings returns the built-in inbox rules.
func DefaultSettings() Settings {
	return Settings{ReopenWindow: DefaultReopenWindow}
}

// Service performs conversation transitions.
type Service struct {
	DB  *gorm.DB
	Set Settings
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

		created, err := s.create(tx, orgID, contactID, account, at, botActive)
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
	window := s.Set.ReopenWindow
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

func (s *Service) create(tx *gorm.DB, orgID, contactID uuid.UUID, account string, at time.Time, botActive bool) (*models.Conversation, error) {
	c := &models.Conversation{
		BaseModel:              models.BaseModel{ID: uuid.New()},
		OrganizationID:         orgID,
		ContactID:              contactID,
		Status:                 models.ConversationOpen,
		BotActive:              botActive,
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
			created, createErr := s.create(tx, orgID, contactID, "", at, false)
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
		}
		if actor.Type == crmevents.ActorUser {
			updates["resolved_by_id"] = actor.ID
		}

		if err := tx.Model(&models.Conversation{}).Where("id = ?", existing.ID).
			Updates(updates).Error; err != nil {
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
			updates["bot_active"] = false
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
