package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func decodeAutomation(t *testing.T, body []byte) handlers.AutomationResponse {
	t.Helper()
	var result struct {
		Data struct {
			Automation handlers.AutomationResponse `json:"automation"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Automation
}

func tagAutomationBody(overrides map[string]any) map[string]any {
	body := map[string]any{
		"name":         "Tag VIP then chase",
		"trigger_type": "contact.tag_added",
		"trigger_config": map[string]any{
			"tags": []string{"VIP"},
		},
		"actions": []map[string]any{{
			"id":     "a1",
			"type":   crmactions.TypeAddTags,
			"config": map[string]any{"tags": []string{"Chased"}},
		}},
	}
	for key, value := range overrides {
		body[key] = value
	}
	return body
}

func createAutomationVia(t *testing.T, app *handlers.App, org *models.Organization, user *models.User, body map[string]any) handlers.AutomationResponse {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateAutomation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	return decodeAutomation(t, testutil.GetResponseBody(req))
}

func TestCreateAutomation_StoresTheRuleDisabled(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	rule := createAutomationVia(t, app, org, admin, tagAutomationBody(nil))

	assert.Equal(t, "Tag VIP then chase", rule.Name)
	assert.False(t, rule.Enabled, "a rule that starts messaging on save leaves no room to check it")
	require.Len(t, rule.Actions, 1)
	assert.Equal(t, crmactions.TypeAddTags, rule.Actions[0].Type)
	assert.Equal(t, automation.DefaultMaxRunsPerHour, rule.RunPolicy.MaxRunsPerHour)
}

func TestCreateAutomation_RejectsAnUnknownTrigger(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, tagAutomationBody(map[string]any{
		"trigger_type": "contact.exploded", "trigger_config": map[string]any{},
	}))
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateAutomation(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestCreateAutomation_RejectsAnActionThatCannotRun(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, tagAutomationBody(map[string]any{
		"actions": []map[string]any{{
			"id": "a1", "type": crmactions.TypeCallWebhook,
			"config": map[string]any{"url": "not a url"},
		}},
	}))
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateAutomation(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// A rule one agent writes acts on everybody's customers, so writing one is not
// an agent permission.
func TestCreateAutomation_RequiresTheAutomationPermission(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)

	role := testutil.CreateAgentRole(t, app.DB, org.ID)
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewJSONRequest(t, tagAutomationBody(nil))
	testutil.SetAuthContext(req, org.ID, agent.ID)

	require.Error(t, app.CreateAutomation(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

func TestEnableAutomation_TurnsTheRuleOn(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	rule := createAutomationVia(t, app, org, admin, tagAutomationBody(nil))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", rule.ID)

	require.NoError(t, app.EnableAutomation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	assert.True(t, decodeAutomation(t, testutil.GetResponseBody(req)).Enabled)
}

// The tester is what makes a rule safe to write, so it must never touch the
// contact it is tested against.
func TestTestAutomation_ChangesNothing(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15558200001"))

	rule := createAutomationVia(t, app, org, admin, tagAutomationBody(nil))

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(),
		"event_data": map[string]any{"tag": "VIP"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", rule.ID)

	require.NoError(t, app.TestAutomation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Run models.AutomationRun `json:"run"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.True(t, result.Data.Run.DryRun)
	assert.Equal(t, models.AutomationSucceeded, result.Data.Run.Status)

	var after models.Contact
	require.NoError(t, app.DB.Where("id = ?", contact.ID).First(&after).Error)
	assert.Empty(t, after.Tags, "a dry run that tagged the contact would make the tester dangerous")
}

func TestListAutomations_IsScopedToTheOrganization(t *testing.T) {
	app := newTestApp(t)
	mine := testutil.CreateTestOrganization(t, app.DB)
	theirs := testutil.CreateTestOrganization(t, app.DB)

	createAutomationVia(t, app, mine, adminFor(t, app, mine), tagAutomationBody(nil))
	createAutomationVia(t, app, theirs, adminFor(t, app, theirs),
		tagAutomationBody(map[string]any{"name": "Not mine"}))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, mine.ID, adminFor(t, app, mine).ID)

	require.NoError(t, app.ListAutomations(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Automations []handlers.AutomationResponse `json:"automations"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.Automations, 1)
	assert.Equal(t, "Tag VIP then chase", result.Data.Automations[0].Name)
}

// The builder must not be able to offer a trigger this backend does not have.
func TestAutomationCatalog_OnlyListsTriggersThatCanFire(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.AutomationCatalog(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Triggers []automation.Trigger `json:"triggers"`
			Actions  []string             `json:"actions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.NotEmpty(t, result.Data.Triggers)
	require.NotEmpty(t, result.Data.Actions)

	kinds := map[string]bool{}
	for _, trigger := range result.Data.Triggers {
		kinds[trigger.Type] = true
	}
	assert.True(t, kinds["contact.tag_added"])
	assert.True(t, kinds[automation.TriggerNoCustomerReply])
	// Keyword rules and chatbot flows own live message handling; two bots
	// answering one message is worse than none.
	assert.False(t, kinds["message.incoming"])
}

func TestDeleteAutomation_RemovesTheRule(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	rule := createAutomationVia(t, app, org, admin, tagAutomationBody(nil))

	req := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", rule.ID)

	require.NoError(t, app.DeleteAutomation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	get := testutil.NewGETRequest(t)
	testutil.SetAuthContext(get, org.ID, admin.ID)
	testutil.SetPathParam(get, "id", rule.ID)
	require.NoError(t, app.GetAutomation(get))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(get))
}

func TestGetAutomation_CannotReachAnotherOrganizationsRule(t *testing.T) {
	app := newTestApp(t)
	theirs := testutil.CreateTestOrganization(t, app.DB)
	mine := testutil.CreateTestOrganization(t, app.DB)

	rule := createAutomationVia(t, app, theirs, adminFor(t, app, theirs), tagAutomationBody(nil))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, mine.ID, adminFor(t, app, mine).ID)
	testutil.SetPathParam(req, "id", rule.ID)

	require.NoError(t, app.GetAutomation(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}
