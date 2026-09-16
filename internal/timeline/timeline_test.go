package timeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/activity"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/timeline"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *timeline.Service, *models.Organization, *models.Contact) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	return db, timeline.New(db), org, contact
}

// addMessage inserts one message at a specific time.
func addMessage(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, at time.Time, direction models.Direction, content string) {
	t.Helper()
	require.NoError(t, db.Create(&models.Message{
		BaseModel:       models.BaseModel{ID: uuid.New(), CreatedAt: at},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "acct",
		Direction:       direction,
		SenderType:      senderFor(direction),
		MessageType:     models.MessageTypeText,
		Content:         content,
		Status:          models.MessageStatusSent,
	}).Error)
}

func senderFor(d models.Direction) models.SenderType {
	if d == models.DirectionIncoming {
		return models.SenderContact
	}
	return models.SenderAgent
}

func itemsByType(items []timeline.Item) map[string][]timeline.Item {
	out := map[string][]timeline.Item{}
	for _, item := range items {
		out[item.Type] = append(out[item.Type], item)
	}
	return out
}

// A back-and-forth is one event in the story. Thirty rows for thirty replies
// buries everything else that happened.
func TestBuild_CollapsesMessageBursts(t *testing.T) {
	db, svc, org, contact := setup(t)

	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 6; i++ {
		direction := models.DirectionIncoming
		if i%2 == 1 {
			direction = models.DirectionOutgoing
		}
		addMessage(t, db, org.ID, contact.ID, base.Add(time.Duration(i)*time.Minute), direction, "hi")
	}

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)

	bursts := itemsByType(items)[timeline.TypeMessageBurst]
	require.Len(t, bursts, 1, "one exchange is one entry")
	require.NotNil(t, bursts[0].Group)
	assert.Equal(t, 6, bursts[0].Group.Count)
	assert.Equal(t, 3, bursts[0].Group.FromCustomer)
}

// A long pause means a separate exchange; merging across it would imply a
// conversation that never happened.
func TestBuild_SplitsBurstsAcrossALongGap(t *testing.T) {
	db, svc, org, contact := setup(t)

	base := time.Now().UTC().Add(-6 * time.Hour)
	addMessage(t, db, org.ID, contact.ID, base, models.DirectionIncoming, "morning")
	addMessage(t, db, org.ID, contact.ID, base.Add(time.Minute), models.DirectionOutgoing, "hello")

	// Well past the burst gap.
	later := base.Add(3 * time.Hour)
	addMessage(t, db, org.ID, contact.ID, later, models.DirectionIncoming, "afternoon")

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)

	bursts := itemsByType(items)[timeline.TypeMessageBurst]
	assert.Len(t, bursts, 2)
}

func TestBuild_MergesEverySourceInOrder(t *testing.T) {
	db, svc, org, contact := setup(t)
	user := testutil.CreateTestUser(t, db, org.ID)

	base := time.Now().UTC().Add(-3 * time.Hour)

	addMessage(t, db, org.ID, contact.ID, base, models.DirectionIncoming, "hello")

	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID: org.ID, ContactID: contact.ID, Type: "contact.tag_added",
		Actor: crmevents.UserActor(user.ID, "Alice"),
		Data:  map[string]any{"tag": "VIP"}, OccurredAt: base.Add(time.Hour),
	}))

	require.NoError(t, db.Create(&models.ConversationNote{
		BaseModel:      models.BaseModel{ID: uuid.New(), CreatedAt: base.Add(2 * time.Hour)},
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		CreatedByID:    user.ID,
		Content:        "Customer prefers email",
	}).Error)

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(items), 3)

	// Newest first: the note, then the tag, then the message.
	assert.Equal(t, timeline.TypeNote, items[0].Type)
	assert.Equal(t, timeline.TypeTag, items[1].Type)
	assert.Equal(t, timeline.TypeMessageBurst, items[2].Type)

	for i := 1; i < len(items); i++ {
		assert.False(t, items[i].OccurredAt.After(items[i-1].OccurredAt),
			"the feed must be ordered newest first across every source")
	}
}

func TestBuild_ActivityTypesMapToRenderableKinds(t *testing.T) {
	db, svc, org, contact := setup(t)
	now := time.Now().UTC()

	cases := map[string]string{
		"conversation.status_changed": timeline.TypeConversation,
		"conversation.assigned":       timeline.TypeAssignment,
		"transfer.created":            timeline.TypeTransfer,
		"contact.field_changed":       timeline.TypeFieldChange,
		"task.created":                timeline.TypeTask,
	}

	i := 0
	for activityType := range cases {
		require.NoError(t, activity.Record(db, activity.Entry{
			OrgID: org.ID, ContactID: contact.ID, Type: activityType,
			Actor:      crmevents.SystemActor(),
			OccurredAt: now.Add(-time.Duration(i) * time.Minute),
		}))
		i++
	}

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, item := range items {
		seen[item.Type] = true
	}
	for _, want := range cases {
		assert.True(t, seen[want], "%s should appear in the timeline", want)
	}
}

func TestBuild_FiltersByType(t *testing.T) {
	db, svc, org, contact := setup(t)
	now := time.Now().UTC()

	addMessage(t, db, org.ID, contact.ID, now.Add(-time.Hour), models.DirectionIncoming, "hi")
	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID: org.ID, ContactID: contact.ID, Type: "contact.tag_added",
		Actor: crmevents.SystemActor(), Data: map[string]any{"tag": "VIP"},
		OccurredAt: now,
	}))

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{
		Types: []string{timeline.TypeTag},
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, timeline.TypeTag, items[0].Type)
}

func TestBuild_PagesBackwards(t *testing.T) {
	db, svc, org, contact := setup(t)
	now := time.Now().UTC()

	// Four separate exchanges, each well past the burst gap.
	for i := 0; i < 4; i++ {
		addMessage(t, db, org.ID, contact.ID,
			now.Add(-time.Duration(i+1)*2*time.Hour), models.DirectionIncoming, "hi")
	}

	first, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)

	before := first[len(first)-1].OccurredAt
	second, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{Limit: 2, Before: &before})
	require.NoError(t, err)
	require.NotEmpty(t, second)

	for _, item := range second {
		assert.True(t, item.OccurredAt.Before(before), "the next page is strictly older")
	}
}

func TestBuild_IsScopedToOneContact(t *testing.T) {
	db, svc, org, contact := setup(t)
	other := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15557100001"))

	addMessage(t, db, org.ID, other.ID, time.Now().UTC(), models.DirectionIncoming, "not mine")

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestBuild_EmptyContactHasEmptyTimeline(t *testing.T) {
	_, svc, org, contact := setup(t)

	items, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)
	assert.Empty(t, items)
}

// Item ids are stable so the UI can key on them across refetches without the
// list re-animating every poll.
func TestBuild_ItemIDsAreStable(t *testing.T) {
	db, svc, org, contact := setup(t)
	addMessage(t, db, org.ID, contact.ID, time.Now().UTC().Add(-time.Hour),
		models.DirectionIncoming, "hi")

	first, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)
	second, err := svc.Build(ctx(), org.ID, contact.ID, timeline.Opts{})
	require.NoError(t, err)

	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.Equal(t, first[0].ID, second[0].ID)
}
