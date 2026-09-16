package handlers_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"
)

// outboxRows returns the events recorded for one organization, oldest first.
func outboxRows(t *testing.T, db *gorm.DB, orgID any) []models.CRMEventOutbox {
	t.Helper()
	var rows []models.CRMEventOutbox
	require.NoError(t, db.Where("organization_id = ?", orgID).
		Order("occurred_at").Find(&rows).Error)
	return rows
}

// Creating a transfer through the real handler must leave a durable event
// behind. Before the outbox this was a goroutine, so a crash between the
// commit and the dispatch lost the webhook with no trace.
func TestCreateAgentTransfer_WritesOutboxEvent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	adminRole := testutil.CreateAdminRole(t, app.DB, org.ID)
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&adminRole.ID))
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id":       contact.ID.String(),
		"whatsapp_account": account.Name,
		"notes":            "Outbox check",
		"source":           models.TransferSourceManual,
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.CreateAgentTransfer(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	rows := outboxRows(t, app.DB, org.ID)
	require.Len(t, rows, 1, "creating a transfer records exactly one event")

	row := rows[0]
	assert.Equal(t, string(models.WebhookEventTransferCreated), row.Type)
	assert.Nil(t, row.PublishedAt, "the handler records the event; the relay publishes it")

	require.NotNil(t, row.ContactID)
	assert.Equal(t, contact.ID, *row.ContactID, "the event belongs to the contact's history")
	assert.Equal(t, crmevents.SubjectTransfer, row.SubjectType)
	require.NotNil(t, row.SubjectID)

	// The payload must keep the exact field names external receivers already
	// consume, or migrating the call site silently breaks their integrations.
	assert.Equal(t, contact.ID.String(), row.Data["contact_id"])
	assert.Equal(t, contact.PhoneNumber, row.Data["contact_phone"])
	assert.Equal(t, "Outbox check", row.Data["reason"])
	assert.Equal(t, account.Name, row.Data["whatsapp_account"])
	assert.Equal(t, string(models.TransferSourceManual), row.Data["source"])
	assert.Equal(t, row.SubjectID.String(), row.Data["transfer_id"])
}

// Every event a handler records must be one the catalog knows, otherwise the
// relay would drop it silently.
func TestMigratedCallSites_UseKnownCatalogEvents(t *testing.T) {
	for _, e := range []models.WebhookEvent{
		models.WebhookEventMessageIncoming,
		models.WebhookEventMessageOutgoing,
		models.WebhookEventMessageSent,
		models.WebhookEventContactCreated,
		models.WebhookEventTransferCreated,
		models.WebhookEventTransferResumed,
		models.WebhookEventTransferAssigned,
	} {
		assert.True(t, crmevents.IsKnown(string(e)),
			"%s is dispatched by a handler but missing from the catalog", e)
	}
}
