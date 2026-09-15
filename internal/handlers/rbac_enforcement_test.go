package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// createSystemAgentUser creates a user whose role carries exactly the default
// permissions of the built-in "agent" system role.
func createSystemAgentUser(t *testing.T, app *handlers.App, org *models.Organization) *models.User {
	t.Helper()
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "system-agent", models.SystemRolePermissions()["agent"])
	return testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
}

// TestRBAC_AgentForbiddenOnManagementEndpoints verifies that a default agent
// cannot reach management APIs that were previously only hidden in the UI.
func TestRBAC_AgentForbiddenOnManagementEndpoints(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := createSystemAgentUser(t, app, org)

	cases := []struct {
		name    string
		handler func(*fastglue.Request) error
		body    bool
	}{
		{"ListWebhooks", app.ListWebhooks, false},
		{"CreateWebhook", app.CreateWebhook, true},
		{"ListRoles", app.ListRoles, false},
		{"CreateRole", app.CreateRole, true},
		{"UpdateRole", app.UpdateRole, true},
		{"DeleteRole", app.DeleteRole, false},
		{"ListPermissions", app.ListPermissions, false},
		{"ListCampaigns", app.ListCampaigns, false},
		{"CreateCampaign", app.CreateCampaign, true},
		{"StartCampaign", app.StartCampaign, false},
		{"CreateTemplate", app.CreateTemplate, true},
		{"SyncTemplates", app.SyncTemplates, true},
		{"DeleteTemplate", app.DeleteTemplate, false},
		{"CreateFlow", app.CreateFlow, true},
		{"UpdateChatbotSettings", app.UpdateChatbotSettings, true},
		{"CreateKeywordRule", app.CreateKeywordRule, true},
		{"ListAIContexts", app.ListAIContexts, false},
		{"CreateCustomAction", app.CreateCustomAction, true},
		{"GetSSOSettings", app.GetSSOSettings, false},
		{"GetDashboardStats", app.GetDashboardStats, false},
		{"SubscribeApp", app.SubscribeApp, false},
		{"CreateCatalog", app.CreateCatalog, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *fastglue.Request
			if tc.body {
				req = testutil.NewJSONRequest(t, map[string]any{})
			} else {
				req = testutil.NewGETRequest(t)
			}
			testutil.SetAuthContext(req, org.ID, agent.ID)
			testutil.SetPathParam(req, "id", agent.ID.String())

			require.NoError(t, tc.handler(req))
			assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
		})
	}
}

// TestRBAC_AgentKeepsChatDependencies verifies that endpoints the chat screen
// relies on stay available to a default agent.
func TestRBAC_AgentKeepsChatDependencies(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := createSystemAgentUser(t, app, org)

	cases := []struct {
		name    string
		handler func(*fastglue.Request) error
	}{
		{"ListTemplates (template picker)", app.ListTemplates},
		{"ListFlows (send flow)", app.ListFlows},
		{"ListCannedResponses", app.ListCannedResponses},
		{"GetChatbotSettings (transfers page)", app.GetChatbotSettings},
		{"ListCustomActions (chat buttons)", app.ListCustomActions},
		{"GetICEServers (calls)", app.GetICEServers},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.NewGETRequest(t)
			testutil.SetAuthContext(req, org.ID, agent.ID)

			require.NoError(t, tc.handler(req))
			assert.NotEqual(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
			assert.NotEqual(t, fasthttp.StatusUnauthorized, testutil.GetResponseStatusCode(req))
		})
	}

	t.Run("GetUser allows reading own record", func(t *testing.T) {
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, agent.ID)
		testutil.SetPathParam(req, "id", agent.ID.String())

		require.NoError(t, app.GetUser(req))
		assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	})

	t.Run("GetUser forbids reading other users", func(t *testing.T) {
		other := testutil.CreateTestUser(t, app.DB, org.ID)
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, agent.ID)
		testutil.SetPathParam(req, "id", other.ID.String())

		require.NoError(t, app.GetUser(req))
		assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
	})
}

// TestRBAC_CustomActionConfigRedactedForAgents verifies that users who can only
// run custom actions from chat do not receive webhook URLs, headers or code.
func TestRBAC_CustomActionConfigRedactedForAgents(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	agent := createSystemAgentUser(t, app, org)
	admin := createAdminUser(t, app, org.ID)

	action := models.CustomAction{
		OrganizationID: org.ID,
		Name:           "CRM lookup",
		ActionType:     models.ActionTypeWebhook,
		Config: models.JSONB{
			"url":     "https://crm.example.com/hook",
			"headers": map[string]any{"Authorization": "Bearer secret-token"},
		},
		IsActive: true,
	}
	require.NoError(t, app.DB.Create(&action).Error)

	listConfigs := func(t *testing.T, userID uuid.UUID) []map[string]any {
		t.Helper()
		req := testutil.NewGETRequest(t)
		testutil.SetAuthContext(req, org.ID, userID)
		require.NoError(t, app.ListCustomActions(req))
		require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

		var resp struct {
			Data struct {
				CustomActions []struct {
					Name   string         `json:"name"`
					Config map[string]any `json:"config"`
				} `json:"custom_actions"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(req.RequestCtx.Response.Body(), &resp))
		require.Len(t, resp.Data.CustomActions, 1)
		return []map[string]any{resp.Data.CustomActions[0].Config}
	}

	agentConfig := listConfigs(t, agent.ID)[0]
	assert.Empty(t, agentConfig, "agent must not see action config")

	adminConfig := listConfigs(t, admin.ID)[0]
	assert.Equal(t, "https://crm.example.com/hook", adminConfig["url"])
}
