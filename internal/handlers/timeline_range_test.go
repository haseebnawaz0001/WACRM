package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plan 02 asks for a date range on the timeline. Without one the only way to
// reach "what happened in March" on a long-running customer is to page back
// through everything since.
func TestGetContactTimeline_FiltersByDateRange(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithAdminRole(t, app.DB, org.ID))
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	march := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	june := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{march, june} {
		require.NoError(t, app.DB.Create(&models.ContactActivity{
			ID:             uuid.New(),
			OrganizationID: org.ID,
			ContactID:      contact.ID,
			Type:           "contact.tag_added",
			ActorType:      "system",
			Data:           models.JSONB{"tag": at.Format("Jan")},
			OccurredAt:     at,
		}).Error)
	}

	assert.Len(t, timelineItems(t, app, org.ID, user.ID, contact.ID, "", ""), 2)
	assert.Len(t, timelineItems(t, app, org.ID, user.ID, contact.ID, "2026-03-01", "2026-03-31"), 1)
	assert.Len(t, timelineItems(t, app, org.ID, user.ID, contact.ID, "2026-01-01", "2026-02-28"), 0)

	// The end date is inclusive: "to the 15th" has to include the 15th, or a
	// range that ends today silently drops today.
	assert.Len(t, timelineItems(t, app, org.ID, user.ID, contact.ID, "2026-03-15", "2026-03-15"), 1)
}

func timelineItems(t *testing.T, app *handlers.App, orgID, userID, contactID uuid.UUID, from, to string) []map[string]any {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, orgID, userID)
	testutil.SetPathParam(req, "id", contactID.String())
	if from != "" {
		testutil.SetQueryParam(req, "from", from)
	}
	if to != "" {
		testutil.SetQueryParam(req, "to", to)
	}
	require.NoError(t, app.GetContactTimeline(req))

	var body struct {
		Items []map[string]any `json:"items"`
	}
	testutil.ParseEnvelopeResponse(t, req, &body)
	return body.Items
}
