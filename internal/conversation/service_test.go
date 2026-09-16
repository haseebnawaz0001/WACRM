package conversation_test

import (
	"context"
	"sync"
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

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *conversation.Service, *models.Organization, *models.Contact) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	return db, conversation.New(db), org, contact
}

// --- Opening ---

func TestTouchInbound_OpensAConversation(t *testing.T) {
	_, svc, org, contact := setup(t)
	now := time.Now().UTC()

	c, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", now, true)
	require.NoError(t, err)

	assert.Equal(t, models.ConversationOpen, c.Status)
	require.NotNil(t, c.WaitingSince, "a customer message puts the ball in our court")
	require.NotNil(t, c.FirstCustomerMessageAt)
	assert.Equal(t, 1, c.MessageCount)
	assert.True(t, c.BotActive)
}

// waiting_since marks the oldest unanswered message. Resetting it on every
// chase would make a customer who messages repeatedly look freshly waiting.
func TestTouchInbound_KeepsTheOldestWaitingTimestamp(t *testing.T) {
	_, svc, org, contact := setup(t)
	first := time.Now().UTC().Add(-time.Hour)

	opened, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", first, true)
	require.NoError(t, err)
	require.NotNil(t, opened.WaitingSince)

	later, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	require.NotNil(t, later.WaitingSince)
	assert.WithinDuration(t, *opened.WaitingSince, *later.WaitingSince, time.Second,
		"the wait is measured from the first unanswered message")
	assert.Equal(t, 2, later.MessageCount)
}

// A contact may only have one live conversation, or the inbox would show the
// same person twice with different states.
func TestTouchInbound_OneActiveConversationPerContact(t *testing.T) {
	db, svc, org, contact := setup(t)

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ? AND status <> ?", contact.ID, models.ConversationResolved).
		Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

// Inbound webhooks run in their own goroutines, so two messages arriving at
// once must still produce one conversation.
func TestTouchInbound_ConcurrentMessagesCreateOneConversation(t *testing.T) {
	db, _, org, contact := setup(t)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := conversation.New(db)
			_, _ = svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
		}()
	}
	wg.Wait()

	var count int64
	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ?", contact.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count, "concurrent inbound messages must not split into two conversations")
}

// --- Responding ---

// Only a human agent reply counts as a response. Counting a bot greeting would
// make response-time reporting describe automation rather than service.
func TestRecordOutbound_OnlyAgentRepliesCountAsResponses(t *testing.T) {
	_, svc, org, contact := setup(t)
	agent := uuid.New()

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	bot, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderBot, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Nil(t, bot.FirstResponseAt, "a bot message is not an agent responding")
	assert.NotNil(t, bot.WaitingSince, "the customer is still waiting on a person")

	replied, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderAgent, &agent, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, replied.FirstResponseAt)
	require.NotNil(t, replied.FirstResponderID)
	assert.Equal(t, agent, *replied.FirstResponderID)
	assert.Nil(t, replied.WaitingSince, "an agent reply clears the wait")
}

func TestRecordOutbound_FirstResponseIsRecordedOnce(t *testing.T) {
	_, svc, org, contact := setup(t)
	first, second := uuid.New(), uuid.New()

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	one, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderAgent, &first, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, one.FirstResponseAt)

	two, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderAgent, &second, time.Now().UTC())
	require.NoError(t, err)

	assert.Equal(t, one.FirstResponseAt.Unix(), two.FirstResponseAt.Unix())
	assert.Equal(t, first, *two.FirstResponderID, "the first responder does not change")
}

// A campaign blast must not open a conversation for every recipient; the
// customer's reply is what starts one.
func TestRecordOutbound_CampaignDoesNotOpenAConversation(t *testing.T) {
	db, svc, org, contact := setup(t)

	_, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderCampaign, nil, time.Now().UTC())
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ?", contact.ID).Count(&count).Error)
	assert.Zero(t, count)
}

// An agent reaching out first legitimately starts one.
func TestRecordOutbound_AgentOutreachOpensAConversation(t *testing.T) {
	_, svc, org, contact := setup(t)
	agent := uuid.New()

	c, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderAgent, &agent, time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, models.ConversationOpen, c.Status)
}

func TestRecordOutbound_AutoPendingWhenConfigured(t *testing.T) {
	db, _, org, contact := setup(t)
	svc := &conversation.Service{DB: db, Set: conversation.Settings{
		ReopenWindow: conversation.DefaultReopenWindow, AutoPendingOnAgentReply: true,
	}}
	agent := uuid.New()

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	replied, err := svc.RecordOutbound(ctx(), org.ID, contact.ID, models.SenderAgent, &agent, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, models.ConversationPending, replied.Status)
}

// --- Resolving and reopening ---

func TestResolve(t *testing.T) {
	_, svc, org, contact := setup(t)
	user := uuid.New()

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	resolved, err := svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent,
		crmevents.UserActor(user, "Agent"))
	require.NoError(t, err)

	assert.Equal(t, models.ConversationResolved, resolved.Status)
	require.NotNil(t, resolved.ResolvedAt)
	require.NotNil(t, resolved.ResolvedByID)
	assert.Equal(t, user, *resolved.ResolvedByID)
	assert.Nil(t, resolved.WaitingSince)
}

// A customer replying soon after resolution is continuing the same issue, so
// the history stays in one thread rather than fragmenting.
func TestTouchInbound_ReopensRecentlyResolvedConversation(t *testing.T) {
	_, svc, org, contact := setup(t)

	opened, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	reopened, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assert.Equal(t, opened.ID, reopened.ID, "the same conversation is reopened")
	assert.Equal(t, models.ConversationOpen, reopened.Status)
	assert.Equal(t, 1, reopened.ReopenedCount)
	assert.Nil(t, reopened.ResolvedAt)
}

// A message long after resolution is a new issue, not a continuation.
func TestTouchInbound_StartsANewConversationAfterTheReopenWindow(t *testing.T) {
	db, svc, org, contact := setup(t)

	opened, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	// Push the resolution outside the window.
	require.NoError(t, db.Model(&models.Conversation{}).Where("id = ?", opened.ID).
		Update("resolved_at", time.Now().UTC().Add(-48*time.Hour)).Error)

	fresh, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assert.NotEqual(t, opened.ID, fresh.ID, "a later message starts a new conversation")
	assert.Equal(t, 0, fresh.ReopenedCount)
}

// --- Snoozing ---

// Snoozing means "not now"; a customer writing in has made it now.
func TestTouchInbound_CustomerMessageEndsASnooze(t *testing.T) {
	_, svc, org, contact := setup(t)

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	snoozed, err := svc.Snooze(ctx(), org.ID, contact.ID, time.Now().UTC().Add(24*time.Hour), crmevents.SystemActor())
	require.NoError(t, err)
	require.Equal(t, models.ConversationSnoozed, snoozed.Status)

	woken, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assert.Equal(t, models.ConversationOpen, woken.Status)
	assert.Nil(t, woken.SnoozedUntil)
}

// Resolving must clear the snooze, or a finished conversation would wake up.
func TestResolve_ClearsSnooze(t *testing.T) {
	_, svc, org, contact := setup(t)

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Snooze(ctx(), org.ID, contact.ID, time.Now().UTC().Add(time.Hour), crmevents.SystemActor())
	require.NoError(t, err)

	resolved, err := svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)
	assert.Nil(t, resolved.SnoozedUntil)
}

// --- Assignment ---

// The assignee is who is handling this conversation; the contact owner is the
// relationship manager. Conflating them is what plan 10's S5 separates.
func TestAssign_DoesNotTouchTheContactOwner(t *testing.T) {
	db, svc, org, contact := setup(t)
	owner := testutil.CreateTestUser(t, db, org.ID)
	handler := testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("assigned_user_id", owner.ID).Error)

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assigned, err := svc.Assign(ctx(), org.ID, contact.ID, &handler.ID, nil, crmevents.SystemActor())
	require.NoError(t, err)

	require.NotNil(t, assigned.AssigneeID)
	assert.Equal(t, handler.ID, *assigned.AssigneeID)
	assert.False(t, assigned.BotActive, "a human taking over stops the bot driving")

	var reloaded models.Contact
	require.NoError(t, db.First(&reloaded, "id = ?", contact.ID).Error)
	require.NotNil(t, reloaded.AssignedUserID)
	assert.Equal(t, owner.ID, *reloaded.AssignedUserID, "the relationship owner is unchanged")
}

// --- Events ---

func TestTransitions_RecordEvents(t *testing.T) {
	db, svc, org, contact := setup(t)
	require.NoError(t, db.Exec("DELETE FROM crm_event_outbox WHERE organization_id = ?", org.ID).Error)

	_, err := svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	var types []string
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND contact_id = ?", org.ID, contact.ID).
		Order("occurred_at").Pluck("type", &types).Error)

	assert.Contains(t, types, "conversation.created")
	assert.Contains(t, types, "conversation.status_changed")
}

func TestActive_ReturnsNotFoundWhenResolved(t *testing.T) {
	_, svc, org, contact := setup(t)

	_, err := svc.Active(ctx(), org.ID, contact.ID)
	assert.ErrorIs(t, err, conversation.ErrNotFound)

	_, err = svc.TouchInbound(ctx(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Active(ctx(), org.ID, contact.ID)
	assert.NoError(t, err)

	_, err = svc.Resolve(ctx(), org.ID, contact.ID, models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)
	_, err = svc.Active(ctx(), org.ID, contact.ID)
	assert.ErrorIs(t, err, conversation.ErrNotFound)
}
