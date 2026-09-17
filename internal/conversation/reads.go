package conversation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Per-user read state (plan 10, S5).
//
// A single is_read flag on the contact belonged to whoever opened the chat
// last. A supervisor glancing at the queue cleared an agent's badge, and the
// agent had no way to know a customer was still waiting on them. Read is a
// fact about a person.

// MarkRead records that a user has seen everything up to `at`.
//
// Upsert on the composite key rather than read-then-write: two browser tabs
// belonging to the same person open the same conversation constantly, and the
// read-modify-write race between them produced duplicate-key errors in a code
// path nobody was checking the error of.
func (s *Service) MarkRead(ctx context.Context, conversationID, userID uuid.UUID, at time.Time) error {
	row := models.ConversationRead{
		ConversationID: conversationID,
		UserID:         userID,
		LastReadAt:     at,
		UpdatedAt:      at,
	}
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "conversation_id"}, {Name: "user_id"}},
		// Never move the marker backwards: an older tab finishing its request
		// after a newer one must not re-hide messages the person has read.
		DoUpdates: clause.Assignments(map[string]any{
			"last_read_at": gorm.Expr("GREATEST(conversation_reads.last_read_at, EXCLUDED.last_read_at)"),
			"updated_at":   at,
		}),
	}).Create(&row).Error
}

// UnreadFor counts, per contact, the inbound messages this user has not seen.
//
// One grouped query for the whole page: counting per row is the N+1 that made
// the contacts list slow in proportion to its page size.
//
// A contact with no read row at all counts every inbound message, which is
// what "never opened it" should mean.
func (s *Service) UnreadFor(ctx context.Context, orgID, userID uuid.UUID, contactIDs []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(contactIDs))
	if len(contactIDs) == 0 {
		return out, nil
	}

	type row struct {
		ContactID uuid.UUID
		Count     int64
	}
	var rows []row

	err := s.DB.WithContext(ctx).
		Table("messages m").
		Select("m.contact_id, count(*) AS count").
		Joins(`LEFT JOIN conversations c
		         ON c.contact_id = m.contact_id AND c.organization_id = m.organization_id
		        AND c.status <> ? AND c.deleted_at IS NULL`, models.ConversationResolved).
		Joins(`LEFT JOIN conversation_reads r
		         ON r.conversation_id = c.id AND r.user_id = ?`, userID).
		Where("m.organization_id = ? AND m.contact_id IN ? AND m.direction = ?",
			orgID, contactIDs, models.DirectionIncoming).
		Where("m.deleted_at IS NULL").
		Where("r.last_read_at IS NULL OR m.created_at > r.last_read_at").
		Group("m.contact_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		out[r.ContactID] = r.Count
	}
	return out, nil
}

// ReadPosition returns when a user last read a conversation, if ever.
func (s *Service) ReadPosition(ctx context.Context, conversationID, userID uuid.UUID) (*time.Time, error) {
	var row models.ConversationRead
	err := s.DB.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row.LastReadAt, nil
}

// MaySendReadReceipts reports whether this viewer opening the conversation
// should tell WhatsApp the customer's messages were read.
//
// Only the person actually dealing with it may: a supervisor skimming the
// queue, or anyone in peek mode, would otherwise show the customer two blue
// ticks that nobody has acted on. An unassigned conversation has no such
// person, so anyone who could pick it up counts.
func MaySendReadReceipts(c *models.Conversation, viewerID uuid.UUID, viewerCanWrite bool) bool {
	if c == nil {
		return viewerCanWrite
	}
	if c.AssigneeID != nil {
		return *c.AssigneeID == viewerID
	}
	return viewerCanWrite
}
