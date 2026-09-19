package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func measureTestUser(t *testing.T, app *handlers.App, name string) (*models.Organization, *models.User) {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateTestRoleExact(t, app.DB, org.ID, "Analytics", false, false, getAnalyticsPermissions(t, app))
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithEmail(testutil.UniqueEmail(name)),
		testutil.WithPassword("password"), testutil.WithRoleID(&role.ID))
	return org, user
}

// The builder is offered every measure, with each filter's choices filled in
// for the organization — a person picks an agent from a list rather than
// typing an id.
func TestApp_GetWidgetCatalog(t *testing.T) {
	app := newTestApp(t)
	org, user := measureTestUser(t, app, "catalog")

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.GetWidgetCatalog(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var resp struct {
		Data struct {
			Measures []struct {
				Key   string   `json:"key"`
				Views []string `json:"views"`
				Dims  []struct {
					Key     string `json:"key"`
					Kind    string `json:"kind"`
					Options []struct {
						Value string `json:"value"`
						Label string `json:"label"`
					} `json:"options"`
				} `json:"dims"`
			} `json:"measures"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	require.NotEmpty(t, resp.Data.Measures)

	var waiting bool
	for _, m := range resp.Data.Measures {
		if m.Key != "conversations_waiting" {
			continue
		}
		waiting = true
		assert.NotContains(t, m.Views, "trend", "a count of right now has no trend")
		for _, d := range m.Dims {
			if d.Key == "assignee" {
				require.NotEmpty(t, d.Options, "the assignee filter offers the organization's people")
				assert.Equal(t, user.FullName, d.Options[0].Label)
			}
		}
	}
	assert.True(t, waiting)
}

// A widget can be computed before it is saved, which is what lets the builder
// show it while it is being made.
func TestApp_PreviewWidget(t *testing.T) {
	app := newTestApp(t)
	org, user := measureTestUser(t, app, "preview")

	req := testutil.NewJSONRequest(t, map[string]any{
		"config":  map[string]any{"measure": "tasks_open", "view": "number"},
		"filters": []map[string]string{{"field": "owner", "operator": "equals", "value": "me"}},
	})
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.PreviewWidget(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var resp struct {
		Data handlers.WidgetDataResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	assert.True(t, resp.Data.Snapshot, "open follow-ups are counted as of now")

	// Something the measure cannot do is refused with a reason.
	bad := testutil.NewJSONRequest(t, map[string]any{
		"config": map[string]any{"measure": "tasks_open", "view": "trend"},
	})
	testutil.SetAuthContext(bad, org.ID, user.ID)
	require.NoError(t, app.PreviewWidget(bad))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(bad))
}

// Saving a measure widget stores the measure and fills in the columns the rest
// of the widget code reads; editing it replaces it wholesale.
func TestApp_CreateAndUpdateMeasureWidget(t *testing.T) {
	app := newTestApp(t)
	org, user := measureTestUser(t, app, "measure-widget")

	create := testutil.NewJSONRequest(t, map[string]any{
		"name":           "Deals by stage",
		"config":         map[string]any{"measure": "deals_open", "view": "bar"},
		"group_by_field": "stage",
		"is_shared":      true,
	})
	testutil.SetAuthContext(create, org.ID, user.ID)
	require.NoError(t, app.CreateWidget(create))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(create), string(testutil.GetResponseBody(create)))

	var created struct {
		Data handlers.WidgetResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(create), &created))
	assert.Equal(t, "measure", created.Data.DataSource)
	assert.Equal(t, "chart", created.Data.DisplayType)
	assert.Equal(t, "bar", created.Data.ChartType)
	assert.Equal(t, "stage", created.Data.GroupByField)
	assert.Equal(t, "green", created.Data.Color, "a deals widget takes the deals colour")
	assert.Equal(t, 6, created.Data.GridW, "a chart gets room to be read")

	update := testutil.NewJSONRequest(t, map[string]any{
		"name":   "Open deals",
		"config": map[string]any{"measure": "deals_open", "view": "number"},
	})
	testutil.SetAuthContext(update, org.ID, user.ID)
	testutil.SetPathParam(update, "id", created.Data.ID.String())
	require.NoError(t, app.UpdateWidget(update))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(update), string(testutil.GetResponseBody(update)))

	var updated struct {
		Data handlers.WidgetResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(update), &updated))
	assert.Equal(t, "Open deals", updated.Data.Name)
	assert.Equal(t, "number", updated.Data.DisplayType)
	assert.Empty(t, updated.Data.GroupByField, "a number is not split")
	assert.True(t, updated.Data.IsShared, "sharing is kept when the edit does not mention it")
}
