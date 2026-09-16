package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// A tag name with a double quote is ordinary — `12" pipe`, `6" x 4" card`.
// Renaming and deleting used to build the JSON comparison by wrapping the name
// in quotes in Go, which produced an invalid literal for these names: the tag
// row changed but the contacts kept the old tag (plan 10, X10).
const quotedTag = `12" pipe`

func contactTags(t *testing.T, app *handlers.App, contactID uuid.UUID) []string {
	t.Helper()
	var c models.Contact
	require.NoError(t, app.DB.Where("id = ?", contactID).First(&c).Error)
	out := make([]string, 0, len(c.Tags))
	for _, v := range c.Tags {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestApp_UpdateTag_QuotedNameRetagsContacts(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	createTestTag(t, app, org.ID, quotedTag, "blue")

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("tags", models.JSONBArray{quotedTag, "Keep"}).Error)

	req := testutil.NewJSONRequest(t, map[string]any{"name": `14" pipe`, "color": "blue"})
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "name", quotedTag)

	require.NoError(t, app.UpdateTag(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	// The contact must have followed the rename, and its other tag survived.
	assert.ElementsMatch(t, []string{`14" pipe`, "Keep"}, contactTags(t, app, contact.ID))
}

func TestApp_DeleteTag_QuotedNameUntagsContacts(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	createTestTag(t, app, org.ID, quotedTag, "blue")

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("tags", models.JSONBArray{quotedTag, "Keep"}).Error)

	req := testutil.NewJSONRequest(t, map[string]any{})
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "name", quotedTag)

	require.NoError(t, app.DeleteTag(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	assert.Equal(t, []string{"Keep"}, contactTags(t, app, contact.ID))
}
