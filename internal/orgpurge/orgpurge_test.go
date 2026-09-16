package orgpurge_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgpurge"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// populate writes a row into enough of the schema that the purge has to reach
// across foreign keys rather than one flat table.
func populate(t *testing.T, db *gorm.DB, orgID uuid.UUID) (contactID, userID uuid.UUID) {
	t.Helper()

	user := testutil.CreateTestUser(t, db, orgID)
	account := testutil.CreateTestWhatsAppAccount(t, db, orgID)
	contact := testutil.CreateTestContact(t, db, orgID)

	team := &models.Team{OrganizationID: orgID, Name: "Support"}
	require.NoError(t, db.Create(team).Error)
	require.NoError(t, db.Create(&models.TeamMember{TeamID: team.ID, UserID: user.ID}).Error)

	conv := &models.Conversation{
		OrganizationID: orgID,
		ContactID:      contact.ID,
		Status:         models.ConversationOpen,
		AssigneeID:     &user.ID,
		TeamID:         &team.ID,
		OpenedAt:       time.Now(),
	}
	require.NoError(t, db.Create(conv).Error)

	require.NoError(t, db.Create(&models.Message{
		OrganizationID:  orgID,
		ContactID:       contact.ID,
		WhatsAppAccount: account.Name,
		ConversationID:  conv.ID.String(),
		MessageType:     models.MessageTypeText,
		Content:         "hello",
		Direction:       models.DirectionIncoming,
	}).Error)

	template := testutil.CreateTestTemplate(t, db, orgID, account.Name)
	campaign := &models.BulkMessageCampaign{
		OrganizationID:  orgID,
		WhatsAppAccount: account.Name,
		Name:            "Launch",
		TemplateID:      template.ID,
		CreatedBy:       user.ID,
	}
	require.NoError(t, db.Create(campaign).Error)
	require.NoError(t, db.Create(&models.BulkMessageRecipient{
		CampaignID:  campaign.ID,
		ContactID:   &contact.ID,
		PhoneNumber: contact.PhoneNumber,
	}).Error)

	return contact.ID, user.ID
}

// Plan 10, S8: deleting an organization has to take its data with it.
//
// Deleting the organizations row alone left every other table behind, scoped to
// an id that no longer resolved to anything. Nothing listed those rows, because
// every query starts from the org, and nothing could delete them either.
func TestPurge_LeavesNothingBehind(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	populate(t, db, org.ID)

	before, err := orgpurge.CountRemaining(db, org.ID)
	require.NoError(t, err)
	require.NotEmpty(t, before, "the fixture must write something to purge")

	result, err := orgpurge.Purge(db, org.ID)
	require.NoError(t, err)
	assert.Positive(t, result.Total)

	after, err := orgpurge.CountRemaining(db, org.ID)
	require.NoError(t, err)
	assert.Empty(t, after, "no row may keep pointing at a purged organization")

	var orgs int64
	db.Raw(`SELECT count(*) FROM organizations WHERE id = ?`, org.ID).Scan(&orgs)
	assert.Zero(t, orgs)
}

// Purging one tenant must not touch another. This is the failure that would be
// discovered by a customer, not by a log line.
func TestPurge_DoesNotTouchOtherOrganizations(t *testing.T) {
	db := testutil.SetupTestDB(t)
	doomed := testutil.CreateTestOrganization(t, db)
	keeper := testutil.CreateTestOrganization(t, db)

	populate(t, db, doomed.ID)
	keptContact, keptUser := populate(t, db, keeper.ID)

	_, err := orgpurge.Purge(db, doomed.ID)
	require.NoError(t, err)

	var contacts, users int64
	db.Raw(`SELECT count(*) FROM contacts WHERE id = ?`, keptContact).Scan(&contacts)
	db.Raw(`SELECT count(*) FROM users WHERE id = ?`, keptUser).Scan(&users)
	assert.Equal(t, int64(1), contacts, "another org's contact must survive")
	assert.Equal(t, int64(1), users, "another org's user must survive")

	remaining, err := orgpurge.CountRemaining(db, keeper.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, remaining, "the surviving org must keep its data")
}

// A user who belongs to a second organization is not this organization's to
// delete: removing them would break a live tenant.
func TestPurge_KeepsUsersWhoBelongToAnotherOrg(t *testing.T) {
	db := testutil.SetupTestDB(t)
	doomed := testutil.CreateTestOrganization(t, db)
	keeper := testutil.CreateTestOrganization(t, db)

	shared := testutil.CreateTestUser(t, db, doomed.ID)
	require.NoError(t, db.Create(&models.UserOrganization{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		UserID:         shared.ID,
		OrganizationID: keeper.ID,
	}).Error)

	result, err := orgpurge.Purge(db, doomed.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.UsersRehomed)

	var homeOrg string
	require.NoError(t, db.Raw(`SELECT organization_id::text FROM users WHERE id = ?`, shared.ID).
		Scan(&homeOrg).Error)
	assert.Equal(t, keeper.ID.String(), homeOrg,
		"a user with a second organization must outlive the purge of their first, re-homed onto it")
}

// Purging an id that does not exist is a typo, not a no-op worth reporting as
// success.
func TestPurge_RefusesAnUnknownOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	_, err := orgpurge.Purge(db, uuid.New())
	require.Error(t, err)
}

// The sweep is discovered from the schema so a table added later cannot be
// forgotten; this asserts the discovery actually sees the schema.
func TestScopedTables_CoverTheSchema(t *testing.T) {
	db := testutil.SetupTestDB(t)

	tables, err := orgpurge.ScopedTables(db)
	require.NoError(t, err)

	found := map[string]bool{}
	for _, table := range tables {
		found[table] = true
	}
	for _, expected := range []string{"contacts", "conversations", "messages", "teams", "users"} {
		assert.True(t, found[expected], "%s carries organization_id and must be purged", expected)
	}
	assert.False(t, found["organizations"], "the org row is deleted last, not swept")
	assert.False(t, found["permissions"], "the permission catalog is global")
}
