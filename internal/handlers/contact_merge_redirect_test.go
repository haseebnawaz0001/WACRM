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

// Plan 06: visiting a merged contact's URL must land on the survivor.
//
// Returning the emptied secondary instead would show a record whose messages,
// tasks and deals have all moved — technically the row that was asked for, and
// useless to whoever followed the link.
func TestGetContact_MergedContactResolvesToSurvivor(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	survivor := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithProfileName("Ada Lovelace"), testutil.WithPhoneNumber("14155550001"))
	secondary := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithProfileName("A. Lovelace"), testutil.WithPhoneNumber("14155550002"))

	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", secondary.ID).
		Update("merged_into_id", survivor.ID).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", secondary.ID.String())

	require.NoError(t, app.GetContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var resp struct {
		Data handlers.ContactResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))

	assert.Equal(t, survivor.ID, resp.Data.ID, "the survivor's record should be returned")
	assert.Equal(t, "Ada Lovelace", resp.Data.ProfileName)
	require.NotNil(t, resp.Data.MergedIntoID, "the client needs the pointer to rewrite the URL")
	assert.Equal(t, survivor.ID, *resp.Data.MergedIntoID)
}

// An ordinary contact carries no pointer, so the client does not redirect.
func TestGetContact_UnmergedContactHasNoPointer(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.GetContact(req))

	var resp struct {
		Data handlers.ContactResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &resp))
	assert.Equal(t, contact.ID, resp.Data.ID)
	assert.Nil(t, resp.Data.MergedIntoID)
}
