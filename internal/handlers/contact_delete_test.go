package handlers_test

import (
	"context"
	"testing"

	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Plan 10, S2: a contact a person deleted stays deleted. The handler used to
// soft-delete the row directly, so no reason was recorded and the restore
// policy — which refuses only deletions it knows a person made — let the next
// message from that number bring the whole record back. The cascade never ran
// either, leaving the contact's transfer holding a queue place nobody could
// pick up.
func TestDeleteContact_StaysDeletedAndEndsWorkInFlight(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "contact-deleter",
		[]string{"contacts:read", "contacts:delete"})
	user := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550199"), testutil.WithContactAccount(account.Name))
	transfer := createTestTransfer(t, app, org.ID, contact.ID, account.Name, models.TransferStatusActive, nil)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())
	require.NoError(t, app.DeleteContact(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var deleted models.Contact
	require.NoError(t, app.DB.Unscoped().First(&deleted, contact.ID).Error)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.Equal(t, contacts.ReasonUser, deleted.DeletedReason,
		"without the reason the restore policy cannot tell a person's deletion from a sync's")

	var after models.AgentTransfer
	require.NoError(t, app.DB.First(&after, transfer.ID).Error)
	assert.Equal(t, models.TransferStatusExpired, after.Status,
		"a transfer for a deleted contact can never be picked up")

	var events int64
	require.NoError(t, app.DB.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND type = ? AND subject_id = ?", org.ID, "contact.deleted", contact.ID).
		Count(&events).Error)
	assert.EqualValues(t, 1, events)

	// The same number writes in again, through the path inbound messages use.
	again, _, err := app.Contacts().Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550199"},
		contacts.ResolveOpts{CreateIfMissing: true, AllowRestore: true, Source: contacts.SourceInbound})
	require.NoError(t, err)
	assert.NotEqual(t, contact.ID, again.ID,
		"a deleted contact must not come back with its history because the customer wrote again")
}
