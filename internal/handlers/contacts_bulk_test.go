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
)

type bulkResult struct {
	Data struct {
		Updated int `json:"updated"`
		Skipped int `json:"skipped"`
	} `json:"data"`
}

func bulkUpdate(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, body map[string]any) (bulkResult, int) {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, orgID, userID)
	require.NoError(t, app.BulkUpdateContacts(req))

	var out bulkResult
	_ = json.Unmarshal(testutil.GetResponseBody(req), &out)
	return out, testutil.GetResponseStatusCode(req)
}

func tagsOf(t *testing.T, app *handlers.App, id uuid.UUID) []string {
	t.Helper()
	var contact models.Contact
	require.NoError(t, app.DB.Where("id = ?", id).First(&contact).Error)
	out := make([]string, 0, len(contact.Tags))
	for _, raw := range contact.Tags {
		if tag, ok := raw.(string); ok {
			out = append(out, tag)
		}
	}
	return out
}

// Retagging fifty contacts after an import used to mean fifty trips through
// the detail page, which is why people did it in a spreadsheet and re-imported
// — losing everything the record had accumulated.
func TestBulkUpdateContacts_AddsAndRemovesTags(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	first := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330001"))
	second := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330002"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", second.ID).
		Update("tags", models.JSONBArray{"stale"}).Error)

	result, status := bulkUpdate(t, app, org.ID, admin.ID, map[string]any{
		"contact_ids": []string{first.ID.String(), second.ID.String()},
		"add_tags":    []string{"imported"},
		"remove_tags": []string{"stale"},
	})

	require.Equal(t, fasthttp.StatusOK, status)
	assert.Equal(t, 2, result.Data.Updated)

	assert.Contains(t, tagsOf(t, app, first.ID), "imported")
	assert.Contains(t, tagsOf(t, app, second.ID), "imported")
	assert.NotContains(t, tagsOf(t, app, second.ID), "stale")
}

// Adding a tag a contact already has is not a change, and reporting it as one
// would make the result count meaningless.
func TestBulkUpdateContacts_IsIdempotent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330010"))

	body := map[string]any{
		"contact_ids": []string{contact.ID.String()},
		"add_tags":    []string{"vip"},
	}

	first, _ := bulkUpdate(t, app, org.ID, admin.ID, body)
	assert.Equal(t, 1, first.Data.Updated)

	second, _ := bulkUpdate(t, app, org.ID, admin.ID, body)
	assert.Equal(t, 0, second.Data.Updated, "nothing moved the second time")
	assert.Len(t, tagsOf(t, app, contact.ID), 1, "the tag is not added twice")
}

func TestBulkUpdateContacts_SetsTheOwner(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	owner := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330020"))

	result, status := bulkUpdate(t, app, org.ID, admin.ID, map[string]any{
		"contact_ids":      []string{contact.ID.String()},
		"assigned_user_id": owner.ID.String(),
	})
	require.Equal(t, fasthttp.StatusOK, status)
	assert.Equal(t, 1, result.Data.Updated)

	var stored models.Contact
	require.NoError(t, app.DB.Where("id = ?", contact.ID).First(&stored).Error)
	require.NotNil(t, stored.AssignedUserID)
	assert.Equal(t, owner.ID, *stored.AssignedUserID)
}

// The ids arrive in a request body, which is exactly where a viewer could name
// contacts the list would never have shown them.
func TestBulkUpdateContacts_SkipsContactsTheViewerCannotSee(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)

	// May edit contacts, may not see all of them: the scope, not the
	// permission, is what decides which rows this touches.
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "editor-"+uuid.NewString()[:8],
		[]string{"contacts:write", "chat:read", "chat:write"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	mine := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330030"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", mine.ID).
		Update("assigned_user_id", agent.ID).Error)
	theirs := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330031"))

	result, status := bulkUpdate(t, app, org.ID, agent.ID, map[string]any{
		"contact_ids": []string{mine.ID.String(), theirs.ID.String()},
		"add_tags":    []string{"seen"},
	})

	require.Equal(t, fasthttp.StatusOK, status)
	assert.Equal(t, 1, result.Data.Updated)
	assert.Equal(t, 1, result.Data.Skipped, "the caller is told, not silently given less")
	assert.NotContains(t, tagsOf(t, app, theirs.ID), "seen")
}

// A bulk action large enough to time out is a data migration, and belongs in
// an import where it can be staged.
func TestBulkUpdateContacts_RefusesTooManyContacts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	ids := make([]string, handlers.MaxBulkContacts+1)
	for i := range ids {
		ids[i] = uuid.NewString()
	}

	_, status := bulkUpdate(t, app, org.ID, admin.ID, map[string]any{
		"contact_ids": ids,
		"add_tags":    []string{"too-many"},
	})
	assert.Equal(t, fasthttp.StatusBadRequest, status)
}

// A malformed id fails the whole request: editing the subset that parsed would
// leave the caller believing all of them changed.
func TestBulkUpdateContacts_RefusesAMalformedID(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15553330040"))

	_, status := bulkUpdate(t, app, org.ID, admin.ID, map[string]any{
		"contact_ids": []string{contact.ID.String(), "not-an-id"},
		"add_tags":    []string{"nope"},
	})
	assert.Equal(t, fasthttp.StatusBadRequest, status)
	assert.Empty(t, tagsOf(t, app, contact.ID))
}

// Tagging one contact used to emit nothing, while tagging a hundred in bulk
// emitted an event each — so the same change reached the timeline, the webhook
// and the automation trigger only when it was done the less common way.
func TestUpdateContactTags_EmitsTheSameEventsAsBulk(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15558800001"), testutil.WithTags("keep", "drop"))

	req := testutil.NewJSONRequest(t, map[string]any{"tags": []string{"keep", "fresh"}})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.UpdateContactTags(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var events []struct {
		Type string
		Data string
	}
	require.NoError(t, app.DB.Raw(
		`SELECT type, data::text AS data FROM crm_event_outbox
		 WHERE contact_id = ? AND type IN ('contact.tag_added', 'contact.tag_removed')`,
		contact.ID).Scan(&events).Error)

	kinds := map[string]string{}
	for _, e := range events {
		kinds[e.Type] = e.Data
	}
	require.Contains(t, kinds, "contact.tag_added")
	require.Contains(t, kinds, "contact.tag_removed")
	assert.Contains(t, kinds["contact.tag_added"], "fresh")
	assert.Contains(t, kinds["contact.tag_removed"], "drop")
	assert.NotContains(t, kinds["contact.tag_added"], "keep",
		"a tag that was already there has not been added")
}

// Reordering the same tags is not a change, and must not raise events that an
// automation would act on.
func TestUpdateContactTags_ReorderingIsNotAChange(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15558800002"), testutil.WithTags("alpha", "beta"))

	req := testutil.NewJSONRequest(t, map[string]any{"tags": []string{"beta", "alpha"}})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.UpdateContactTags(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var count int64
	require.NoError(t, app.DB.Raw(
		`SELECT count(*) FROM crm_event_outbox
		 WHERE contact_id = ? AND type IN ('contact.tag_added', 'contact.tag_removed')`,
		contact.ID).Scan(&count).Error)
	assert.Zero(t, count)
}
