package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func activeTransfer(orgID, contactID uuid.UUID, account string) *models.AgentTransfer {
	return &models.AgentTransfer{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: account,
		PhoneNumber:     "+15550001111",
		Status:          models.TransferStatusActive,
		Source:          models.TransferSourceManual,
	}
}

// "One active transfer per contact" was enforced by counting rows and then
// inserting. Inbound webhooks run in their own goroutines, so two of them could
// both pass the count and both insert, leaving the contact with two active
// transfers and no defined assignee. The database now refuses the second.
func TestOneActiveTransferPerContact_IsEnforcedByTheDatabase(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	require.NoError(t, app.DB.Create(activeTransfer(org.ID, contact.ID, account.Name)).Error)

	err := app.DB.Create(activeTransfer(org.ID, contact.ID, account.Name)).Error
	require.Error(t, err, "a second active transfer for the same contact must be rejected")

	var active int64
	require.NoError(t, app.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			org.ID, contact.ID, models.TransferStatusActive).Count(&active).Error)
	assert.EqualValues(t, 1, active)
}

// The constraint must only cover active transfers: a contact legitimately
// accumulates many resumed and expired ones over time.
func TestClosedTransfersDoNotBlockANewActiveOne(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)

	for i := 0; i < 3; i++ {
		past := activeTransfer(org.ID, contact.ID, account.Name)
		past.Status = models.TransferStatusResumed
		require.NoError(t, app.DB.Create(past).Error)
	}

	assert.NoError(t, app.DB.Create(activeTransfer(org.ID, contact.ID, account.Name)).Error,
		"a contact with past transfers can still get a new one")
}

// Different contacts are unrelated.
func TestActiveTransfersAreScopedPerContact(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	first := testutil.CreateTestContact(t, app.DB, org.ID)
	second := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15550002222"))

	require.NoError(t, app.DB.Create(activeTransfer(org.ID, first.ID, account.Name)).Error)
	assert.NoError(t, app.DB.Create(activeTransfer(org.ID, second.ID, account.Name)).Error)
}
