package handlers_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/activity"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/reports"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func reportOrg(t *testing.T, app *handlers.App) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	return org
}

func TestContactsBySourceReport_ReturnsTotals(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559500001"))
	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeySource: "inbound"}, nil)
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ContactsBySourceReport(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data reports.ContactsBySource `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, int64(1), result.Data.Total)
	require.Len(t, result.Data.Totals, 1)
	assert.Equal(t, "inbound", result.Data.Totals[0].Source)
}

// Reading a team's reports means reading everyone's numbers, which is not an
// agent permission.
func TestContactsBySourceReport_RequiresTheReportPermission(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)

	role := testutil.CreateAgentRole(t, app.DB, org.ID)
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, agent.ID)

	require.Error(t, app.ContactsBySourceReport(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

func TestLifecycleFunnelReport_ReturnsTheOrgsOwnStages(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559500002"))
	require.NoError(t, activity.Record(app.DB, activity.Entry{
		OrgID: org.ID, ContactID: contact.ID, Type: "contact.field_changed",
		Actor: crmevents.SystemActor(),
		Data:  map[string]any{"field": models.FieldKeyLifecycleStage, "to": "lead"},
	}))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.LifecycleFunnelReport(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data reports.LifecycleFunnel `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.NotEmpty(t, result.Data.Steps)
	assert.NotEmpty(t, result.Data.Note)
}

// A spreadsheet is where numbers go to be argued with; refusing to hand them
// over just means somebody retypes them.
func TestExportReport_WritesCSV(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559500003"))
	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeySource: "import"}, nil)
	require.NoError(t, err)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "key", "contacts-by-source")

	require.NoError(t, app.ExportReport(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	body := string(testutil.GetResponseBody(req))
	assert.Contains(t, body, "source,contacts,share_percent,became_customer")
	assert.Contains(t, body, "import")
	assert.Contains(t, string(req.RequestCtx.Response.Header.Peek("Content-Type")), "text/csv")
	assert.Contains(t, string(req.RequestCtx.Response.Header.Peek("Content-Disposition")), "attachment")
}

func TestExportReport_RejectsAnUnknownReport(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "key", "everything")

	require.NoError(t, app.ExportReport(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// Reading the pipeline report must not be a way around not being allowed to
// see deals.
func TestPipelineFunnelReport_AlsoRequiresDealAccess(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)

	// A role with reports but no deals.
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "analyst",
		[]string{"reports:read", "reports:export"})
	analyst := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, analyst.ID)

	require.NoError(t, app.PipelineFunnelReport(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

func TestAgentPerformanceReport_ReturnsRows(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559500004"))

	opened := time.Now().UTC().Add(-2 * time.Hour)
	responded := opened.Add(5 * time.Minute)
	require.NoError(t, app.DB.Create(&models.Conversation{
		BaseModel:              models.BaseModel{ID: uuid.New()},
		OrganizationID:         org.ID,
		ContactID:              contact.ID,
		Status:                 models.ConversationOpen,
		WhatsAppAccount:        "acct",
		OpenedAt:               opened,
		FirstCustomerMessageAt: &opened,
		FirstResponseAt:        &responded,
		FirstResponderID:       &admin.ID,
	}).Error)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.AgentPerformanceReport(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data reports.AgentPerformance `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.Rows, 1)
	require.NotNil(t, result.Data.Rows[0].FirstResponseMedianSeconds)
	assert.InDelta(t, 300, *result.Data.Rows[0].FirstResponseMedianSeconds, 1)
}

// Dates are the business's dates: a report bucketed in UTC puts a Monday
// morning in Karachi into Sunday.
func TestReports_RangeUsesTheOrganizationTimezone(t *testing.T) {
	app := newTestApp(t)
	org := reportOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetQueryParam(req, "from", "2026-03-01")
	testutil.SetQueryParam(req, "to", "2026-03-31")
	testutil.SetPathParam(req, "key", "lifecycle-funnel")

	require.NoError(t, app.ExportReport(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	disposition := string(req.RequestCtx.Response.Header.Peek("Content-Disposition"))
	assert.True(t, strings.Contains(disposition, "2026-03-01"),
		"the filename names the range the user asked for")
}
