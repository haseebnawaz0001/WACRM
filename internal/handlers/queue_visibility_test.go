package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// agentWithoutContactsRead builds an agent who can use the chat but does not
// hold contacts:read — the role the queue is designed for.
func agentWithoutContactsRead(t *testing.T, app *handlers.App, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, orgID, "queue-agent-"+uuid.NewString()[:8],
		[]string{"chat:read"})
	return testutil.CreateTestUser(t, app.DB, orgID, testutil.WithRoleID(&role.ID))
}

// Plan 10, S9: visibility is contact ∪ conversation.
//
// The inbox's Unassigned view lists conversations nobody has picked up so an
// agent can take one. Scoping the contact by ownership alone meant they saw the
// row and then got a 404 opening it, which reads as the product being broken
// rather than as a permission boundary.
func TestGetContact_AgentCanOpenAnUnassignedQueuedConversation(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := agentWithoutContactsRead(t, app, org.ID)

	// A contact the agent does not own and has no transfer for.
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	// Its conversation is in the general queue: handed off by the bot, nobody
	// has taken it.
	require.NoError(t, app.DB.Create(&models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		Handling:       models.HandlingNone,
		OpenedAt:       time.Now(),
	}).Error)
	require.NoError(t, app.DB.Model(&models.Conversation{}).
		Where("contact_id = ?", contact.ID).Update("handling", models.HandlingNone).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContact(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req),
		"a conversation in the queue must be openable by an agent who can take it")
}

// A conversation the bot still holds is not waiting for a human, so it does not
// widen anybody's visibility.
func TestGetContact_BotHeldConversationStaysHidden(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := agentWithoutContactsRead(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	require.NoError(t, app.DB.Create(&models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		Handling:       models.HandlingBot,
		OpenedAt:       time.Now(),
	}).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContact(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

// A queue belonging to another team is not this agent's to take from.
func TestGetContact_OtherTeamsQueueStaysHidden(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := agentWithoutContactsRead(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	otherTeam := &models.Team{OrganizationID: org.ID, Name: "Other team"}
	require.NoError(t, app.DB.Create(otherTeam).Error)

	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		TeamID:         &otherTeam.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)
	require.NoError(t, app.DB.Model(conv).Update("handling", models.HandlingNone).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContact(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req),
		"another team's queue is not this agent's to take from")
}
