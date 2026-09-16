package dedupe_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/dedupe"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *dedupe.Service, *models.Organization, *models.User) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, customfields.SeedOrganization(db, org.ID))
	user := testutil.CreateTestUser(t, db, org.ID)
	return db, dedupe.New(db), org, user
}

// contactWithPhone creates a contact whose normalised phone is set, as the
// lifecycle service would.
func contactWithPhone(t *testing.T, db *gorm.DB, orgID uuid.UUID, phone, normalized string) *models.Contact {
	t.Helper()
	contact := testutil.CreateTestContactWith(t, db, orgID, testutil.WithPhoneNumber(phone))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("phone_normalized", normalized).Error)
	contact.PhoneNormalized = normalized
	return contact
}

// --- Detection ---

// Two records for one number is the commonest duplicate, and the one the
// non-partial unique index used to make impossible to even record.
func TestScan_FindsContactsSharingANormalisedPhone(t *testing.T) {
	db, svc, org, _ := setup(t)

	a := contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	b := contactWithPhone(t, db, org.ID, "+92 321 1234567", "923211234567")
	contactWithPhone(t, db, org.ID, "923219999999", "923219999999")

	recorded, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, recorded)

	pending, err := svc.PendingCandidates(ctx(), org.ID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	assert.Equal(t, dedupe.ScorePhoneNormalized, pending[0].Score)
	assert.Contains(t, pending[0].Reasons, dedupe.ReasonPhoneNormalized)

	ids := map[uuid.UUID]bool{pending[0].ContactAID: true, pending[0].ContactBID: true}
	assert.True(t, ids[a.ID] && ids[b.ID])
}

func TestScan_FindsContactsSharingAnEmail(t *testing.T) {
	db, svc, org, _ := setup(t)

	a := contactWithPhone(t, db, org.ID, "15558100001", "15558100001")
	b := contactWithPhone(t, db, org.ID, "15558100002", "15558100002")

	fields := customfields.New(db)
	for _, contact := range []*models.Contact{a, b} {
		_, err := fields.SetValues(db, org.ID, contact.ID, models.FieldEntityContact,
			map[string]any{models.FieldKeyEmail: "shared@example.com"}, nil)
		require.NoError(t, err)
	}

	recorded, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, recorded)

	pending, err := svc.PendingCandidates(ctx(), org.ID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Contains(t, pending[0].Reasons, dedupe.ReasonEmail)
}

// Re-suggesting a pair someone dismissed would train people to ignore the whole
// list.
func TestScan_DoesNotResurrectDismissedPairs(t *testing.T) {
	db, svc, org, user := setup(t)

	contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	contactWithPhone(t, db, org.ID, "+923211234567", "923211234567")

	_, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)

	pending, err := svc.PendingCandidates(ctx(), org.ID, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.NoError(t, svc.Dismiss(ctx(), org.ID, pending[0].ID, user.ID))

	recorded, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)
	assert.Zero(t, recorded, "a dismissed pair must not be suggested again")

	pending, err = svc.PendingCandidates(ctx(), org.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// (A,B) and (B,A) are one pair, not two.
func TestScan_RecordsEachPairOnce(t *testing.T) {
	db, svc, org, _ := setup(t)

	contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	contactWithPhone(t, db, org.ID, "+923211234567", "923211234567")

	_, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)
	_, err = svc.Scan(ctx(), org.ID)
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&models.ContactDuplicateCandidate{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestScan_IgnoresAlreadyMergedContacts(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	secondary := contactWithPhone(t, db, org.ID, "+923211234567", "923211234567")

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec("DELETE FROM contact_duplicate_candidates WHERE organization_id = ?", org.ID).Error)

	recorded, err := svc.Scan(ctx(), org.ID)
	require.NoError(t, err)
	assert.Zero(t, recorded, "a merged contact is not a duplicate of its survivor")
}

// --- Merging ---

// History has to move wholesale, or half of it becomes unreachable.
func TestMerge_MovesHistoryToTheSurvivor(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	secondary := contactWithPhone(t, db, org.ID, "923219999999", "923219999999")
	account := testutil.CreateTestWhatsAppAccount(t, db, org.ID)

	for i := 0; i < 3; i++ {
		require.NoError(t, db.Create(&models.Message{
			BaseModel:       models.BaseModel{ID: uuid.New()},
			OrganizationID:  org.ID,
			ContactID:       secondary.ID,
			WhatsAppAccount: account.Name,
			Direction:       models.DirectionIncoming,
			SenderType:      models.SenderContact,
			MessageType:     models.MessageTypeText,
			Content:         "hello",
			Status:          models.MessageStatusReceived,
		}).Error)
	}

	record, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, record)

	var moved int64
	require.NoError(t, db.Model(&models.Message{}).
		Where("contact_id = ?", primary.ID).Count(&moved).Error)
	assert.EqualValues(t, 3, moved)

	var left int64
	require.NoError(t, db.Model(&models.Message{}).
		Where("contact_id = ?", secondary.ID).Count(&left).Error)
	assert.Zero(t, left)
}

// The customer does not know they were merged and will keep using whichever
// number they always used.
func TestMerge_SecondaryNumberStillResolvesToTheSurvivor(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "923211234567", "923211234567")
	secondary := contactWithPhone(t, db, org.ID, "923219999999", "923219999999")

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	resolved, err := svc.ResolveIdentity(ctx(), org.ID, models.IdentityPhone, "+92 321 999 9999")
	require.NoError(t, err)
	assert.Equal(t, primary.ID, resolved)

	var reloaded models.Contact
	require.NoError(t, db.Unscoped().First(&reloaded, "id = ?", secondary.ID).Error)
	require.NotNil(t, reloaded.MergedIntoID)
	assert.Equal(t, primary.ID, *reloaded.MergedIntoID)
	assert.True(t, reloaded.DeletedAt.Valid)
	_ = db
}

// Both records' tags describe the same person, so they are unioned rather than
// one replacing the other.
func TestMerge_UnionsTags(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15558200001"), testutil.WithTags("VIP", "Lead"))
	secondary := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15558200002"), testutil.WithTags("Lead", "Newsletter"))

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	var reloaded models.Contact
	require.NoError(t, db.First(&reloaded, "id = ?", primary.ID).Error)

	tags := map[string]bool{}
	for _, raw := range reloaded.Tags {
		tags[raw.(string)] = true
	}
	assert.True(t, tags["VIP"] && tags["Lead"] && tags["Newsletter"])
	assert.Len(t, reloaded.Tags, 3, "the union must not duplicate a shared tag")
}

// A merge must never silently overwrite the record someone chose to keep.
func TestMerge_PrimaryFieldValuesWin(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "15558300001", "15558300001")
	secondary := contactWithPhone(t, db, org.ID, "15558300002", "15558300002")

	fields := customfields.New(db)
	_, err := fields.SetValues(db, org.ID, primary.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyCompany: "Keep This"}, nil)
	require.NoError(t, err)
	_, err = fields.SetValues(db, org.ID, secondary.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyCompany: "Discard", models.FieldKeyAddress: "Only on secondary"}, nil)
	require.NoError(t, err)

	_, err = svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	values, err := fields.Values(ctx(), org.ID, primary.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Keep This", values[models.FieldKeyCompany], "the primary's value wins")
	assert.Equal(t, "Only on secondary", values[models.FieldKeyAddress],
		"a value only the secondary had is not lost")
}

// Two active conversations for one contact is exactly what the unique index
// forbids, so the secondary's are resolved rather than moved live.
func TestMerge_ResolvesTheSecondaryActiveConversation(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "15558400001", "15558400001")
	secondary := contactWithPhone(t, db, org.ID, "15558400002", "15558400002")

	for _, contact := range []*models.Contact{primary, secondary} {
		require.NoError(t, db.Create(&models.Conversation{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: org.ID,
			ContactID:      contact.ID,
			Status:         models.ConversationOpen,
			OpenedAt:       primary.CreatedAt,
		}).Error)
	}

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	var active int64
	require.NoError(t, db.Model(&models.Conversation{}).
		Where("contact_id = ? AND status <> ?", primary.ID, models.ConversationResolved).
		Count(&active).Error)
	assert.EqualValues(t, 1, active, "the survivor keeps exactly one active conversation")
}

// Without a snapshot, "why does this contact have that name?" has no answer.
func TestMerge_RecordsAReviewableSnapshot(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "15558500001", "15558500001")
	secondary := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15558500002"), testutil.WithProfileName("Old Record"))

	record, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	snapshot, ok := record.Snapshot["secondary"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Old Record", snapshot["profile_name"])
	assert.Contains(t, record.Snapshot, "moved")
	_ = db
}

func TestMerge_RefusesSelfMergeAndDoubleMerge(t *testing.T) {
	db, svc, org, user := setup(t)

	primary := contactWithPhone(t, db, org.ID, "15558600001", "15558600001")
	secondary := contactWithPhone(t, db, org.ID, "15558600002", "15558600002")

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: primary.ID, ActorID: user.ID,
	})
	assert.ErrorIs(t, err, dedupe.ErrSameContact)

	_, err = svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	_, err = svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	assert.ErrorContains(t, err, "already been merged")
	_ = db
}

func TestMerge_RecordsAnEvent(t *testing.T) {
	db, svc, org, user := setup(t)
	require.NoError(t, db.Exec("DELETE FROM crm_event_outbox WHERE organization_id = ?", org.ID).Error)

	primary := contactWithPhone(t, db, org.ID, "15558700001", "15558700001")
	secondary := contactWithPhone(t, db, org.ID, "15558700002", "15558700002")

	_, err := svc.Merge(ctx(), dedupe.MergeInput{
		OrgID: org.ID, PrimaryID: primary.ID, SecondaryID: secondary.ID, ActorID: user.ID,
	})
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND type = ?", org.ID, "contact.merged").
		Count(&count).Error)
	assert.EqualValues(t, 1, count)
	_ = crmevents.SystemActor
}
