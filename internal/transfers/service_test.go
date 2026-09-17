package transfers_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/transfers"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

// queued creates an unassigned active transfer for a fresh contact.
func queued(t *testing.T, db *gorm.DB, orgID uuid.UUID, teamID *uuid.UUID, age time.Duration) models.AgentTransfer {
	t.Helper()
	contact := testutil.CreateTestContact(t, db, orgID)
	transfer := models.AgentTransfer{
		OrganizationID:  orgID,
		ContactID:       contact.ID,
		WhatsAppAccount: "acct",
		PhoneNumber:     contact.PhoneNumber,
		Status:          models.TransferStatusActive,
		TeamID:          teamID,
		TransferredAt:   time.Now().UTC().Add(-age),
	}
	require.NoError(t, db.Create(&transfer).Error)
	return transfer
}

// The queue is first-in-first-out: the customer who has waited longest is the
// one an agent should get.
func TestPickNext_TakesTheOldestQueuedTransfer(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)

	newer := queued(t, db, org.ID, nil, time.Minute)
	older := queued(t, db, org.ID, nil, time.Hour)

	picked, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID, Unrestricted: true,
	})
	require.NoError(t, err)

	assert.Equal(t, older.ID, picked.ID, "the longest wait goes first")
	assert.NotEqual(t, newer.ID, picked.ID)
	require.NotNil(t, picked.AgentID)
	assert.Equal(t, agent.ID, *picked.AgentID)
	assert.NotNil(t, picked.SLA.PickedUpAt, "queue time is measured from pickup, not from the next update")
}

// Two agents hitting Pick at the same moment must get different customers.
// Handing the same conversation to both is the failure this locking exists to
// prevent.
func TestPickNext_TwoAgentsNeverGetTheSameTransfer(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	first := testutil.CreateTestUser(t, db, org.ID)
	second := testutil.CreateTestUser(t, db, org.ID)

	queued(t, db, org.ID, nil, time.Hour)
	queued(t, db, org.ID, nil, time.Minute)

	svc := transfers.New(db)
	results := make([]*models.AgentTransfer, 2)
	errs := make([]error, 2)

	var wg sync.WaitGroup
	for i, user := range []uuid.UUID{first.ID, second.ID} {
		wg.Add(1)
		go func(i int, userID uuid.UUID) {
			defer wg.Done()
			results[i], errs[i] = svc.PickNext(ctx(), transfers.PickInput{
				OrgID: org.ID, UserID: userID, Unrestricted: true,
			})
		}(i, user)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.NotEqual(t, results[0].ID, results[1].ID,
		"one conversation cannot be handed to two agents")
}

// An agent may only draw from their own team's queue, plus the general one.
func TestPickNext_RespectsQueueMembership(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)

	otherTeam := uuid.New()
	require.NoError(t, db.Create(&models.Team{
		BaseModel:      models.BaseModel{ID: otherTeam},
		OrganizationID: org.ID,
		Name:           "Billing",
	}).Error)

	queued(t, db, org.ID, &otherTeam, time.Hour)
	general := queued(t, db, org.ID, nil, time.Minute)

	picked, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, general.ID, picked.ID,
		"another team's queue is not this agent's to pick from, however long it has waited")
}

func TestPickNext_EmptyQueueSaysSo(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)

	_, err := transfers.New(db).PickNext(ctx(), transfers.PickInput{
		OrgID: org.ID, UserID: agent.ID, Unrestricted: true,
	})
	assert.ErrorIs(t, err, transfers.ErrNoneQueued)
}

// Pinning the picker as owner is opt-in, and only fills an empty slot: an
// existing relationship manager is not displaced by whoever happened to answer.
func TestPickNext_OwnerAssignmentOnlyFillsAnEmptySlot(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	picker := testutil.CreateTestUser(t, db, org.ID)
	owner := testutil.CreateTestUser(t, db, org.ID)

	unowned := queued(t, db, org.ID, nil, time.Hour)
	owned := queued(t, db, org.ID, nil, time.Minute)
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", owned.ContactID).
		Update("assigned_user_id", owner.ID).Error)

	svc := transfers.New(db)
	in := transfers.PickInput{
		OrgID: org.ID, UserID: picker.ID, Unrestricted: true, AssignContactOwner: true,
	}

	_, err := svc.PickNext(ctx(), in)
	require.NoError(t, err)
	_, err = svc.PickNext(ctx(), in)
	require.NoError(t, err)

	var first, second models.Contact
	require.NoError(t, db.Where("id = ?", unowned.ContactID).First(&first).Error)
	require.NoError(t, db.Where("id = ?", owned.ContactID).First(&second).Error)

	require.NotNil(t, first.AssignedUserID)
	assert.Equal(t, picker.ID, *first.AssignedUserID)
	require.NotNil(t, second.AssignedUserID)
	assert.Equal(t, owner.ID, *second.AssignedUserID,
		"answering one message does not make you the relationship manager")
}

// An agent stepping away returns their conversations to the queue — and keeps
// being the contact's owner. Clearing it broke owner-based IVR routing for a
// customer whose agent went to lunch.
func TestReturnToQueue_KeepsTheContactOwner(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)

	transfer := queued(t, db, org.ID, nil, time.Hour)
	require.NoError(t, db.Model(&models.AgentTransfer{}).Where("id = ?", transfer.ID).
		Update("agent_id", agent.ID).Error)
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", transfer.ContactID).
		Update("assigned_user_id", agent.ID).Error)

	returned, err := transfers.New(db).ReturnToQueue(ctx(), org.ID, agent.ID)
	require.NoError(t, err)
	require.Len(t, returned, 1)

	var after models.AgentTransfer
	require.NoError(t, db.Where("id = ?", transfer.ID).First(&after).Error)
	assert.Nil(t, after.AgentID, "it is back in the queue for somebody to take")
	assert.Equal(t, models.TransferStatusActive, after.Status)

	var contact models.Contact
	require.NoError(t, db.Where("id = ?", transfer.ContactID).First(&contact).Error)
	require.NotNil(t, contact.AssignedUserID)
	assert.Equal(t, agent.ID, *contact.AssignedUserID,
		"going unavailable is not resigning the relationship")
}

// Resume is a read-modify-write unless it re-reads under the lock. Two agents
// clicking it at once must not both report success.
func TestResume_SecondCallerIsToldItIsAlreadyClosed(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)

	transfer := queued(t, db, org.ID, nil, time.Hour)
	svc := transfers.New(db)

	_, outcome, err := svc.Resume(ctx(), org.ID, transfer.ID, agent.ID)
	require.NoError(t, err)
	assert.Equal(t, transfers.Assigned, outcome)

	_, outcome, err = svc.Resume(ctx(), org.ID, transfer.ID, agent.ID)
	require.NoError(t, err)
	assert.Equal(t, transfers.AlreadyActive, outcome,
		"a transfer somebody already closed must not be closed twice")

	var after models.AgentTransfer
	require.NoError(t, db.Where("id = ?", transfer.ID).First(&after).Error)
	assert.Equal(t, models.TransferStatusResumed, after.Status)
}

func TestExpire_ClosesOnlyActiveTransfers(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	agent := testutil.CreateTestUser(t, db, org.ID)
	svc := transfers.New(db)

	transfer := queued(t, db, org.ID, nil, time.Hour)
	require.NoError(t, svc.Expire(ctx(), org.ID, transfer.ID))

	var after models.AgentTransfer
	require.NoError(t, db.Where("id = ?", transfer.ID).First(&after).Error)
	assert.Equal(t, models.TransferStatusExpired, after.Status)

	// Expiring again must not reopen or re-stamp anything.
	require.NoError(t, svc.Expire(ctx(), org.ID, transfer.ID))
	require.NoError(t, db.Where("id = ?", transfer.ID).First(&after).Error)
	assert.Equal(t, models.TransferStatusExpired, after.Status)

	resumed := queued(t, db, org.ID, nil, time.Hour)
	_, _, err := svc.Resume(ctx(), org.ID, resumed.ID, agent.ID)
	require.NoError(t, err)
	require.NoError(t, svc.Expire(ctx(), org.ID, resumed.ID))

	// A fresh destination: First() on a struct that still holds a primary key
	// silently adds it to the WHERE clause.
	var finished models.AgentTransfer
	require.NoError(t, db.Where("id = ?", resumed.ID).First(&finished).Error)
	assert.Equal(t, models.TransferStatusResumed, finished.Status,
		"a transfer an agent finished must not be relabelled as timed out")
}

func TestHasActive(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := transfers.New(db)

	transfer := queued(t, db, org.ID, nil, time.Minute)

	active, err := svc.HasActive(ctx(), org.ID, transfer.ContactID)
	require.NoError(t, err)
	assert.True(t, active)

	require.NoError(t, svc.Expire(ctx(), org.ID, transfer.ID))

	active, err = svc.HasActive(ctx(), org.ID, transfer.ContactID)
	require.NoError(t, err)
	assert.False(t, active)
}
