package transfers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service performs transfer operations with the product's canonical lock
// order (plan 10, S5).
//
// The operations lived inside HTTP handlers, which meant the campaign worker,
// the calling manager and the scheduler could not reach them, and — more
// seriously — they locked rows in a different order from the conversation
// service. The conversation service takes the contact advisory lock first;
// picking from the queue took the transfer row first and then read the
// contact. Two of those running at once on the same contact is a deadlock, and
// under load it was one.
//
// Canonical order, everywhere: contact advisory lock → conversation row →
// transfer row.
type Service struct {
	DB *gorm.DB
}

// New builds a Service. It needs nothing but a database handle, so every
// caller — HTTP, worker, scheduler — can have one.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// ErrNoneQueued is returned by PickNext when the queue is empty.
var ErrNoneQueued = errors.New("transfers: nothing in the queue")

// lockContact serialises everything touching one contact, in the same way and
// with the same key as the conversation service. The two must agree or the
// lock does nothing.
func lockContact(tx *gorm.DB, contactID uuid.UUID) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", contactID.String()).Error
}

// PickInput describes who is picking and from which queues.
type PickInput struct {
	OrgID  uuid.UUID
	UserID uuid.UUID

	// TeamID restricts the pick to one team's queue.
	TeamID *uuid.UUID
	// GeneralOnly restricts the pick to transfers with no team.
	GeneralOnly bool
	// AllowedTeamIDs are the queues this picker may draw from. Empty with
	// Unrestricted false means the general queue only.
	AllowedTeamIDs []uuid.UUID
	// Unrestricted lets a supervisor pick from any queue.
	Unrestricted bool

	// AssignContactOwner pins the picker as the contact's owner when the
	// contact has none. It reflects the organization's AssignToSameAgent
	// setting; the caller resolves that, because settings are not this
	// package's concern.
	AssignContactOwner bool
}

// pickAttempts bounds the retry when a candidate is taken between the
// unlocked read and the locked one. Each attempt skips one contended row, and
// a queue where three consecutive rows are contested is a queue where any
// answer is stale anyway.
const pickAttempts = 3

// PickNext assigns the oldest queued transfer the picker is allowed to take.
//
// It reads a candidate without locking, takes the contact advisory lock, then
// re-reads that row FOR UPDATE and re-checks it is still unassigned. Locking
// the transfer first would be the wrong order; locking the contact first is
// impossible until a candidate names one. Re-validating after the lock is what
// makes the unlocked read safe: the worst case is a wasted round trip.
func (s *Service) PickNext(ctx context.Context, in PickInput) (*models.AgentTransfer, error) {
	var skip []uuid.UUID

	for attempt := 0; attempt < pickAttempts; attempt++ {
		candidate, err := s.nextCandidate(ctx, in, skip)
		if err != nil {
			return nil, err
		}

		var picked *models.AgentTransfer
		err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockContact(tx, candidate.ContactID); err != nil {
				return err
			}

			var row models.AgentTransfer
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND status = ? AND agent_id IS NULL",
					candidate.ID, models.TransferStatusActive).
				First(&row).Error; err != nil {
				// Somebody took it while we were reaching for the lock.
				return err
			}

			now := time.Now().UTC()
			row.AgentID = &in.UserID
			row.SLA.PickedUpAt = &now
			// A transfer nobody initiated was self-picked; recording the
			// picker keeps "who moved this" answerable.
			if row.TransferredByUserID == nil {
				row.TransferredByUserID = &in.UserID
			}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			if err := MirrorToConversation(tx, &row); err != nil {
				return err
			}

			if in.AssignContactOwner {
				// Only when the contact has no owner: the active transfer
				// already grants visibility, and setting it unconditionally
				// would leave the conversation in this agent's list forever
				// after the transfer ends.
				if err := tx.Model(&models.Contact{}).
					Where("id = ? AND assigned_user_id IS NULL", row.ContactID).
					Update("assigned_user_id", in.UserID).Error; err != nil {
					return err
				}
			}

			picked = &row
			return nil
		})

		if err == nil {
			return picked, nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			skip = append(skip, candidate.ID)
			continue
		}
		return nil, err
	}

	return nil, ErrNoneQueued
}

// nextCandidate reads the oldest queued transfer the picker may take, without
// locking anything.
func (s *Service) nextCandidate(ctx context.Context, in PickInput, skip []uuid.UUID) (*models.AgentTransfer, error) {
	q := s.DB.WithContext(ctx).Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND status = ? AND agent_id IS NULL",
			in.OrgID, models.TransferStatusActive).
		Order("transferred_at ASC")

	if len(skip) > 0 {
		q = q.Where("id NOT IN ?", skip)
	}

	switch {
	case in.GeneralOnly:
		q = q.Where("team_id IS NULL")
	case in.TeamID != nil:
		q = q.Where("team_id = ?", *in.TeamID)
	case in.Unrestricted:
		// Any queue.
	case len(in.AllowedTeamIDs) > 0:
		q = q.Where("team_id IS NULL OR team_id IN ?", in.AllowedTeamIDs)
	default:
		q = q.Where("team_id IS NULL")
	}

	var row models.AgentTransfer
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNoneQueued
		}
		return nil, err
	}
	return &row, nil
}

// ReturnToQueue unassigns every active transfer held by one agent.
//
// Called when an agent goes away. The contact's owner is deliberately left
// alone (plan 10, S5): returning a conversation to the queue says nothing
// about who owns the relationship, and clearing it used to break owner-based
// IVR routing for a customer whose agent simply stepped out.
func (s *Service) ReturnToQueue(ctx context.Context, orgID, userID uuid.UUID) ([]models.AgentTransfer, error) {
	var held []models.AgentTransfer
	if err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND agent_id = ? AND status = ?",
			orgID, userID, models.TransferStatusActive).
		Find(&held).Error; err != nil {
		return nil, err
	}

	returned := make([]models.AgentTransfer, 0, len(held))
	for i := range held {
		transfer := held[i]
		err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockContact(tx, transfer.ContactID); err != nil {
				return err
			}
			if err := tx.Model(&models.AgentTransfer{}).
				Where("id = ? AND status = ?", transfer.ID, models.TransferStatusActive).
				Updates(map[string]any{
					"agent_id":     nil,
					"picked_up_at": nil,
				}).Error; err != nil {
				return err
			}
			queued := transfer
			queued.AgentID = nil
			return MirrorToConversation(tx, &queued)
		})
		if err != nil {
			// One stuck contact must not strand the agent's other
			// conversations in a queue nobody is watching.
			continue
		}
		transfer.AgentID = nil
		transfer.SLA.PickedUpAt = nil
		returned = append(returned, transfer)
	}
	return returned, nil
}

// Resume ends an active transfer and hands the conversation back to the bot.
func (s *Service) Resume(ctx context.Context, orgID, transferID, byUserID uuid.UUID) (*models.AgentTransfer, Outcome, error) {
	var out models.AgentTransfer

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transfer models.AgentTransfer
		if err := tx.Where("id = ? AND organization_id = ?", transferID, orgID).
			First(&transfer).Error; err != nil {
			return err
		}

		if err := lockContact(tx, transfer.ContactID); err != nil {
			return err
		}

		// Re-read under the lock: the SLA job may have expired it, or another
		// agent resumed it, between the first read and the lock.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", transferID).First(&transfer).Error; err != nil {
			return err
		}
		if transfer.Status != models.TransferStatusActive {
			out = transfer
			return errNotActive
		}

		now := time.Now().UTC()
		transfer.Status = models.TransferStatusResumed
		transfer.ResumedAt = &now
		transfer.ResumedBy = &byUserID
		if err := tx.Save(&transfer).Error; err != nil {
			return err
		}
		out = transfer
		return MirrorToConversation(tx, &transfer)
	})

	switch {
	case errors.Is(err, errNotActive):
		return &out, AlreadyActive, nil
	case err != nil:
		return nil, Failed, err
	default:
		return &out, Assigned, nil
	}
}

// errNotActive signals a resume of a transfer somebody else already closed. It
// is internal: callers see the AlreadyActive outcome instead.
var errNotActive = errors.New("transfers: transfer is not active")

// Expire closes an active transfer that timed out, under the contact lock.
func (s *Service) Expire(ctx context.Context, orgID, transferID uuid.UUID) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var transfer models.AgentTransfer
		if err := tx.Where("id = ? AND organization_id = ?", transferID, orgID).
			First(&transfer).Error; err != nil {
			return err
		}
		if err := lockContact(tx, transfer.ContactID); err != nil {
			return err
		}
		result := tx.Model(&models.AgentTransfer{}).
			Where("id = ? AND status = ?", transferID, models.TransferStatusActive).
			Update("status", models.TransferStatusExpired)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		transfer.Status = models.TransferStatusExpired
		return MirrorToConversation(tx, &transfer)
	})
}

// HasActive reports whether a contact is already being handled.
func (s *Service) HasActive(ctx context.Context, orgID, contactID uuid.UUID) (bool, error) {
	var count int64
	err := s.DB.WithContext(ctx).Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			orgID, contactID, models.TransferStatusActive).
		Count(&count).Error
	return count > 0, err
}
