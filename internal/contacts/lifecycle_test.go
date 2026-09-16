package contacts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func inboundOpts() contacts.ResolveOpts {
	return contacts.ResolveOpts{
		CreateIfMissing: true,
		AllowRestore:    true,
		Source:          contacts.SourceInbound,
		UpdateName:      true,
		Actor:           crmevents.SystemActor(),
	}
}

// campaignOpts mirrors how the campaign worker resolves a recipient: it may
// create, but it must never restore and must never rename.
func campaignOpts(name string) contacts.ResolveOpts {
	return contacts.ResolveOpts{
		CreateIfMissing: true,
		AllowRestore:    false,
		Source:          contacts.SourceCampaign,
		ProfileName:     name,
		UpdateName:      false,
		Actor:           crmevents.SystemActor(),
	}
}

func softDelete(t *testing.T, db *gorm.DB, svc *contacts.Service, orgID, contactID uuid.UUID, reason string) {
	t.Helper()
	require.NoError(t, svc.Delete(context.Background(), orgID, contactID, reason, crmevents.SystemActor()))
}

// --- Lookup ---

func TestResolve_FindsExistingContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)
	existing := testutil.CreateTestContact(t, db, org.ID)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: existing.PhoneNumber}, inboundOpts())
	require.NoError(t, err)

	assert.Equal(t, contacts.OutcomeFound, outcome)
	assert.Equal(t, existing.ID, got.ID)
}

// The same number arrives formatted differently from Meta, a CSV and a user.
// Missing any of those forms creates a duplicate.
func TestResolve_MatchesAcrossPhoneFormatting(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	created, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923211234567"}, inboundOpts())
	require.NoError(t, err)
	require.Equal(t, contacts.OutcomeCreated, outcome)

	for _, variant := range []string{"+923211234567", "+92 321 1234567", "92-321-123 4567"} {
		got, outcome, err := svc.Resolve(context.Background(), org.ID,
			contacts.Identity{Phone: variant}, inboundOpts())
		require.NoError(t, err)
		assert.Equal(t, contacts.OutcomeFound, outcome, "%s should match the existing contact", variant)
		assert.Equal(t, created.ID, got.ID, "%s created a duplicate", variant)
	}
}

func TestResolve_CreatesWithNormalisedPhoneAndSource(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "+92 321 7654321"}, inboundOpts())
	require.NoError(t, err)

	assert.Equal(t, contacts.OutcomeCreated, outcome)
	assert.Equal(t, "923217654321", got.PhoneNormalized)
	assert.Equal(t, contacts.SourceInbound, got.Source)
}

func TestResolve_WithoutCreateReturnsNotFound(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	_, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923219999999"},
		contacts.ResolveOpts{CreateIfMissing: false})
	assert.ErrorIs(t, err, contacts.ErrNotFound)
}

// --- Restore policy (X4) ---

// A campaign send must never resurrect a deleted contact. This is the defect
// that made deletion meaningless: any outbound touch brought the record back.
func TestResolve_CampaignSendDoesNotRestoreDeletedContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)
	original := testutil.CreateTestContact(t, db, org.ID)
	softDelete(t, db, svc, org.ID, original.ID, contacts.ReasonUser)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: original.PhoneNumber}, campaignOpts("CSV Name"))
	require.NoError(t, err)

	assert.NotEqual(t, contacts.OutcomeRestored, outcome, "a campaign send must not restore")
	assert.NotEqual(t, original.ID, got.ID, "the deleted contact must stay deleted")

	var reloaded models.Contact
	require.NoError(t, db.Unscoped().First(&reloaded, "id = ?", original.ID).Error)
	assert.True(t, reloaded.DeletedAt.Valid, "the original record is still deleted")
}

// A contact a person deleted stays deleted even on an inbound message; the
// number gets a fresh record instead, which plan 06 later flags as a duplicate.
func TestResolve_InboundDoesNotRestoreUserDeletedContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)
	original := testutil.CreateTestContact(t, db, org.ID)
	softDelete(t, db, svc, org.ID, original.ID, contacts.ReasonUser)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: original.PhoneNumber}, inboundOpts())
	require.NoError(t, err)

	assert.Equal(t, contacts.OutcomeCreated, outcome)
	assert.NotEqual(t, original.ID, got.ID)
}

// An address-book-sync removal is the product's own bookkeeping, not a
// person's decision, so an inbound message may undo it.
func TestResolve_InboundRestoresAddressBookSyncDeletion(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)
	original := testutil.CreateTestContact(t, db, org.ID)
	softDelete(t, db, svc, org.ID, original.ID, contacts.ReasonAddressBookSync)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: original.PhoneNumber}, inboundOpts())
	require.NoError(t, err)

	assert.Equal(t, contacts.OutcomeRestored, outcome)
	assert.Equal(t, original.ID, got.ID)
	assert.False(t, got.DeletedAt.Valid)
}

// --- Merge pointer (plan 06 groundwork) ---

func TestResolve_FollowsMergePointerToSurvivor(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	survivor := testutil.CreateTestContact(t, db, org.ID)
	merged := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("923215550001"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", merged.ID).
		Update("merged_into_id", survivor.ID).Error)

	got, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: merged.PhoneNumber}, inboundOpts())
	require.NoError(t, err)

	assert.Equal(t, contacts.OutcomeFollowedMerge, outcome)
	assert.Equal(t, survivor.ID, got.ID, "messages for a merged number belong to the survivor")
}

// --- Profile name ---

// A recipient name from a campaign spreadsheet must not replace the name
// WhatsApp reported for that person.
func TestResolve_CampaignDoesNotOverwriteProfileName(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	created, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923215551111"},
		contacts.ResolveOpts{CreateIfMissing: true, UpdateName: true, ProfileName: "Real Name"})
	require.NoError(t, err)

	got, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923215551111"}, campaignOpts("Spreadsheet Name"))
	require.NoError(t, err)

	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Real Name", got.ProfileName)
}

func TestResolve_InboundUpdatesProfileName(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	opts := inboundOpts()
	opts.ProfileName = "First Name"
	created, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923215552222"}, opts)
	require.NoError(t, err)

	opts.ProfileName = "Updated Name"
	got, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923215552222"}, opts)
	require.NoError(t, err)

	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "Updated Name", got.ProfileName)
}

// --- Events ---

func TestLifecycle_EmitsEventsForEveryTransition(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	created, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "923215553333"}, inboundOpts())
	require.NoError(t, err)

	require.NoError(t, svc.Delete(context.Background(), org.ID, created.ID,
		contacts.ReasonUser, crmevents.SystemActor()))
	require.NoError(t, svc.Restore(context.Background(), org.ID, created.ID, crmevents.SystemActor()))

	var types []string
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND contact_id = ?", org.ID, created.ID).
		Order("occurred_at").Pluck("type", &types).Error)

	assert.Equal(t, []string{"contact.created", "contact.deleted", "contact.restored"}, types,
		"every lifecycle transition is recorded; before this only inbound creation was")
}

// A merged contact must not be resurrected by an explicit restore either.
func TestRestore_RefusesMergedContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	svc := contacts.New(db)
	org := testutil.CreateTestOrganization(t, db)

	survivor := testutil.CreateTestContact(t, db, org.ID)
	merged := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("923215554444"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", merged.ID).
		Update("merged_into_id", survivor.ID).Error)
	softDelete(t, db, svc, org.ID, merged.ID, contacts.ReasonMerged)

	err := svc.Restore(context.Background(), org.ID, merged.ID, crmevents.SystemActor())
	assert.Error(t, err, "restoring a merged contact would split its history back apart")
}
