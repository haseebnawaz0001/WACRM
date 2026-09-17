package contacts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// cascadingService is the lifecycle service as the HTTP layer builds it: with a
// conversation service attached, so deleting a contact closes their
// conversation through the one component that owns that transition.
func cascadingService(db *gorm.DB) *contacts.Service {
	return contacts.New(db).WithConversations(conversation.New(db))
}

func activeTransfer(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID) uuid.UUID {
	t.Helper()
	transfer := models.AgentTransfer{
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "acct",
		Status:          models.TransferStatusActive,
		TransferredAt:   time.Now().UTC(),
	}
	require.NoError(t, db.Create(&transfer).Error)
	return transfer.ID
}

func activeSession(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID) uuid.UUID {
	t.Helper()
	session := models.ChatbotSession{
		OrganizationID:  orgID,
		ContactID:       contactID,
		WhatsAppAccount: "acct",
		Status:          models.SessionStatusActive,
		StartedAt:       time.Now().UTC(),
	}
	require.NoError(t, db.Create(&session).Error)
	return session.ID
}

func transferStatus(t *testing.T, db *gorm.DB, id uuid.UUID) models.TransferStatus {
	t.Helper()
	var got models.AgentTransfer
	require.NoError(t, db.Where("id = ?", id).First(&got).Error)
	return got.Status
}

func sessionStatus(t *testing.T, db *gorm.DB, id uuid.UUID) models.SessionStatus {
	t.Helper()
	var got models.ChatbotSession
	require.NoError(t, db.Where("id = ?", id).First(&got).Error)
	return got.Status
}

// Deleting a contact must end the work that was in flight for them. A queued
// transfer for a contact who no longer exists can never be picked up, and it
// holds the one-active-transfer slot forever.
func TestDelete_EndsWorkInFlight(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	ctx := context.Background()

	conversations := conversation.New(db)
	_, err := conversations.TouchInbound(ctx, org.ID, contact.ID, "acct", time.Now().UTC(), false)
	require.NoError(t, err)

	transferID := activeTransfer(t, db, org.ID, contact.ID)
	sessionID := activeSession(t, db, org.ID, contact.ID)

	require.NoError(t, cascadingService(db).
		Delete(ctx, org.ID, contact.ID, contacts.ReasonUser, crmevents.SystemActor()))

	var conv models.Conversation
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&conv).Error)
	assert.Equal(t, models.ConversationResolved, conv.Status,
		"a deleted contact must not leave an open conversation in someone's inbox")
	assert.Equal(t, models.ResolutionContactDeleted, conv.ResolutionReason,
		"the reason has to say the contact was deleted, or reporting counts it as agent work")

	assert.Equal(t, models.TransferStatusExpired, transferStatus(t, db, transferID),
		"a transfer nobody can pick up must not hold its queue position")
	assert.Equal(t, models.SessionStatusCancelled, sessionStatus(t, db, sessionID),
		"a live prompt node would swallow the first message from the next owner of that number")
}

// An address-book sync says the contact left the phone's address book. It says
// nothing about the conversation, which is still live and is restored on the
// next inbound message — so it must not close anything.
func TestDelete_AddressBookSyncLeavesWorkAlone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	ctx := context.Background()

	_, err := conversation.New(db).TouchInbound(ctx, org.ID, contact.ID, "acct", time.Now().UTC(), false)
	require.NoError(t, err)
	transferID := activeTransfer(t, db, org.ID, contact.ID)

	require.NoError(t, cascadingService(db).
		Delete(ctx, org.ID, contact.ID, contacts.ReasonAddressBookSync, crmevents.SystemActor()))

	var conv models.Conversation
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&conv).Error)
	assert.Equal(t, models.ConversationOpen, conv.Status,
		"a sync removal is not a decision to end the conversation")
	assert.Equal(t, models.TransferStatusActive, transferStatus(t, db, transferID))
}

// Deleting a contact must not destroy someone's to-do list, but the work must
// disappear from lists while the contact is gone — and come back with them.
func TestDelete_HidesTasksAndDealsUntilRestore(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	ctx := context.Background()

	taskType := models.TaskType{
		OrganizationID: org.ID,
		Key:            "call_back",
		Label:          "Call back",
	}
	require.NoError(t, db.Create(&taskType).Error)

	task := models.Task{
		OrganizationID: org.ID,
		ContactID:      contact.ID,
		TypeID:         taskType.ID,
		OwnerID:        user.ID,
		CreatedByID:    &user.ID,
		Title:          "Call back",
		Status:         models.TaskOpen,
		DueAt:          time.Now().UTC().Add(time.Hour),
	}
	require.NoError(t, db.Create(&task).Error)

	svc := cascadingService(db)
	require.NoError(t, svc.Delete(ctx, org.ID, contact.ID, contacts.ReasonUser, crmevents.SystemActor()))

	var stillThere models.Task
	require.NoError(t, db.Where("id = ?", task.ID).First(&stillThere).Error,
		"the task row is kept: it is a person's work, not the customer's")

	var listed int64
	require.NoError(t, db.Model(&models.Task{}).
		Where("tasks.organization_id = ?", org.ID).
		Where("EXISTS (SELECT 1 FROM contacts c WHERE c.id = tasks.contact_id AND c.deleted_at IS NULL)").
		Count(&listed).Error)
	assert.Zero(t, listed, "a follow-up for a deleted contact is not work anybody can do")

	require.NoError(t, svc.Restore(ctx, org.ID, contact.ID, crmevents.SystemActor()))

	require.NoError(t, db.Model(&models.Task{}).
		Where("tasks.organization_id = ?", org.ID).
		Where("EXISTS (SELECT 1 FROM contacts c WHERE c.id = tasks.contact_id AND c.deleted_at IS NULL)").
		Count(&listed).Error)
	assert.EqualValues(t, 1, listed, "restoring the contact brings their work back")
}

// A lifecycle service built without a conversation service still deletes. The
// campaign worker and the calling manager construct one that way.
func TestDelete_WorksWithoutAConversationService(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	ctx := context.Background()

	transferID := activeTransfer(t, db, org.ID, contact.ID)

	require.NoError(t, contacts.New(db).
		Delete(ctx, org.ID, contact.ID, contacts.ReasonUser, crmevents.SystemActor()))

	assert.Equal(t, models.TransferStatusExpired, transferStatus(t, db, transferID),
		"the parts that need no collaborator still run")
}
