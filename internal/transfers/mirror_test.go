package transfers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/transfers"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// queuedWithConversation is a customer who wrote in and was handed to the
// queue: a live conversation and an unassigned active transfer.
func queuedWithConversation(t *testing.T, db *gorm.DB, orgID uuid.UUID) models.AgentTransfer {
	t.Helper()
	transfer := queued(t, db, orgID, nil, time.Minute)
	_, err := conversation.New(db).TouchInbound(ctx(), orgID, transfer.ContactID, "acct", time.Now().UTC(), false)
	require.NoError(t, err)
	return transfer
}

func liveConversation(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID) models.Conversation {
	t.Helper()
	conv, err := conversation.New(db).Active(ctx(), orgID, contactID)
	require.NoError(t, err)
	return *conv
}

// Plan 03, §4.4: while a transfer is active the conversation mirrors it. The
// inbox's Mine view lists conversations by assignee, and picking up from the
// queue used to change only the transfer — so the customer an agent had just
// taken was not in their list.
func TestPickNext_AssignsTheConversationToThePicker(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)
	transfer := queuedWithConversation(t, db, org.ID)

	_, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID, Unrestricted: true,
	})
	require.NoError(t, err)

	conv := liveConversation(t, db, org.ID, transfer.ContactID)
	require.NotNil(t, conv.AssigneeID, "the picker must find the customer under Mine")
	assert.Equal(t, agent.ID, *conv.AssigneeID)
	assert.Equal(t, models.HandlingHuman, conv.Handling)
}

// The agent stepping away puts the customer back in the queue, and the
// conversation has to leave their Mine view with it.
func TestReturnToQueue_UnassignsTheConversation(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)
	transfer := queuedWithConversation(t, db, org.ID)

	_, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID, Unrestricted: true,
	})
	require.NoError(t, err)
	_, err = transfers.New(db).ReturnToQueue(ctx(), org.ID, agent.ID)
	require.NoError(t, err)

	conv := liveConversation(t, db, org.ID, transfer.ContactID)
	assert.Nil(t, conv.AssigneeID, "a returned customer belongs to the queue, not to whoever stepped away")
}

// Handing back to the bot ends the agent's part. Leaving them as assignee kept
// a bot conversation in their Mine view and out of the Bot view.
func TestResume_ClearsTheTransfersAgentButNotALaterAssignee(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)
	transfer := queuedWithConversation(t, db, org.ID)

	picked, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID, Unrestricted: true,
	})
	require.NoError(t, err)
	_, _, err = transfers.New(db).Resume(ctx(), org.ID, picked.ID, agent.ID)
	require.NoError(t, err)

	conv := liveConversation(t, db, org.ID, transfer.ContactID)
	assert.Nil(t, conv.AssigneeID)

	// A transfer that ends after somebody else has been assigned must not
	// undo that assignment.
	other := testutil.CreateTestUser(t, db, org.ID)
	_, err = conversation.New(db).Assign(ctx(), org.ID, transfer.ContactID, &other.ID, nil, crmevents.SystemActor())
	require.NoError(t, err)
	stale := *picked
	stale.Status = models.TransferStatusExpired
	require.NoError(t, transfers.MirrorToConversation(db, &stale))

	conv = liveConversation(t, db, org.ID, transfer.ContactID)
	require.NotNil(t, conv.AssigneeID)
	assert.Equal(t, other.ID, *conv.AssigneeID)
}

// The other direction: the inbox's Take assigns the conversation. The transfer
// has to follow, or it stays unpicked in the queue and its response deadline
// escalates for a customer somebody is already answering.
func TestConversationAssign_PicksUpTheActiveTransfer(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)
	transfer := queuedWithConversation(t, db, org.ID)

	_, err := conversation.New(db).Assign(ctx(), org.ID, transfer.ContactID, &agent.ID, nil,
		crmevents.UserActor(agent.ID, ""))
	require.NoError(t, err)

	var after models.AgentTransfer
	require.NoError(t, db.First(&after, transfer.ID).Error)
	require.NotNil(t, after.AgentID)
	assert.Equal(t, agent.ID, *after.AgentID)
	assert.NotNil(t, after.SLA.PickedUpAt, "queue time ends when somebody takes it")

	// Unassigning returns it to the queue.
	_, err = conversation.New(db).Assign(ctx(), org.ID, transfer.ContactID, nil, nil,
		crmevents.UserActor(agent.ID, ""))
	require.NoError(t, err)
	var returned models.AgentTransfer
	require.NoError(t, db.First(&returned, transfer.ID).Error)
	assert.Nil(t, returned.AgentID)
	assert.Equal(t, models.TransferStatusActive, returned.Status)
}
