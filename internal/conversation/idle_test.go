package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// idleService builds a conversation service with the sweep thresholds set.
func idleService(db *gorm.DB, idle, pending time.Duration) *conversation.Service {
	svc := conversation.New(db)
	svc.Set = conversation.Settings{
		ReopenWindow:    conversation.DefaultReopenWindow,
		AutoResolveIdle: idle,
		PendingTimeout:  pending,
	}
	return svc
}

// staleConversation opens one and backdates its last message.
func staleConversation(t *testing.T, db *gorm.DB, svc *conversation.Service, orgID uuid.UUID, age time.Duration, status models.ConversationStatus) *models.Contact {
	t.Helper()
	contact := testutil.CreateTestContact(t, db, orgID)
	at := time.Now().UTC().Add(-age)

	_, err := svc.TouchInbound(context.Background(), orgID, contact.ID, "acct", at, false)
	require.NoError(t, err)

	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ?", contact.ID).
		Updates(map[string]any{
			"status":          status,
			"last_message_at": at,
			"opened_at":       at,
		}).Error)
	return contact
}

func statusOf(t *testing.T, db *gorm.DB, contactID uuid.UUID) (models.ConversationStatus, string) {
	t.Helper()
	var c models.Conversation
	require.NoError(t, db.Where("contact_id = ?", contactID).First(&c).Error)
	return c.Status, c.ResolutionReason
}

// An inbox that never finishes anything is an inbox people stop reading. This
// used to run only when the SLA feature was switched on, so organizations that
// did not want response-time alerting also never got conversations closed.
func TestSweepIdle_ClosesConversationsNothingHappenedIn(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := idleService(db, 48*time.Hour, 0)

	stale := staleConversation(t, db, svc, org.ID, 72*time.Hour, models.ConversationOpen)
	fresh := staleConversation(t, db, svc, org.ID, time.Hour, models.ConversationOpen)

	result, err := svc.SweepIdle(context.Background(), org.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Idle)

	status, reason := statusOf(t, db, stale.ID)
	assert.Equal(t, models.ConversationResolved, status)
	assert.Equal(t, models.ResolutionClientInactivity, reason,
		"the reason has to separate this from an agent finishing the job, or every report overstates agent resolutions")

	status, _ = statusOf(t, db, fresh.ID)
	assert.Equal(t, models.ConversationOpen, status, "a conversation from an hour ago is live")
}

// Waiting on the customer is a different kind of stale from nobody speaking at
// all, and organizations set the two independently.
func TestSweepIdle_ClosesPendingSeparately(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := idleService(db, 0, 24*time.Hour)

	pending := staleConversation(t, db, svc, org.ID, 48*time.Hour, models.ConversationPending)
	open := staleConversation(t, db, svc, org.ID, 48*time.Hour, models.ConversationOpen)

	result, err := svc.SweepIdle(context.Background(), org.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Pending)
	assert.Equal(t, 0, result.Idle, "the idle rule is off")

	status, reason := statusOf(t, db, pending.ID)
	assert.Equal(t, models.ConversationResolved, status)
	assert.Equal(t, models.ResolutionPendingTimeout, reason)

	status, _ = statusOf(t, db, open.ID)
	assert.Equal(t, models.ConversationOpen, status,
		"an open conversation is waiting on us, which the pending rule says nothing about")
}

// Snoozing is someone saying "come back to me on Thursday". Closing it as
// stale would silently throw that away.
func TestSweepIdle_LeavesSnoozedConversationsAlone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := idleService(db, 24*time.Hour, 0)

	snoozed := staleConversation(t, db, svc, org.ID, 72*time.Hour, models.ConversationSnoozed)
	wake := time.Now().UTC().Add(48 * time.Hour)
	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ?", snoozed.ID).
		Update("snoozed_until", wake).Error)

	result, err := svc.SweepIdle(context.Background(), org.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, result.Idle)

	status, _ := statusOf(t, db, snoozed.ID)
	assert.Equal(t, models.ConversationSnoozed, status)
}

// Both thresholds off is the default, and it has to stay expressible: some
// organizations want the list to be the record.
func TestSweepIdle_DoesNothingWhenDisabled(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := idleService(db, 0, 0)

	stale := staleConversation(t, db, svc, org.ID, 200*time.Hour, models.ConversationOpen)

	result, err := svc.SweepIdle(context.Background(), org.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.Zero(t, result.Idle)
	assert.Zero(t, result.Pending)

	status, _ := statusOf(t, db, stale.ID)
	assert.Equal(t, models.ConversationOpen, status)
}

// Closing a conversation must end the script running inside it, or the
// customer's next message weeks later is read as an answer to a prompt.
func TestResolve_EndsTheChatbotSession(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	svc := conversation.New(db)
	ctx := context.Background()

	_, err := svc.TouchInbound(ctx, org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	session := models.ChatbotSession{
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		WhatsAppAccount: "acct",
		Status:          models.SessionStatusActive,
		StartedAt:       time.Now().UTC(),
	}
	require.NoError(t, db.Create(&session).Error)

	_, err = svc.Resolve(ctx, org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	var after models.ChatbotSession
	require.NoError(t, db.Where("id = ?", session.ID).First(&after).Error)
	assert.Equal(t, models.SessionStatusCompleted, after.Status,
		"a finished conversation has no half-asked question left in it")
	assert.NotNil(t, after.CompletedAt)
}
