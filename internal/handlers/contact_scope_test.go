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

// chatOnlyAgent can use the inbox but holds no contacts:read.
func chatOnlyAgent(t *testing.T, app *handlers.App, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, orgID, "chat-only-"+uuid.NewString()[:8],
		[]string{"chat:read", "chat:write"})
	return testutil.CreateTestUser(t, app.DB, orgID, testutil.WithRoleID(&role.ID))
}

// Plan 10, S9: an endpoint keyed by a contact id has to ask the contact scope.
//
// Notes are internal and often blunt — "refuses to pay", "threatened to
// escalate". The notes list was gated on chat:read alone, so any agent could
// read the notes on any contact in the organization by putting its id in the
// URL, including contacts the contact list itself will not show them.
func TestListConversationNotes_HidesAContactTheAgentCannotSee(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)

	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", other.ID).Error)

	require.NoError(t, app.DB.Create(&models.ConversationNote{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		CreatedByID:    other.ID,
		Content:        "internal only",
	}).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.ListConversationNotes(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

// The agent assigned to the contact still reads its notes.
func TestListConversationNotes_AllowsTheAssignedAgent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", agent.ID).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.ListConversationNotes(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

// A queued conversation is the case the scope exists to allow: the agent can
// take it, so they can read its notes before deciding to.
func TestListConversationNotes_AllowsAQueuedConversation(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)
	require.NoError(t, app.DB.Model(conv).Update("bot_active", false).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.ListConversationNotes(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

// Writing a note onto a contact the agent cannot see puts internal commentary
// on a record they have no business touching.
func TestCreateConversationNote_RefusesAContactTheAgentCannotSee(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)

	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", other.ID).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"content": "should not land"})
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.CreateConversationNote(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))

	var notes int64
	app.DB.Model(&models.ConversationNote{}).Where("contact_id = ?", contact.ID).Count(&notes)
	assert.Zero(t, notes)
}

// The timeline is the contact's entire history — messages, notes, field
// changes, deals. Reaching it needed only chat:read and an id.
func TestGetContactTimeline_HidesAContactTheAgentCannotSee(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)

	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", other.ID).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContactTimeline(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

// And still returns for the agent who owns the contact.
func TestGetContactTimeline_AllowsTheAssignedAgent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatOnlyAgent(t, app, org.ID)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", agent.ID).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContactTimeline(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}
