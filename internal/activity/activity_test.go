package activity_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/activity"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

func seedActivity(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, at time.Time, typ string) {
	t.Helper()
	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID:      orgID,
		ContactID:  contactID,
		Type:       typ,
		Actor:      crmevents.SystemActor(),
		Data:       map[string]any{"tag": "VIP"},
		OccurredAt: at,
	}))
}

func TestRecord_StoresTheEntry(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID:     org.ID,
		ContactID: contact.ID,
		Type:      "contact.tag_added",
		Actor:     crmevents.UserActor(user.ID, "Alice"),
		Subject:   crmevents.Subject{Type: crmevents.SubjectTag},
		Data:      map[string]any{"tag": "VIP"},
	}))

	var row models.ContactActivity
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&row).Error)

	assert.Equal(t, "contact.tag_added", row.Type)
	assert.Equal(t, crmevents.ActorUser, row.ActorType)
	require.NotNil(t, row.ActorID)
	assert.Equal(t, user.ID, *row.ActorID)
	assert.Equal(t, "Alice", row.ActorName)
	assert.Equal(t, "VIP", row.Data["tag"])
	assert.False(t, row.OccurredAt.IsZero(), "an unset time defaults to now")
}

// History must commit with the change that produced it, or the timeline will
// show things that did not happen.
func TestRecord_RollsBackWithTheTransaction(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	_ = db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, activity.Record(tx, activity.Entry{
			OrgID: org.ID, ContactID: contact.ID, Type: "contact.tag_added",
		}))
		return assert.AnError
	})

	var count int64
	require.NoError(t, db.Model(&models.ContactActivity{}).
		Where("contact_id = ?", contact.ID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestList_ReturnsNewestFirst(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	base := time.Now().UTC().Truncate(time.Microsecond)
	seedActivity(t, db, org.ID, contact.ID, base.Add(-2*time.Hour), "contact.tag_added")
	seedActivity(t, db, org.ID, contact.ID, base.Add(-time.Hour), "contact.assigned")
	seedActivity(t, db, org.ID, contact.ID, base, "contact.tag_removed")

	rows, next, err := activity.List(db, org.ID, contact.ID, activity.ListOpts{})
	require.NoError(t, err)
	require.Len(t, rows, 3)

	assert.Equal(t, "contact.tag_removed", rows[0].Type)
	assert.Equal(t, "contact.assigned", rows[1].Type)
	assert.Equal(t, "contact.tag_added", rows[2].Type)
	assert.Nil(t, next, "a complete timeline has no next cursor")
}

// Offset pagination would skip or repeat rows as new activity arrives while
// someone scrolls. The keyset must be stable against concurrent writes.
func TestList_KeysetPaginationIsStableWhenNewActivityArrives(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	base := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 6; i++ {
		seedActivity(t, db, org.ID, contact.ID, base.Add(-time.Duration(i)*time.Minute), "contact.tag_added")
	}

	first, cursor, err := activity.List(db, org.ID, contact.ID, activity.ListOpts{Limit: 3})
	require.NoError(t, err)
	require.Len(t, first, 3)
	require.NotNil(t, cursor)

	// Something new lands at the top while the reader is paging.
	seedActivity(t, db, org.ID, contact.ID, base.Add(time.Minute), "contact.assigned")

	second, _, err := activity.List(db, org.ID, contact.ID, activity.ListOpts{Limit: 3, Before: cursor})
	require.NoError(t, err)
	require.Len(t, second, 3)

	seen := map[uuid.UUID]bool{}
	for _, r := range append(first, second...) {
		assert.False(t, seen[r.ID], "keyset paging must not repeat a row")
		seen[r.ID] = true
	}
	assert.Len(t, seen, 6)
}

// Timestamps collide, so the cursor needs the id as a tiebreaker or rows at the
// same instant are silently dropped between pages.
func TestList_HandlesIdenticalTimestamps(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	same := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 5; i++ {
		seedActivity(t, db, org.ID, contact.ID, same, "contact.tag_added")
	}

	var all []models.ContactActivity
	opts := activity.ListOpts{Limit: 2}
	for {
		page, next, err := activity.List(db, org.ID, contact.ID, opts)
		require.NoError(t, err)
		all = append(all, page...)
		if next == nil {
			break
		}
		opts.Before = next
	}

	assert.Len(t, all, 5, "every row at the same instant must be reachable")
}

func TestList_FiltersByType(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	base := time.Now().UTC()
	seedActivity(t, db, org.ID, contact.ID, base, "contact.tag_added")
	seedActivity(t, db, org.ID, contact.ID, base.Add(-time.Minute), "contact.assigned")

	rows, _, err := activity.List(db, org.ID, contact.ID,
		activity.ListOpts{Types: []string{"contact.assigned"}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "contact.assigned", rows[0].Type)
}

func TestList_IsScopedToOneContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	mine := testutil.CreateTestContact(t, db, org.ID)
	other := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15557770001"))

	seedActivity(t, db, org.ID, mine.ID, time.Now().UTC(), "contact.tag_added")
	seedActivity(t, db, org.ID, other.ID, time.Now().UTC(), "contact.tag_added")

	rows, _, err := activity.List(db, org.ID, mine.ID, activity.ListOpts{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, mine.ID, rows[0].ContactID)
}

// --- Relay integration ---

// Only events the catalog flags for activity land on the timeline; message
// traffic must not, or every conversation would double its write cost.
func TestRelay_RecordsActivityOnlyForFlaggedEvents(t *testing.T) {
	db := testutil.SetupTestDB(t)
	require.NoError(t, db.Exec("TRUNCATE TABLE crm_event_outbox").Error)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	// contact.created records activity; message.incoming does not.
	require.NoError(t, crmevents.PublishTx(db, crmevents.New(org.ID, "contact.created",
		crmevents.SystemActor(), map[string]any{"contact_phone": "+15550001111"}).ForContact(contact.ID)))
	require.NoError(t, crmevents.PublishTx(db, crmevents.New(org.ID, "message.incoming",
		crmevents.SystemActor(), map[string]any{"content": "hi"}).ForContact(contact.ID)))

	relay := &crmevents.Relay{
		DB:  db,
		Log: logf.New(logf.Opts{Level: logf.ErrorLevel}),
		Sinks: []crmevents.Sink{
			&crmevents.ActivitySink{DB: db, Recorder: activity.Recorder{}},
		},
		BatchSize: 100,
	}
	n, err := relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n)

	rows, _, err := activity.List(db, org.ID, contact.ID, activity.ListOpts{})
	require.NoError(t, err)
	require.Len(t, rows, 1, "only the flagged event is recorded")
	assert.Equal(t, "contact.created", rows[0].Type)
}
