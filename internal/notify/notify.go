// Package notify sends in-app notifications (plan 00, F5).
//
// Notifying an agent used to mean firing a toast from a WebSocket handler.
// Nothing was stored, so anything that arrived while the agent was on another
// screen, or simply away, vanished — there was no way to catch up on what had
// happened. This package persists notifications first and signals afterwards,
// so the record survives whether or not anyone was watching.
//
// Send is deliberately the only entry point. Email and push are out of scope
// today, and keeping one door means adding them later does not require finding
// every notification site again.
package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Entity identifies what a notification is about.
type Entity struct {
	Type string
	ID   *uuid.UUID
}

// Input describes one notification to deliver to one or more users.
type Input struct {
	OrgID   uuid.UUID
	UserIDs []uuid.UUID
	Type    string
	Title   string
	Body    string
	Link    string
	Entity  Entity
	Data    map[string]any
}

// Signaller is told about each stored notification so it can reach connected
// clients. The notification is already durable by then, so a failure here costs
// the live update, never the record.
type Signaller interface {
	NotificationCreated(n *models.Notification)
}

// Service stores and signals notifications.
type Service struct {
	DB *gorm.DB
	// Signal is optional; without it notifications are stored but not pushed.
	Signal Signaller
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Send stores a notification for every listed user who wants it, then signals
// each one.
//
// Users who have turned this notification type off are skipped, and duplicate
// user ids are collapsed, so a caller that assembles a recipient list from
// several sources cannot notify the same person twice.
func (s *Service) Send(ctx context.Context, in Input) error {
	recipients := s.eligible(ctx, in)
	if len(recipients) == 0 {
		return nil
	}

	data := models.JSONB(in.Data)
	if data == nil {
		data = models.JSONB{}
	}

	rows := make([]*models.Notification, 0, len(recipients))
	for _, userID := range recipients {
		rows = append(rows, &models.Notification{
			ID:             uuid.New(),
			OrganizationID: in.OrgID,
			UserID:         userID,
			Type:           in.Type,
			Title:          in.Title,
			Body:           in.Body,
			Link:           in.Link,
			EntityType:     in.Entity.Type,
			EntityID:       in.Entity.ID,
			Data:           data,
		})
	}

	if err := s.DB.WithContext(ctx).CreateInBatches(rows, 200).Error; err != nil {
		return err
	}

	if s.Signal != nil {
		for _, row := range rows {
			s.Signal.NotificationCreated(row)
		}
	}
	return nil
}

// eligible resolves the recipient list: deduplicated, and filtered by each
// user's preferences.
func (s *Service) eligible(ctx context.Context, in Input) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(in.UserIDs))
	unique := make([]uuid.UUID, 0, len(in.UserIDs))
	for _, id := range in.UserIDs {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil
	}

	var users []models.User
	if err := s.DB.WithContext(ctx).Select("id, settings").
		Where("id IN ?", unique).Find(&users).Error; err != nil {
		// Preferences are an optimisation, not a gate. If they cannot be
		// read, notifying is better than silently dropping.
		return unique
	}

	prefs := make(map[uuid.UUID]bool, len(users))
	for _, u := range users {
		prefs[u.ID] = WantsInApp(u.Settings, in.Type)
	}

	out := make([]uuid.UUID, 0, len(unique))
	for _, id := range unique {
		if allowed, known := prefs[id]; !known || allowed {
			out = append(out, id)
		}
	}
	return out
}

// WantsInApp reports whether a user wants in-app notifications of this type.
//
// Notifications default to on: a type the user has never expressed an opinion
// about should reach them, since the alternative is silently withholding
// something they are waiting for.
func WantsInApp(settings models.JSONB, notificationType string) bool {
	return prefFlag(settings, notificationType, "in_app")
}

// WantsSound reports whether a user wants a sound for this type.
func WantsSound(settings models.JSONB, notificationType string) bool {
	return prefFlag(settings, notificationType, "sound")
}

func prefFlag(settings models.JSONB, notificationType, flag string) bool {
	if settings == nil {
		return true
	}
	raw, ok := settings["notifications"].(map[string]any)
	if !ok {
		return true
	}
	entry, ok := raw[notificationType].(map[string]any)
	if !ok {
		return true
	}
	value, ok := entry[flag].(bool)
	if !ok {
		return true
	}
	return value
}

// Cursor is a keyset position in a user's notification list.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListOpts filters a notification query.
type ListOpts struct {
	UnreadOnly bool
	Before     *Cursor
	Limit      int
}

// DefaultLimit is the page size when none is given.
const DefaultLimit = 20

// MaxLimit caps one page.
const MaxLimit = 100

// List returns one page of a user's notifications, newest first, with the
// cursor for the next page (nil when exhausted).
func (s *Service) List(ctx context.Context, orgID, userID uuid.UUID, opts ListOpts) ([]models.Notification, *Cursor, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	q := s.DB.WithContext(ctx).
		Where("organization_id = ? AND user_id = ?", orgID, userID)
	if opts.UnreadOnly {
		q = q.Where("read_at IS NULL")
	}
	if opts.Before != nil {
		q = q.Where("(created_at, id) < (?, ?)", opts.Before.CreatedAt, opts.Before.ID)
	}

	var rows []models.Notification
	if err := q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, err
	}

	if len(rows) <= limit {
		return rows, nil, nil
	}
	rows = rows[:limit]
	last := rows[len(rows)-1]
	return rows, &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

// UnreadCount returns how many unread notifications a user has.
func (s *Service) UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	var count int64
	err := s.DB.WithContext(ctx).Model(&models.Notification{}).
		Where("organization_id = ? AND user_id = ? AND read_at IS NULL", orgID, userID).
		Count(&count).Error
	return count, err
}

// MarkRead marks one notification read. Scoping the update by user is what
// stops one user marking another's notifications read.
func (s *Service) MarkRead(ctx context.Context, orgID, userID, notificationID uuid.UUID) error {
	return s.DB.WithContext(ctx).Model(&models.Notification{}).
		Where("id = ? AND organization_id = ? AND user_id = ? AND read_at IS NULL",
			notificationID, orgID, userID).
		Update("read_at", time.Now().UTC()).Error
}

// MarkAllRead marks every unread notification for a user read.
func (s *Service) MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) error {
	return s.DB.WithContext(ctx).Model(&models.Notification{}).
		Where("organization_id = ? AND user_id = ? AND read_at IS NULL", orgID, userID).
		Update("read_at", time.Now().UTC()).Error
}
