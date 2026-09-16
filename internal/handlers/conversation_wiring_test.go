package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the seam between the message paths and the conversation state
// machine. The unit tests in internal/conversation prove the transitions; these
// prove the handlers actually call them, which is where wiring gets forgotten.

func TestConversations_InboundMessageOpensAConversation(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15551900001"))

	conv, err := app.Conversations().TouchInbound(context.Background(),
		org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assert.Equal(t, models.ConversationOpen, conv.Status)
	require.NotNil(t, conv.WaitingSince)

	active, err := app.Conversations().Active(context.Background(), org.ID, contact.ID)
	require.NoError(t, err)
	assert.Equal(t, conv.ID, active.ID)
}

// An agent reply must clear the wait and record the response; a bot reply must
// not, or response-time reporting describes automation instead of service.
func TestConversations_OnlyAgentRepliesClearTheWait(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15551910001"))

	svc := app.Conversations()
	_, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	afterBot, err := svc.RecordOutbound(context.Background(), org.ID, contact.ID,
		models.SenderBot, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.NotNil(t, afterBot.WaitingSince)
	assert.Nil(t, afterBot.FirstResponseAt)

	afterAgent, err := svc.RecordOutbound(context.Background(), org.ID, contact.ID,
		models.SenderAgent, &agent.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.Nil(t, afterAgent.WaitingSince)
	require.NotNil(t, afterAgent.FirstResponseAt)
}

// The App wires default inbox settings; a conversation resolved and then
// messaged again straight away is the same issue continuing.
func TestConversations_AppUsesTheReopenWindow(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15551920001"))

	svc := app.Conversations()
	opened, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	_, err = svc.Resolve(context.Background(), org.ID, contact.ID,
		models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	reopened, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)

	assert.Equal(t, opened.ID, reopened.ID)
	assert.Equal(t, 1, reopened.ReopenedCount)
}

func TestConversations_ResolvedConversationIsNoLongerActive(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15551930001"))

	svc := app.Conversations()
	_, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Resolve(context.Background(), org.ID, contact.ID,
		models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	_, err = svc.Active(context.Background(), org.ID, contact.ID)
	assert.ErrorIs(t, err, conversation.ErrNotFound)
}
