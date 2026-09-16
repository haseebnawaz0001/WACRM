package notify_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// recordingSignaller captures what would be pushed to clients.
type recordingSignaller struct {
	sent []*models.Notification
}

func (s *recordingSignaller) NotificationCreated(n *models.Notification) {
	s.sent = append(s.sent, n)
}

func taskInput(orgID uuid.UUID, userIDs ...uuid.UUID) notify.Input {
	return notify.Input{
		OrgID:   orgID,
		UserIDs: userIDs,
		Type:    models.NotificationTaskDue,
		Title:   "Task due",
		Body:    "Follow up with Acme",
		Link:    "/contacts/123?tab=tasks",
		Data:    map[string]any{"task": "follow-up"},
	}
}

func setPrefs(t *testing.T, db *gorm.DB, userID uuid.UUID, prefs map[string]any) {
	t.Helper()
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", userID).
		Update("settings", models.JSONB{"notifications": prefs}).Error)
}

func TestSend_StoresANotificationPerUser(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	first := testutil.CreateTestUser(t, db, org.ID)
	second := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, first.ID, second.ID)))

	var rows []models.Notification
	require.NoError(t, db.Where("organization_id = ?", org.ID).Find(&rows).Error)
	require.Len(t, rows, 2)

	assert.Equal(t, models.NotificationTaskDue, rows[0].Type)
	assert.Equal(t, "Task due", rows[0].Title)
	assert.Equal(t, "/contacts/123?tab=tasks", rows[0].Link)
	assert.Nil(t, rows[0].ReadAt, "a new notification is unread")
}

// Persisting first is the point: a toast fired at someone who was on another
// screen was simply lost, with no way to catch up.
func TestSend_StoresEvenWithNoSignaller(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db) // no Signal wired
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, user.ID)))

	count, err := svc.UnreadCount(context.Background(), org.ID, user.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func TestSend_SignalsEachStoredNotification(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	signal := &recordingSignaller{}
	svc := &notify.Service{DB: db, Signal: signal}
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, user.ID)))

	require.Len(t, signal.sent, 1)
	assert.Equal(t, user.ID, signal.sent[0].UserID)
}

// A recipient list assembled from several sources (owner, assignee, watchers)
// can name the same person twice; they must not be notified twice.
func TestSend_DeduplicatesRecipients(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	require.NoError(t, svc.Send(context.Background(),
		taskInput(org.ID, user.ID, user.ID, user.ID)))

	count, err := svc.UnreadCount(context.Background(), org.ID, user.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func TestSend_SkipsUsersWhoTurnedTheTypeOff(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	optedOut := testutil.CreateTestUser(t, db, org.ID)
	optedIn := testutil.CreateTestUser(t, db, org.ID)

	setPrefs(t, db, optedOut.ID, map[string]any{
		models.NotificationTaskDue: map[string]any{"in_app": false},
	})

	svc := notify.New(db)
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, optedOut.ID, optedIn.ID)))

	out, err := svc.UnreadCount(context.Background(), org.ID, optedOut.ID)
	require.NoError(t, err)
	assert.Zero(t, out)

	in, err := svc.UnreadCount(context.Background(), org.ID, optedIn.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, in)
}

// Turning off one type must not silence the others.
func TestSend_PreferencesArePerType(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	setPrefs(t, db, user.ID, map[string]any{
		models.NotificationTaskDue: map[string]any{"in_app": false},
	})

	svc := notify.New(db)
	in := taskInput(org.ID, user.ID)
	in.Type = models.NotificationSLAEscalation
	require.NoError(t, svc.Send(context.Background(), in))

	count, err := svc.UnreadCount(context.Background(), org.ID, user.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count, "a different type is unaffected")
}

// A type the user has never configured must still reach them; the alternative
// is silently withholding something they are waiting for.
func TestWantsInApp_DefaultsToOn(t *testing.T) {
	assert.True(t, notify.WantsInApp(nil, models.NotificationTaskDue))
	assert.True(t, notify.WantsInApp(models.JSONB{}, models.NotificationTaskDue))
	assert.True(t, notify.WantsInApp(models.JSONB{
		"notifications": map[string]any{"other_type": map[string]any{"in_app": false}},
	}, models.NotificationTaskDue))

	assert.False(t, notify.WantsInApp(models.JSONB{
		"notifications": map[string]any{
			models.NotificationTaskDue: map[string]any{"in_app": false},
		},
	}, models.NotificationTaskDue))
}

func TestWantsSound_IsIndependentOfInApp(t *testing.T) {
	settings := models.JSONB{
		"notifications": map[string]any{
			models.NotificationTaskDue: map[string]any{"in_app": true, "sound": false},
		},
	}
	assert.True(t, notify.WantsInApp(settings, models.NotificationTaskDue))
	assert.False(t, notify.WantsSound(settings, models.NotificationTaskDue))
}

// --- Reading ---

func TestMarkRead_OnlyAffectsTheOwnersNotification(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	other := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, owner.ID)))

	var row models.Notification
	require.NoError(t, db.Where("user_id = ?", owner.ID).First(&row).Error)

	// Another user tries to mark it read.
	require.NoError(t, svc.MarkRead(context.Background(), org.ID, other.ID, row.ID))

	count, err := svc.UnreadCount(context.Background(), org.ID, owner.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count, "another user must not be able to mark it read")

	require.NoError(t, svc.MarkRead(context.Background(), org.ID, owner.ID, row.ID))
	count, err = svc.UnreadCount(context.Background(), org.ID, owner.ID)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestMarkAllRead(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, user.ID)))
	}

	require.NoError(t, svc.MarkAllRead(context.Background(), org.ID, user.ID))

	count, err := svc.UnreadCount(context.Background(), org.ID, user.ID)
	require.NoError(t, err)
	assert.Zero(t, count)
}

func TestList_PagesNewestFirstAndFiltersUnread(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	for i := 0; i < 5; i++ {
		require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, user.ID)))
	}

	page, cursor, err := svc.List(context.Background(), org.ID, user.ID, notify.ListOpts{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.NotNil(t, cursor)

	// Walk the rest with the cursor; every row must appear exactly once.
	seen := map[uuid.UUID]bool{}
	for _, r := range page {
		seen[r.ID] = true
	}
	opts := notify.ListOpts{Limit: 2, Before: cursor}
	for {
		next, c, err := svc.List(context.Background(), org.ID, user.ID, opts)
		require.NoError(t, err)
		for _, r := range next {
			require.False(t, seen[r.ID], "paging must not repeat a notification")
			seen[r.ID] = true
		}
		if c == nil {
			break
		}
		opts.Before = c
	}
	assert.Len(t, seen, 5)

	require.NoError(t, svc.MarkAllRead(context.Background(), org.ID, user.ID))
	unread, _, err := svc.List(context.Background(), org.ID, user.ID, notify.ListOpts{UnreadOnly: true})
	require.NoError(t, err)
	assert.Empty(t, unread)
}

func TestList_IsScopedToTheUser(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	mine := testutil.CreateTestUser(t, db, org.ID)
	other := testutil.CreateTestUser(t, db, org.ID)

	svc := notify.New(db)
	require.NoError(t, svc.Send(context.Background(), taskInput(org.ID, other.ID)))

	rows, _, err := svc.List(context.Background(), org.ID, mine.ID, notify.ListOpts{})
	require.NoError(t, err)
	assert.Empty(t, rows, "a user only ever sees their own notifications")
}

func TestSend_NoRecipientsIsANoOp(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	assert.NoError(t, notify.New(db).Send(context.Background(), taskInput(org.ID)))
}
