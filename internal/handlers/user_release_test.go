package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Plan 10, S8: deactivating a user has to release the live work they were
// holding.
//
// Without it the work stays where it was, which is the worst of both worlds: a
// conversation assigned to somebody who can no longer sign in shows in neither
// the Unassigned view nor any active agent's list, so the customer waits on a
// person who is gone.
func TestUpdateUser_DeactivationUnassignsLiveWork(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		AssigneeID:     &agent.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)

	// A resolved conversation names its assignee historically and must stay put.
	resolvedContact := testutil.CreateTestContact(t, app.DB, org.ID)
	resolved := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      resolvedContact.ID,
		Status:         models.ConversationResolved,
		AssigneeID:     &agent.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(resolved).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": false})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", agent.ID.String())

	require.NoError(t, app.UpdateUser(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var reloaded models.Conversation
	require.NoError(t, app.DB.Where("id = ?", conv.ID).First(&reloaded).Error)
	assert.Nil(t, reloaded.AssigneeID,
		"an open conversation must return to the queue when its agent is deactivated")

	var stillAssigned models.Conversation
	require.NoError(t, app.DB.Where("id = ?", resolved.ID).First(&stillAssigned).Error)
	require.NotNil(t, stillAssigned.AssigneeID)
	assert.Equal(t, agent.ID, *stillAssigned.AssigneeID,
		"a resolved conversation keeps its assignee as history")
}

// An API key authenticates on its own hash and never consults users.is_active,
// so a deactivated user keeps full API access until the key is revoked.
func TestUpdateUser_DeactivationRevokesAPIKeys(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))

	key := &models.APIKey{
		OrganizationID: org.ID,
		UserID:         agent.ID,
		Name:           "agent integration",
		KeyPrefix:      "wak_test",
		KeyHash:        "hash",
		IsActive:       true,
	}
	require.NoError(t, app.DB.Create(key).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": false})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", agent.ID.String())
	require.NoError(t, app.UpdateUser(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var reloaded models.APIKey
	require.NoError(t, app.DB.Where("id = ?", key.ID).First(&reloaded).Error)
	assert.False(t, reloaded.IsActive, "a deactivated user's API key must stop authenticating")
}

// Team membership drives round-robin assignment, so leaving the row behind
// keeps routing work to somebody who is gone.
func TestUpdateUser_DeactivationRemovesTeamMembership(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))

	team := &models.Team{OrganizationID: org.ID, Name: "Support"}
	require.NoError(t, app.DB.Create(team).Error)
	require.NoError(t, app.DB.Create(&models.TeamMember{TeamID: team.ID, UserID: agent.ID}).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": false})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", agent.ID.String())
	require.NoError(t, app.UpdateUser(req))

	var members int64
	app.DB.Model(&models.TeamMember{}).
		Where("team_id = ? AND user_id = ?", team.ID, agent.ID).Count(&members)
	assert.Zero(t, members, "a deactivated user must leave the round-robin pool")
}

// A private segment owned by a departed user is visible to nobody and editable
// by nobody; it becomes shared under the admin who deactivated them. Automation
// rules keep running, so their ownership moves rather than dangling.
func TestUpdateUser_DeactivationRehomesPrivateSegmentsAndRules(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))

	seg := &models.Segment{
		OrganizationID: org.ID,
		Name:           "My leads " + uuid.NewString()[:8],
		Filter:         models.JSONB{"op": "and", "conditions": []any{}},
		Visibility:     models.SegmentPrivate,
		CreatedByID:    agent.ID,
	}
	require.NoError(t, app.DB.Create(seg).Error)

	rule := &models.AutomationRule{
		OrganizationID: org.ID,
		Name:           "Welcome " + uuid.NewString()[:8],
		TriggerType:    "contact.created",
		// Actions is a JSONB object wrapping the list; the column default is a
		// bare [], which will not scan back into models.JSONB.
		Actions:     models.JSONB{"list": []any{}},
		CreatedByID: &agent.ID,
	}
	require.NoError(t, app.DB.Create(rule).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": false})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", agent.ID.String())
	require.NoError(t, app.UpdateUser(req))

	var reloadedSeg models.Segment
	require.NoError(t, app.DB.Where("id = ?", seg.ID).First(&reloadedSeg).Error)
	assert.Equal(t, "shared", reloadedSeg.Visibility)
	assert.Equal(t, admin.ID, reloadedSeg.CreatedByID)

	var reloadedRule models.AutomationRule
	require.NoError(t, app.DB.Where("id = ?", rule.ID).First(&reloadedRule).Error)
	require.NotNil(t, reloadedRule.CreatedByID)
	assert.Equal(t, admin.ID, *reloadedRule.CreatedByID)
}

// Reactivating is not a deactivation and must leave everything alone.
func TestUpdateUser_ReactivationDoesNotReleaseWork(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	require.NoError(t, app.DB.Model(agent).Update("is_active", false).Error)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	conv := &models.Conversation{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		AssigneeID:     &agent.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, app.DB.Create(conv).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"is_active": true})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", agent.ID.String())
	require.NoError(t, app.UpdateUser(req))

	var reloaded models.Conversation
	require.NoError(t, app.DB.Where("id = ?", conv.ID).First(&reloaded).Error)
	require.NotNil(t, reloaded.AssigneeID, "reactivation must not unassign anything")
}
