package crmevents_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

func testLogger() logf.Logger {
	return logf.New(logf.Opts{Level: logf.ErrorLevel})
}

// recordingSink captures the events it is handed, and can be told to fail.
type recordingSink struct {
	name   string
	events []crmevents.Event
	err    error
}

func (s *recordingSink) Name() string { return s.name }

func (s *recordingSink) Handle(_ context.Context, e crmevents.Event) error {
	s.events = append(s.events, e)
	return s.err
}

// freshOutbox empties the outbox for one test. SetupTestDB truncates once per
// package rather than per test, and the relay drains every organization by
// design, so leftover rows from an earlier test would otherwise be picked up.
func freshOutbox(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("TRUNCATE TABLE crm_event_outbox").Error)
}

func newRelay(db *gorm.DB, sinks ...crmevents.Sink) *crmevents.Relay {
	return &crmevents.Relay{DB: db, Log: testLogger(), Sinks: sinks, BatchSize: 100}
}

func contactEvent(orgID, contactID uuid.UUID) crmevents.Event {
	return crmevents.New(orgID, "contact.created", crmevents.SystemActor(), map[string]any{
		"contact_phone": "+15550001111",
	}).ForContact(contactID)
}

// --- PublishTx ---

// The whole point of the outbox: the event must commit with the change that
// caused it, and disappear with it on rollback.
func TestPublishTx_RollsBackWithTheChange(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	sentinel := errors.New("business rule failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Contact{}).Where("id = ?", contact.ID).
			Update("profile_name", "Renamed").Error; err != nil {
			return err
		}
		if err := crmevents.PublishTx(tx, contactEvent(org.ID, contact.ID)); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	var events int64
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ?", org.ID).Count(&events).Error)
	assert.Zero(t, events, "event must not survive a rolled-back transaction")

	var reloaded models.Contact
	require.NoError(t, db.First(&reloaded, "id = ?", contact.ID).Error)
	assert.NotEqual(t, "Renamed", reloaded.ProfileName, "the change itself must have rolled back too")
}

func TestPublishTx_CommitsWithTheChange(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return crmevents.PublishTx(tx, contactEvent(org.ID, contact.ID))
	}))

	var row models.CRMEventOutbox
	require.NoError(t, db.Where("organization_id = ?", org.ID).First(&row).Error)
	assert.Equal(t, "contact.created", row.Type)
	assert.Equal(t, crmevents.ActorSystem, row.ActorType)
	require.NotNil(t, row.ContactID)
	assert.Equal(t, contact.ID, *row.ContactID)
	assert.Equal(t, crmevents.SubjectContact, row.SubjectType)
	assert.Nil(t, row.PublishedAt, "a fresh event is unpublished")
	assert.Equal(t, "+15550001111", row.Data["contact_phone"])
}

func TestPublishTx_RejectsUnknownEventType(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	err := crmevents.PublishTx(db, crmevents.New(org.ID, "contact.teleported", crmevents.SystemActor(), nil))
	require.ErrorIs(t, err, crmevents.ErrUnknownEventType)

	var count int64
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.Zero(t, count)
}

func TestPublishTx_RejectsMissingOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)

	err := crmevents.PublishTx(db, crmevents.New(uuid.Nil, "contact.created", crmevents.SystemActor(), nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no organization")
}

// --- Relay ---

func TestRelay_FansOutAndMarksPublished(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, crmevents.PublishTx(db, contactEvent(org.ID, contact.ID)))

	sink := &recordingSink{name: "test"}
	n, err := newRelay(db, sink).ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	require.Len(t, sink.events, 1)
	got := sink.events[0]
	assert.Equal(t, "contact.created", got.Type)
	assert.Equal(t, org.ID, got.OrgID)
	require.NotNil(t, got.ContactID)
	assert.Equal(t, contact.ID, *got.ContactID)
	assert.Equal(t, "+15550001111", got.Data["contact_phone"])

	var row models.CRMEventOutbox
	require.NoError(t, db.First(&row, "id = ?", got.ID).Error)
	assert.NotNil(t, row.PublishedAt, "relayed events are marked published")
}

// A second pass must not redeliver what the first already published —
// otherwise every webhook subscriber would see duplicates forever.
func TestRelay_DoesNotRedeliverPublishedEvents(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, crmevents.PublishTx(db, contactEvent(org.ID, contact.ID)))

	sink := &recordingSink{name: "test"}
	relay := newRelay(db, sink)

	n, err := relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	n, err = relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Len(t, sink.events, 1, "the event must be delivered exactly once")
}

// One broken endpoint must not wedge the outbox: the row is still published,
// and the reason is recorded on it.
func TestRelay_SinkFailureDoesNotStallTheOutbox(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, crmevents.PublishTx(db, contactEvent(org.ID, contact.ID)))

	failing := &recordingSink{name: "broken", err: errors.New("endpoint down")}
	healthy := &recordingSink{name: "healthy"}

	n, err := newRelay(db, failing, healthy).ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.Len(t, healthy.events, 1, "a failing sink must not stop later sinks")

	var row models.CRMEventOutbox
	require.NoError(t, db.Where("organization_id = ?", org.ID).First(&row).Error)
	assert.NotNil(t, row.PublishedAt, "a failed sink still marks the row published")
	assert.Equal(t, 1, row.Attempts)
	assert.Contains(t, row.LastError, "broken: endpoint down")
}

func TestRelay_ProcessesInOccurredAtOrder(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	for i, offset := range []time.Duration{-2 * time.Hour, -time.Hour, 0} {
		e := contactEvent(org.ID, contact.ID)
		e.OccurredAt = now.Add(offset)
		e.Data = map[string]any{"seq": float64(i)}
		require.NoError(t, crmevents.PublishTx(db, e))
	}

	sink := &recordingSink{name: "test"}
	n, err := newRelay(db, sink).ProcessBatch(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)

	require.Len(t, sink.events, 3)
	for i, e := range sink.events {
		assert.Equal(t, float64(i), e.Data["seq"], "events relay oldest first")
	}
}

func TestRelay_BatchSizeCapsOnePass(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	for i := 0; i < 5; i++ {
		require.NoError(t, crmevents.PublishTx(db, contactEvent(org.ID, contact.ID)))
	}

	sink := &recordingSink{name: "test"}
	relay := &crmevents.Relay{DB: db, Log: testLogger(), Sinks: []crmevents.Sink{sink}, BatchSize: 2}

	n, err := relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	n, err = relay.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

// --- Prune ---

func TestPrune_RemovesOnlyExpiredPublishedRows(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	// One old published, one recent published, one still unpublished.
	old := contactEvent(org.ID, contact.ID)
	recent := contactEvent(org.ID, contact.ID)
	pending := contactEvent(org.ID, contact.ID)
	for _, e := range []crmevents.Event{old, recent, pending} {
		require.NoError(t, crmevents.PublishTx(db, e))
	}

	longAgo := time.Now().UTC().Add(-30 * 24 * time.Hour)
	yesterday := time.Now().UTC().Add(-24 * time.Hour)
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).Where("id = ?", old.ID).
		Update("published_at", longAgo).Error)
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).Where("id = ?", recent.ID).
		Update("published_at", yesterday).Error)

	newRelay(db).Prune(context.Background())

	var remaining []models.CRMEventOutbox
	require.NoError(t, db.Where("organization_id = ?", org.ID).Find(&remaining).Error)

	ids := map[uuid.UUID]bool{}
	for _, r := range remaining {
		ids[r.ID] = true
	}
	assert.False(t, ids[old.ID], "published rows past retention are pruned")
	assert.True(t, ids[recent.ID], "recently published rows are kept")
	assert.True(t, ids[pending.ID], "unpublished rows are never pruned")
}

// --- Catalog ---

func TestCatalog_OnlyExposesEventsThatCanFire(t *testing.T) {
	events := crmevents.WebhookEventTypes()

	// Every webhook-exposed type must be a real, known event.
	for _, e := range events {
		assert.True(t, crmevents.IsKnown(e), "%s is offered to webhooks but not in the catalog", e)
	}

	assert.Contains(t, events, "contact.created")
	assert.Contains(t, events, "message.incoming")
	assert.Contains(t, events, "transfer.created")
	// Plans 03, 04, 06 and 07 shipped, so their events are now subscribable.
	assert.Contains(t, events, "conversation.status_changed")
	assert.Contains(t, events, "task.created")
	assert.Contains(t, events, "contact.merged")
	assert.Contains(t, events, "deal.created")
	assert.Contains(t, events, "deal.stage_changed")
	assert.Contains(t, events, "deal.won")
}

func TestCatalog_AutomatableIsASubsetOfKnown(t *testing.T) {
	for _, e := range crmevents.AutomatableEventTypes() {
		spec, ok := crmevents.Lookup(e)
		require.True(t, ok)
		assert.True(t, spec.Automatable)
	}
}

// --- Concurrency ---

// concurrentSink records deliveries from several relays at once.
type concurrentSink struct {
	mu       sync.Mutex
	eventIDs []uuid.UUID
}

func (s *concurrentSink) Name() string { return "concurrent" }

func (s *concurrentSink) Handle(_ context.Context, e crmevents.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventIDs = append(s.eventIDs, e.ID)
	return nil
}

// Running a relay on every replica is only safe because rows are claimed with
// FOR UPDATE SKIP LOCKED. If that guarantee broke, every webhook subscriber
// would receive duplicates, so it is worth asserting rather than assuming.
func TestRelay_ConcurrentRelaysDeliverEachEventExactlyOnce(t *testing.T) {
	db := testutil.SetupTestDB(t)
	freshOutbox(t, db)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	const total = 40
	for i := 0; i < total; i++ {
		require.NoError(t, crmevents.PublishTx(db, contactEvent(org.ID, contact.ID)))
	}

	sink := &concurrentSink{}

	// Small batches force the two relays to interleave over many passes.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			relay := &crmevents.Relay{
				DB: db, Log: testLogger(), Sinks: []crmevents.Sink{sink}, BatchSize: 3,
			}
			for {
				n, err := relay.ProcessBatch(context.Background())
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	seen := map[uuid.UUID]int{}
	for _, id := range sink.eventIDs {
		seen[id]++
	}
	for id, count := range seen {
		assert.Equal(t, 1, count, "event %s was delivered %d times", id, count)
	}
	assert.Len(t, seen, total, "every event must be delivered exactly once")

	var unpublished int64
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("published_at IS NULL").Count(&unpublished).Error)
	assert.Zero(t, unpublished, "the outbox must be fully drained")
}
