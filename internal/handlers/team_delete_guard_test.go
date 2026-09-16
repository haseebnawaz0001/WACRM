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

func queuedConversation(t *testing.T, app *handlers.App, orgID, contactID uuid.UUID, teamID *uuid.UUID) *models.Conversation {
	t.Helper()
	conv := &models.Conversation{
		OrganizationID: orgID,
		ContactID:      contactID,
		Status:         models.ConversationOpen,
		TeamID:         teamID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)
	return conv
}

// Plan 10, S8: a team holding live work cannot simply be deleted.
//
// Deleting regardless leaves transfers and conversations pointing at a team
// that no longer exists — they vanish from every team view without ever being
// reassigned, which is how a queue silently loses its backlog.
func TestDeleteTeam_BlockedWhileWorkIsAssigned(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	team := &models.Team{OrganizationID: org.ID, Name: "Support"}
	require.NoError(t, app.DB.Create(team).Error)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	queuedConversation(t, app, org.ID, contact.ID, &team.ID)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", team.ID.String())

	require.NoError(t, app.DeleteTeam(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req),
		"a team with an open conversation must not be deleted silently")

	var stillThere int64
	app.DB.Model(&models.Team{}).Where("id = ?", team.ID).Count(&stillThere)
	assert.Equal(t, int64(1), stillThere, "the team must survive a refused delete")
}

// With a target, the work moves and the team goes.
func TestDeleteTeam_ReassignsWorkToAnotherTeam(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	from := &models.Team{OrganizationID: org.ID, Name: "Retiring"}
	to := &models.Team{OrganizationID: org.ID, Name: "Taking over"}
	require.NoError(t, app.DB.Create(from).Error)
	require.NoError(t, app.DB.Create(to).Error)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	conv := queuedConversation(t, app, org.ID, contact.ID, &from.ID)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", from.ID.String())
	req.RequestCtx.QueryArgs().Set("reassign_to_team_id", to.ID.String())

	require.NoError(t, app.DeleteTeam(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var moved models.Conversation
	require.NoError(t, app.DB.Where("id = ?", conv.ID).First(&moved).Error)
	require.NotNil(t, moved.TeamID)
	assert.Equal(t, to.ID, *moved.TeamID, "the work should have followed the reassignment")

	var gone int64
	app.DB.Model(&models.Team{}).Where("id = ?", from.ID).Count(&gone)
	assert.Zero(t, gone)
}

// A team nobody is using deletes without ceremony.
func TestDeleteTeam_EmptyTeamDeletesCleanly(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	team := &models.Team{OrganizationID: org.ID, Name: "Unused"}
	require.NoError(t, app.DB.Create(team).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", team.ID.String())

	require.NoError(t, app.DeleteTeam(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}

// A resolved conversation names its team historically; that must not keep the
// team alive forever.
func TestDeleteTeam_ResolvedWorkDoesNotBlock(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	team := &models.Team{OrganizationID: org.ID, Name: "Historic"}
	require.NoError(t, app.DB.Create(team).Error)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	conv := queuedConversation(t, app, org.ID, contact.ID, &team.ID)
	require.NoError(t, app.DB.Model(conv).Update("status", models.ConversationResolved).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", team.ID.String())

	require.NoError(t, app.DeleteTeam(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
}
