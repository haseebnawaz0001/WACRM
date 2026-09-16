package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func segmentOrg(t *testing.T, app *handlers.App) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	return org
}

func decodeSegment(t *testing.T, body []byte) handlers.SegmentResponse {
	t.Helper()
	var result struct {
		Data struct {
			Segment handlers.SegmentResponse `json:"segment"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Segment
}

func createSegmentVia(t *testing.T, app *handlers.App, org *models.Organization, user *models.User, body map[string]any) handlers.SegmentResponse {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateSegment(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	return decodeSegment(t, testutil.GetResponseBody(req))
}

func vipBody(name string) map[string]any {
	return map[string]any{
		"name":   name,
		"filter": map[string]any{"field": "tags", "operator": "contains_any", "value": []string{"VIP"}},
	}
}

func TestCreateSegment_SavesTheFilter(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP customers"))

	assert.Equal(t, "VIP customers", segment.Name)
	assert.Equal(t, "tags", segment.Filter.Field)
}

// A segment that holds a filter which fails when somebody later runs it shows
// up as a broken campaign rather than a bad save.
func TestCreateSegment_RejectsAFilterOnAnUnknownField(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, map[string]any{
		"name":   "Broken",
		"filter": map[string]any{"field": "favourite_colour", "operator": "equals", "value": "blue"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateSegment(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestCreateSegment_RejectsADuplicateName(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	createSegmentVia(t, app, org, admin, vipBody("VIP"))

	req := testutil.NewJSONRequest(t, vipBody("vip"))
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.CreateSegment(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// Someone else's private segment should not be discoverable by probing ids.
func TestGetSegment_HidesSomebodyElsesPrivateSegment(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	owner := adminFor(t, app, org)
	other := adminFor(t, app, org)

	body := vipBody("My own list")
	body["visibility"] = models.SegmentPrivate
	segment := createSegmentVia(t, app, org, owner, body)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, other.ID)
	testutil.SetPathParam(req, "id", segment.ID)

	require.NoError(t, app.GetSegment(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestSegmentContacts_ReturnsTheMembers(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	member := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559600001"), testutil.WithTags("VIP"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559600002"))

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))

	req := testutil.NewJSONRequest(t, map[string]any{"limit": 50})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", segment.ID)

	require.NoError(t, app.SegmentContacts(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Total    int64 `json:"total"`
			Contacts []struct {
				ID string `json:"id"`
			} `json:"contacts"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, int64(1), result.Data.Total)
	require.Len(t, result.Data.Contacts, 1)
	assert.Equal(t, member.ID.String(), result.Data.Contacts[0].ID)
}

// The builder shows a live count while the filter is being written, before
// anything is saved.
func TestPreviewSegmentCount_CountsAnUnsavedFilter(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559600003"), testutil.WithTags("VIP"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"filter": map[string]any{"field": "tags", "operator": "contains_any", "value": []string{"VIP"}},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.PreviewSegmentCount(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Count int64 `json:"count"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, int64(1), result.Data.Count)
}

// Saving an audience a campaign will message is a supervisor's decision.
func TestCreateSegment_RequiresTheWritePermission(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)

	role := testutil.CreateAgentRole(t, app.DB, org.ID)
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewJSONRequest(t, vipBody("Sneaky"))
	testutil.SetAuthContext(req, org.ID, agent.ID)

	require.Error(t, app.CreateSegment(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

// A segment another segment points at cannot simply go: the message names the
// dependents so the caller can unpick them.
func TestDeleteSegment_ConflictsWhileReferenced(t *testing.T) {
	app := newTestApp(t)
	org := segmentOrg(t, app)
	admin := adminFor(t, app, org)

	base := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	createSegmentVia(t, app, org, admin, map[string]any{
		"name": "VIP with email",
		"filter": map[string]any{
			"op": "and",
			"rules": []any{
				map[string]any{"field": "segment", "operator": "in", "value": base.ID},
				map[string]any{"field": "profile_name", "operator": "is_not_empty"},
			},
		},
	})

	req := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", base.ID)

	require.NoError(t, app.DeleteSegment(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))
}
