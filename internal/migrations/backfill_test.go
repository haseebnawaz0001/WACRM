package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/migrations"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertLegacyMessage writes a row the way the code did before sender_type
// existed: the column is left NULL.
func insertLegacyMessage(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, direction string, sentBy *uuid.UUID, metadata string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO messages (id, organization_id, whats_app_account, contact_id,
			direction, message_type, content, status, sent_by_user_id, metadata,
			created_at, updated_at)
		VALUES (?, ?, 'acct', ?, ?, 'text', 'hello', 'sent', ?, ?::jsonb, now(), now())`,
		id, orgID, contactID, direction, sentBy, metadata).Error)
	return id
}

func senderTypeOf(t *testing.T, db *gorm.DB, id uuid.UUID) string {
	t.Helper()
	var got *string
	require.NoError(t, db.Raw(`SELECT sender_type FROM messages WHERE id = ?`, id).Scan(&got).Error)
	if got == nil {
		return "<null>"
	}
	return *got
}

// senderTypeBackfill returns the registered backfill migration by name, so the
// test exercises exactly what ships rather than a copy of its logic.
func senderTypeBackfill(t *testing.T) func(*gorm.DB) error {
	t.Helper()
	const name = "2026_09_16_backfill_message_sender_type"
	for _, m := range migrations.Registered() {
		if m.Name == name {
			return m.Run
		}
	}
	t.Fatalf("migration %s is not registered", name)
	return nil
}

// Rows written before the column existed still have to be attributable, or
// every historical response-time metric silently changes meaning.
func TestBackfillMessageSenderType_InfersFromExistingColumns(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	inbound := insertLegacyMessage(t, db, org.ID, contact.ID, "incoming", nil, `{}`)
	campaign := insertLegacyMessage(t, db, org.ID, contact.ID, "outgoing", nil, `{"campaign_id":"abc"}`)
	agent := insertLegacyMessage(t, db, org.ID, contact.ID, "outgoing", &user.ID, `{}`)
	automated := insertLegacyMessage(t, db, org.ID, contact.ID, "outgoing", nil, `{}`)

	require.NoError(t, db.Transaction(senderTypeBackfill(t)))

	assert.Equal(t, "contact", senderTypeOf(t, db, inbound),
		"an inbound message is always from the customer")
	assert.Equal(t, "campaign", senderTypeOf(t, db, campaign),
		"campaign metadata identifies a bulk send")
	assert.Equal(t, "agent", senderTypeOf(t, db, agent),
		"an outgoing message with a sending user was a person in the inbox")
	assert.Equal(t, "system", senderTypeOf(t, db, automated),
		"anything else outgoing was produced by the product")
}

// A campaign send also carries no sending user, so the campaign rule has to be
// checked before the catch-all; this pins that ordering.
func TestBackfillMessageSenderType_CampaignWinsOverSystemFallback(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	id := insertLegacyMessage(t, db, org.ID, contact.ID, "outgoing", nil, `{"campaign_id":"xyz"}`)
	require.NoError(t, db.Transaction(senderTypeBackfill(t)))

	assert.Equal(t, "campaign", senderTypeOf(t, db, id))
}

// Re-running must not reclassify rows that already have a sender, otherwise a
// message correctly recorded as bot or automation would be rewritten.
func TestBackfillMessageSenderType_LeavesExistingValuesAlone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	id := insertLegacyMessage(t, db, org.ID, contact.ID, "outgoing", &user.ID, `{}`)
	require.NoError(t, db.Exec(`UPDATE messages SET sender_type = 'bot' WHERE id = ?`, id).Error)

	require.NoError(t, db.Transaction(senderTypeBackfill(t)))

	assert.Equal(t, "bot", senderTypeOf(t, db, id),
		"the backfill only fills NULLs; it must never overwrite a known sender")
}

// Existing contacts must join the normalised lookup, or an import in a
// different phone format duplicates a contact that is already there.
func TestBackfillContactPhoneNormalized(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	plain := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("+92 321 1234567"))
	jid := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("120363422675615917@g.us"))

	// Clear the column to simulate rows written before it existed.
	require.NoError(t, db.Exec(`UPDATE contacts SET phone_normalized = NULL WHERE organization_id = ?`, org.ID).Error)

	require.NoError(t, db.Transaction(migrationByName(t, "2026_09_16_backfill_contact_phone_normalized")))

	var got string
	require.NoError(t, db.Raw(`SELECT coalesce(phone_normalized, '') FROM contacts WHERE id = ?`, plain.ID).Scan(&got).Error)
	assert.Equal(t, "923211234567", got, "formatting is stripped for matching")

	require.NoError(t, db.Raw(`SELECT coalesce(phone_normalized, '') FROM contacts WHERE id = ?`, jid.ID).Scan(&got).Error)
	assert.Empty(t, got, "group JIDs are not phone numbers and are left alone")
}

// migrationByName returns a registered migration's Run function.
func migrationByName(t *testing.T, name string) func(*gorm.DB) error {
	t.Helper()
	for _, m := range migrations.Registered() {
		if m.Name == name {
			return m.Run
		}
	}
	t.Fatalf("migration %s is not registered", name)
	return nil
}
