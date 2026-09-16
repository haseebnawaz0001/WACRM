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
)

func chatAgent(t *testing.T, app *handlers.App, orgID uuid.UUID) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, orgID, "audience-agent-"+uuid.NewString()[:8],
		[]string{"chat:read"})
	return testutil.CreateTestUser(t, app.DB, orgID, testutil.WithRoleID(&role.ID))
}

// Plan 10, S10: a realtime payload goes to the people allowed to see it.
//
// `new_message` was broadcast org-wide with the contact's name and the message
// body in it, so every connected client received every customer conversation in
// the organization — including agents who cannot open that contact at all. The
// same feed drove the toast notifications, so the leak was visible on screen.
func TestFilterUsersWhoCanSeeContact_ExcludesAnAgentWithNoClaimOnTheContact(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	owner := chatAgent(t, app, org.ID)
	bystander := chatAgent(t, app, org.ID)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("assigned_user_id", owner.ID).Error)
	require.NoError(t, app.DB.Where("id = ?", contact.ID).First(contact).Error)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{owner.ID, bystander.ID})

	assert.Contains(t, audience, owner.ID, "the assigned agent must still be told")
	assert.NotContains(t, audience, bystander.ID,
		"an agent with no claim on the contact must not receive its messages")
}

// Someone who can open every contact can be told about every message.
func TestFilterUsersWhoCanSeeContact_IncludesUsersWithContactsRead(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	supervisor := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{supervisor.ID})
	assert.Contains(t, audience, supervisor.ID)
}

// A conversation sitting in the general queue is waiting for anyone, so the
// arrival of a message is exactly what every agent needs to see.
func TestFilterUsersWhoCanSeeContact_IncludesEveryoneForTheGeneralQueue(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatAgent(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)
	require.NoError(t, app.DB.Model(conv).Update("bot_active", false).Error)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{agent.ID})
	assert.Contains(t, audience, agent.ID)
}

// A queue that belongs to a team reaches that team, and stops there.
func TestFilterUsersWhoCanSeeContact_TeamQueueReachesOnlyThatTeam(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	member := chatAgent(t, app, org.ID)
	outsider := chatAgent(t, app, org.ID)

	team := &models.Team{OrganizationID: org.ID, Name: "Billing"}
	require.NoError(t, app.DB.Create(team).Error)
	require.NoError(t, app.DB.Create(&models.TeamMember{TeamID: team.ID, UserID: member.ID}).Error)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		TeamID:         &team.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)
	require.NoError(t, app.DB.Model(conv).Update("bot_active", false).Error)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{member.ID, outsider.ID})
	assert.Contains(t, audience, member.ID)
	assert.NotContains(t, audience, outsider.ID)
}

// A conversation the bot still holds is nobody's queue, so it widens nothing.
func TestFilterUsersWhoCanSeeContact_BotHeldConversationReachesNobodyExtra(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := chatAgent(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	require.NoError(t, app.DB.Create(&models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		BotActive:      true,
		OpenedAt:       time.Now(),
	}).Error)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{agent.ID})
	assert.Empty(t, audience)
}

// An agent holding an active transfer for the contact is working it, whatever
// the contact's owner says.
func TestFilterUsersWhoCanSeeContact_IncludesTheActiveTransferAgent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	handler := chatAgent(t, app, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	require.NoError(t, app.DB.Create(&models.AgentTransfer{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		AgentID:        &handler.ID,
		Status:         models.TransferStatusActive,
	}).Error)

	audience := app.FilterUsersWhoCanSeeContact(org.ID, contact, []uuid.UUID{handler.ID})
	assert.Contains(t, audience, handler.ID)
}

// Nobody online means nobody to tell; the filter must not go looking anyway.
func TestFilterUsersWhoCanSeeContact_EmptyCandidateListIsEmpty(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	assert.Empty(t, app.FilterUsersWhoCanSeeContact(org.ID, contact, nil))
}
