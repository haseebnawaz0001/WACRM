package handlers_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Plan 05: a segment is an audience an organization has already defined.
// Exporting it should not mean rebuilding the same filter by hand in the
// export dialog and hoping the two agree.
func TestExportSegment_ReturnsOnlyTheSegmentsContacts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, app.DB.Exec(`DELETE FROM contacts WHERE organization_id = ?`, org.ID).Error)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550201"),
		testutil.WithProfileName("In The Segment"),
		testutil.WithTags("vip"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550202"),
		testutil.WithProfileName("Not In It"))

	segment := &models.Segment{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Name:           "VIPs",
		Filter: models.JSONB{
			"op": "and",
			"rules": []any{
				map[string]any{"field": "tags", "operator": "contains_any", "value": []any{"vip"}},
			},
		},
		Visibility:  models.SegmentShared,
		CreatedByID: admin.ID,
	}
	require.NoError(t, app.DB.Create(segment).Error)

	req := testutil.NewJSONRequest(t, map[string]any{})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", segment.ID.String())

	require.NoError(t, app.ExportSegment(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req),
		string(testutil.GetResponseBody(req)))

	body := string(testutil.GetResponseBody(req))
	assert.Contains(t, body, "In The Segment")
	assert.NotContains(t, body, "Not In It", "an export aimed at a segment must not carry the rest of the list")
	assert.Equal(t, 2, strings.Count(strings.TrimSpace(body), "\n")+1,
		"a header and one row")
}
