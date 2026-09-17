package conversation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// sentCampaign records a campaign that reached one contact at a given time.
func sentCampaign(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, sentAt time.Time) uuid.UUID {
	t.Helper()

	template := testutil.CreateTestTemplate(t, db, orgID, "acct")
	creator := testutil.CreateTestUser(t, db, orgID)

	campaign := models.BulkMessageCampaign{
		OrganizationID:  orgID,
		Name:            "Spring offer",
		WhatsAppAccount: "acct",
		TemplateID:      template.ID,
		CreatedBy:       creator.ID,
		Status:          models.CampaignStatusCompleted,
	}
	require.NoError(t, db.Create(&campaign).Error)

	recipient := models.BulkMessageRecipient{
		CampaignID:  campaign.ID,
		ContactID:   &contactID,
		PhoneNumber: "15551230000",
		Status:      models.MessageStatusSent,
		SentAt:      &sentAt,
	}
	require.NoError(t, db.Create(&recipient).Error)
	return campaign.ID
}

func openedAt(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, at time.Time) *models.Conversation {
	t.Helper()
	conv, err := conversation.New(db).TouchInbound(context.Background(), orgID, contactID, "acct", at, false)
	require.NoError(t, err)
	return conv
}

// A campaign could report how many messages went out and nothing about what
// came back, which is the only question anybody asks about a campaign.
func TestAttributeToCampaign_LinksARecentReply(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	campaignID := sentCampaign(t, db, org.ID, contact.ID, now.Add(-2*time.Hour))
	conv := openedAt(t, db, org.ID, contact.ID, now)

	got, err := conversation.New(db).AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, campaignID, *got)

	var stored models.Conversation
	require.NoError(t, db.Where("id = ?", conv.ID).First(&stored).Error)
	require.NotNil(t, stored.OriginCampaignID)
	assert.Equal(t, campaignID, *stored.OriginCampaignID)
}

// A reply three weeks later is not a reply to a campaign nobody remembers.
func TestAttributeToCampaign_IgnoresSendsOutsideTheWindow(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	sentCampaign(t, db, org.ID, contact.ID, now.Add(-30*24*time.Hour))
	conv := openedAt(t, db, org.ID, contact.ID, now)

	got, err := conversation.New(db).AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// A campaign sent after the customer wrote did not prompt the message.
func TestAttributeToCampaign_IgnoresSendsAfterTheConversationOpened(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	conv := openedAt(t, db, org.ID, contact.ID, now.Add(-time.Hour))
	sentCampaign(t, db, org.ID, contact.ID, now)

	got, err := conversation.New(db).AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	assert.Nil(t, got, "a send that followed the reply cannot have caused it")
}

// A customer who sends five messages replied once. Re-attributing would credit
// the campaign five times over.
func TestAttributeToCampaign_IsIdempotent(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	sentCampaign(t, db, org.ID, contact.ID, now.Add(-time.Hour))
	conv := openedAt(t, db, org.ID, contact.ID, now)

	svc := conversation.New(db)
	first, err := svc.AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := svc.AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	assert.Nil(t, second, "a conversation is attributed once")
}

// The send that preceded the reply is the one that prompted it, not whichever
// campaign happens to be newest.
func TestAttributeToCampaign_PicksTheMostRecentPrecedingSend(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now().UTC()
	sentCampaign(t, db, org.ID, contact.ID, now.Add(-48*time.Hour))
	recent := sentCampaign(t, db, org.ID, contact.ID, now.Add(-3*time.Hour))
	conv := openedAt(t, db, org.ID, contact.ID, now)

	got, err := conversation.New(db).AttributeToCampaign(context.Background(), conv, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, recent, *got)
}
