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

func decodeCandidates(t *testing.T, body []byte) []handlers.DuplicateCandidateResponse {
	t.Helper()
	var result struct {
		Data struct {
			Candidates []handlers.DuplicateCandidateResponse `json:"candidates"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Candidates
}

// Two records for one person is a problem that grows quietly; nobody goes
// looking for it by hand.
func TestScanDuplicates_FindsAPairSharingANumber(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	// The same number written two ways.
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700001"), testutil.WithProfileName("Ann"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("+1 555 970 0001"), testutil.WithProfileName("Ann Smith"))

	scan := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(scan, org.ID, admin.ID)
	require.NoError(t, app.ScanDuplicates(scan))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(scan))

	list := testutil.NewGETRequest(t)
	testutil.SetAuthContext(list, org.ID, admin.ID)
	require.NoError(t, app.ListDuplicates(list))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(list))

	candidates := decodeCandidates(t, testutil.GetResponseBody(list))
	require.Len(t, candidates, 1)
	assert.NotEmpty(t, candidates[0].Reasons,
		"somebody deciding needs to see why, not be asked to trust a number")
	assert.NotEmpty(t, candidates[0].ContactA.PhoneNumber)
	assert.NotEmpty(t, candidates[0].ContactB.PhoneNumber)
}

// Re-suggesting a pair somebody has already said no to is how a review queue
// becomes something nobody opens.
func TestDismissDuplicate_KeepsThePairOutOfTheQueue(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700002"), testutil.WithProfileName("Bea"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("+1 555 970 0002"), testutil.WithProfileName("Bea Jones"))

	scan := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(scan, org.ID, admin.ID)
	require.NoError(t, app.ScanDuplicates(scan))

	list := testutil.NewGETRequest(t)
	testutil.SetAuthContext(list, org.ID, admin.ID)
	require.NoError(t, app.ListDuplicates(list))
	candidates := decodeCandidates(t, testutil.GetResponseBody(list))
	require.Len(t, candidates, 1)

	dismiss := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(dismiss, org.ID, admin.ID)
	testutil.SetPathParam(dismiss, "id", candidates[0].ID)
	require.NoError(t, app.DismissDuplicate(dismiss))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(dismiss))

	// Scanning again must not resurrect it.
	rescan := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(rescan, org.ID, admin.ID)
	require.NoError(t, app.ScanDuplicates(rescan))

	after := testutil.NewGETRequest(t)
	testutil.SetAuthContext(after, org.ID, admin.ID)
	require.NoError(t, app.ListDuplicates(after))
	assert.Empty(t, decodeCandidates(t, testutil.GetResponseBody(after)))
}

func TestMergeContacts_FoldsOneIntoTheOther(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	primary := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700003"), testutil.WithProfileName("Cal"))
	secondary := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700004"), testutil.WithProfileName("Cal Roy"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"primary_id":   primary.ID.String(),
		"secondary_id": secondary.ID.String(),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.MergeContacts(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	// The secondary keeps its row, soft-deleted and pointing at the survivor,
	// so a message arriving for its number still reaches the right person.
	var merged models.Contact
	require.NoError(t, app.DB.Unscoped().Where("id = ?", secondary.ID).First(&merged).Error)
	require.NotNil(t, merged.MergedIntoID)
	assert.Equal(t, primary.ID, *merged.MergedIntoID)
	assert.NotNil(t, merged.DeletedAt)
}

// A merge removes a record from the list, so the person doing it has to be
// allowed to do that.
func TestMergeContacts_RequiresDeletePermission(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)

	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "editor",
		[]string{"contacts:read", "contacts:write"})
	editor := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewJSONRequest(t, map[string]any{
		"primary_id":   testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15559700005")).ID.String(),
		"secondary_id": testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15559700006")).ID.String(),
	})
	testutil.SetAuthContext(req, org.ID, editor.ID)

	require.Error(t, app.MergeContacts(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

// Merging a contact into itself is a mistake worth naming rather than a
// silently successful no-op.
func TestMergeContacts_RejectsMergingARecordIntoItself(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700007"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"primary_id":   contact.ID.String(),
		"secondary_id": contact.ID.String(),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.MergeContacts(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// A record that suddenly has somebody else's history has to be able to explain
// itself.
func TestContactMergeHistory_ShowsWhatWasFoldedIn(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	primary := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700008"))
	secondary := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559700009"))

	merge := testutil.NewJSONRequest(t, map[string]any{
		"primary_id":   primary.ID.String(),
		"secondary_id": secondary.ID.String(),
	})
	testutil.SetAuthContext(merge, org.ID, admin.ID)
	require.NoError(t, app.MergeContacts(merge))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", primary.ID.String())

	require.NoError(t, app.ContactMergeHistory(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Merges []models.ContactMerge `json:"merges"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.Merges, 1)
	assert.Equal(t, secondary.ID, result.Data.Merges[0].SecondaryContactID)
}
