package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// inboundMessage writes a customer message at a given time.
func inboundMessage(t *testing.T, app *handlers.App, orgID, contactID uuid.UUID, at time.Time) {
	t.Helper()
	msg := models.Message{
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "acct",
		Direction:       models.DirectionIncoming,
		MessageType:     "text",
		Content:         "hello",
		Status:          models.MessageStatusDelivered,
		SenderType:      models.SenderContact,
	}
	require.NoError(t, app.DB.Create(&msg).Error)
	require.NoError(t, app.DB.Model(&models.Message{}).Where("id = ?", msg.ID).
		Update("created_at", at).Error)
}

func markRead(t *testing.T, app *handlers.App, orgID, userID, contactID uuid.UUID, peek bool) {
	t.Helper()
	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, orgID, userID)
	testutil.SetPathParam(req, "id", contactID.String())
	if peek {
		testutil.SetQueryParam(req, "peek", "1")
	}
	require.NoError(t, app.MarkContactRead(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

func unreadFor(t *testing.T, app *handlers.App, orgID, userID, contactID uuid.UUID) int64 {
	t.Helper()
	counts, err := app.Conversations().UnreadFor(context.Background(), orgID, userID,
		[]uuid.UUID{contactID})
	require.NoError(t, err)
	return counts[contactID]
}

// Read state used to be one flag on the contact, so whoever opened the chat
// last owned it. A supervisor glancing at a queue cleared the badge for the
// agent who actually had to answer.
func TestMarkContactRead_IsPerUser(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	owner := adminFor(t, app, org)
	colleague := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553220001")

	inboundMessage(t, app, org.ID, contact.ID, time.Now().UTC())

	assert.EqualValues(t, 1, unreadFor(t, app, org.ID, owner.ID, contact.ID))
	assert.EqualValues(t, 1, unreadFor(t, app, org.ID, colleague.ID, contact.ID))

	markRead(t, app, org.ID, colleague.ID, contact.ID, false)

	assert.EqualValues(t, 0, unreadFor(t, app, org.ID, colleague.ID, contact.ID),
		"the person who read it has nothing waiting")
	assert.EqualValues(t, 1, unreadFor(t, app, org.ID, owner.ID, contact.ID),
		"a colleague reading it does not answer the customer for you")
}

// Peek is a supervisor looking at somebody else's queue. It must change
// nothing: not their own badge, and certainly not the agent's.
func TestMarkContactRead_PeekChangesNothing(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	supervisor := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553220002")

	inboundMessage(t, app, org.ID, contact.ID, time.Now().UTC())
	markRead(t, app, org.ID, supervisor.ID, contact.ID, true)

	assert.EqualValues(t, 1, unreadFor(t, app, org.ID, supervisor.ID, contact.ID),
		"peeking is looking, not reading")

	var stillUnread int64
	require.NoError(t, app.DB.Model(&models.Message{}).
		Where("contact_id = ? AND direction = ? AND status <> ?",
			contact.ID, models.DirectionIncoming, models.MessageStatusRead).
		Count(&stillUnread).Error)
	assert.EqualValues(t, 1, stillUnread,
		"peeking must not tell the customer their message was read")
}

// A message that arrives after you read the conversation is unread again.
func TestUnreadFor_CountsMessagesAfterTheReadMark(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553220003")

	inboundMessage(t, app, org.ID, contact.ID, time.Now().UTC().Add(-time.Hour))
	markRead(t, app, org.ID, agent.ID, contact.ID, false)
	assert.EqualValues(t, 0, unreadFor(t, app, org.ID, agent.ID, contact.ID))

	inboundMessage(t, app, org.ID, contact.ID, time.Now().UTC().Add(time.Minute))
	assert.EqualValues(t, 1, unreadFor(t, app, org.ID, agent.ID, contact.ID),
		"a new message after you looked is waiting on you again")
}

// Two tabs belonging to one person race constantly. The later read must win,
// and neither may error.
func TestMarkRead_NeverMovesBackwards(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553220004")

	conv, err := app.Conversations().Active(context.Background(), org.ID, contact.ID)
	require.NoError(t, err)

	newer := time.Now().UTC()
	older := newer.Add(-time.Hour)

	require.NoError(t, app.Conversations().MarkRead(context.Background(), conv.ID, agent.ID, newer))
	require.NoError(t, app.Conversations().MarkRead(context.Background(), conv.ID, agent.ID, older))

	at, err := app.Conversations().ReadPosition(context.Background(), conv.ID, agent.ID)
	require.NoError(t, err)
	require.NotNil(t, at)
	assert.WithinDuration(t, newer, *at, time.Second,
		"a slow tab finishing late must not re-hide messages already read")
}

// Blue ticks say "somebody has this". Only the person who does may send them.
func TestMaySendReadReceipts(t *testing.T) {
	assignee := uuid.New()
	other := uuid.New()

	assigned := &models.Conversation{AssigneeID: &assignee}
	assert.True(t, conversation.MaySendReadReceipts(assigned, assignee, true))
	assert.False(t, conversation.MaySendReadReceipts(assigned, other, true),
		"a supervisor reading over someone's shoulder has not picked it up")

	unassigned := &models.Conversation{}
	assert.True(t, conversation.MaySendReadReceipts(unassigned, other, true),
		"anyone who can reply to an unassigned conversation can take it")
	assert.False(t, conversation.MaySendReadReceipts(unassigned, other, false),
		"a read-only viewer cannot answer, so must not promise that anyone will")
}
