// Package activity reads and writes a contact's history (plan 00, F3).
//
// The timeline needs one ordered story per contact: tags added, ownership
// changed, a transfer opened, a field edited. Those changes previously left
// either no trace at all (tag edits and assignment emitted nothing) or a row in
// audit_logs that could not be attributed to a contact. This package records
// them in contact_activities, written in the same transaction as the change so
// the history cannot drift from the data.
//
// High-volume records are deliberately not copied here. Messages, call logs,
// notes and campaign sends already have their own tables with timestamps, and
// duplicating them would double the write cost of every conversation; the
// timeline reads those directly and merges.
package activity

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Entry describes one activity to record.
type Entry struct {
	OrgID     uuid.UUID
	ContactID uuid.UUID
	Type      string
	Actor     crmevents.Actor
	Subject   crmevents.Subject
	Data      map[string]any
	// OccurredAt defaults to now when zero.
	OccurredAt time.Time
}

// Record writes one activity row using the caller's transaction.
//
// Pass the transaction performing the change so the history commits with it.
func Record(tx *gorm.DB, e Entry) error {
	return tx.Create(rowFor(e)).Error
}

// RecordAll writes several activity rows in one insert. Tag renames and bulk
// actions touch many contacts at once, and a row-per-contact round trip makes
// those operations quadratic.
func RecordAll(tx *gorm.DB, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	rows := make([]*models.ContactActivity, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, rowFor(e))
	}
	return tx.CreateInBatches(rows, 500).Error
}

// FromEvent records the activity for a domain event, when its catalog entry
// asks for one. This is the bridge the outbox relay's activity sink uses.
func FromEvent(tx *gorm.DB, e crmevents.Event) error {
	spec, ok := crmevents.Lookup(e.Type)
	if !ok || !spec.RecordActivity || e.ContactID == nil {
		return nil
	}
	return Record(tx, Entry{
		OrgID:      e.OrgID,
		ContactID:  *e.ContactID,
		Type:       e.Type,
		Actor:      e.Actor,
		Subject:    e.Subject,
		Data:       e.Data,
		OccurredAt: e.OccurredAt,
	})
}

func rowFor(e Entry) *models.ContactActivity {
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	data := models.JSONB(e.Data)
	if data == nil {
		data = models.JSONB{}
	}
	actorType := e.Actor.Type
	if actorType == "" {
		actorType = crmevents.ActorSystem
	}
	return &models.ContactActivity{
		ID:             uuid.New(),
		OrganizationID: e.OrgID,
		ContactID:      e.ContactID,
		Type:           e.Type,
		ActorType:      actorType,
		ActorID:        e.Actor.ID,
		ActorName:      e.Actor.Name,
		SubjectType:    e.Subject.Type,
		SubjectID:      e.Subject.ID,
		Data:           data,
		OccurredAt:     occurred,
	}
}

// Cursor is a keyset position in a contact's timeline.
//
// Offset pagination is wrong here: activity is appended constantly, so an
// offset shifts under the reader and rows get skipped or repeated while they
// scroll. The cursor is (occurred_at, id) because timestamps collide — several
// activities can share a millisecond — and id breaks the tie deterministically.
type Cursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

// ListOpts filters a timeline query.
type ListOpts struct {
	// Types restricts the query to these activity types. Empty means all.
	Types []string
	// Before returns only activity older than this cursor.
	Before *Cursor
	// Limit caps the page size.
	Limit int
}

// DefaultLimit is the page size when none is given.
const DefaultLimit = 50

// MaxLimit caps how much of a timeline one request can pull.
const MaxLimit = 200

// List returns one page of a contact's activity, newest first, along with the
// cursor for the next page (nil when the timeline is exhausted).
func List(db *gorm.DB, orgID, contactID uuid.UUID, opts ListOpts) ([]models.ContactActivity, *Cursor, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	q := db.Where("organization_id = ? AND contact_id = ?", orgID, contactID)
	if len(opts.Types) > 0 {
		q = q.Where("type IN ?", opts.Types)
	}
	if opts.Before != nil {
		// Row-value comparison keeps the keyset strictly ordered and lets
		// the timeline index serve it directly.
		q = q.Where("(occurred_at, id) < (?, ?)", opts.Before.OccurredAt, opts.Before.ID)
	}

	// Fetch one extra row to find out whether another page exists without a
	// second count query.
	var rows []models.ContactActivity
	if err := q.Order("occurred_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, err
	}

	if len(rows) <= limit {
		return rows, nil, nil
	}

	rows = rows[:limit]
	last := rows[len(rows)-1]
	return rows, &Cursor{OccurredAt: last.OccurredAt, ID: last.ID}, nil
}

// Recorder adapts this package to the relay's activity sink.
type Recorder struct{}

// Handle records the activity for one event.
func (Recorder) Handle(tx *gorm.DB, e crmevents.Event) error {
	return FromEvent(tx, e)
}
