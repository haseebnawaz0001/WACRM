package tasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *tasks.Service, *models.Organization, *models.Contact, *models.User) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, tasks.SeedOrganization(db, org.ID))
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)
	return db, tasks.New(db), org, contact, user
}

func basicInput(orgID, contactID uuid.UUID, creator uuid.UUID) tasks.CreateInput {
	return tasks.CreateInput{
		OrgID:     orgID,
		ContactID: contactID,
		TypeKey:   models.TaskTypeFollowUp,
		Title:     "Follow up with the customer",
		CreatedBy: &creator,
	}
}

// --- Seeding ---

func TestSeedOrganization_AddsBuiltInTypes(t *testing.T) {
	db, _, org, _, _ := setup(t)

	var types []models.TaskType
	require.NoError(t, db.Where("organization_id = ?", org.ID).Find(&types).Error)
	require.Len(t, types, 5)

	byKey := map[string]models.TaskType{}
	for _, tt := range types {
		byKey[tt.Key] = tt
	}

	// The offsets differ on purpose: a call back is same-day work, a quote is
	// not. Defaulting everything to tomorrow makes the deadline meaningless.
	assert.Equal(t, 60, byKey[models.TaskTypeCallBack].DefaultDueOffsetMinutes)
	assert.Equal(t, 1440, byKey[models.TaskTypeSendQuote].DefaultDueOffsetMinutes)
	assert.True(t, byKey[models.TaskTypeCallBack].IsSystem)
}

func TestSeedOrganization_IsIdempotent(t *testing.T) {
	db, _, org, _, _ := setup(t)

	callBack := models.TaskType{}
	require.NoError(t, db.Where("organization_id = ? AND key = ?", org.ID, models.TaskTypeCallBack).
		First(&callBack).Error)
	require.NoError(t, db.Model(&models.TaskType{}).Where("id = ?", callBack.ID).
		Update("label", "Ring back").Error)

	require.NoError(t, tasks.SeedOrganization(db, org.ID))

	var count int64
	require.NoError(t, db.Model(&models.TaskType{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.EqualValues(t, 5, count)

	var reloaded models.TaskType
	require.NoError(t, db.Where("id = ?", callBack.ID).First(&reloaded).Error)
	assert.Equal(t, "Ring back", reloaded.Label, "a rename must survive re-seeding")
}

// --- Creating ---

func TestCreate_UsesTheTypeDefaultDeadline(t *testing.T) {
	_, svc, org, contact, user := setup(t)

	in := basicInput(org.ID, contact.ID, user.ID)
	in.TypeKey = models.TaskTypeCallBack

	task, err := svc.Create(ctx(), in)
	require.NoError(t, err)

	assert.Equal(t, models.TaskOpen, task.Status)
	assert.Equal(t, models.TaskPriorityNormal, task.Priority)
	assert.WithinDuration(t, time.Now().UTC().Add(time.Hour), task.DueAt, time.Minute,
		"a call back defaults to an hour out")
	require.NotNil(t, task.RemindAt, "a deadline with no reminder is only noticed once it is late")
	assert.True(t, task.RemindAt.Before(task.DueAt))
}

// Follow-up should stay with whoever knows the customer.
func TestCreate_DefaultsOwnerToTheContactOwner(t *testing.T) {
	db, svc, org, contact, creator := setup(t)
	contactOwner := testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("assigned_user_id", contactOwner.ID).Error)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, creator.ID))
	require.NoError(t, err)
	assert.Equal(t, contactOwner.ID, task.OwnerID)
}

func TestCreate_FallsBackToTheCreator(t *testing.T) {
	_, svc, org, contact, creator := setup(t)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, creator.ID))
	require.NoError(t, err)
	assert.Equal(t, creator.ID, task.OwnerID, "an unowned task is never done")
}

func TestCreate_ExplicitOwnerWins(t *testing.T) {
	db, svc, org, contact, creator := setup(t)
	chosen := testutil.CreateTestUser(t, db, org.ID)

	in := basicInput(org.ID, contact.ID, creator.ID)
	in.OwnerID = &chosen.ID

	task, err := svc.Create(ctx(), in)
	require.NoError(t, err)
	assert.Equal(t, chosen.ID, task.OwnerID)
}

// An inactive user cannot be handed work.
func TestCreate_SkipsInactiveOwners(t *testing.T) {
	db, svc, org, contact, creator := setup(t)
	inactive := testutil.CreateTestUser(t, db, org.ID, testutil.WithInactive())

	in := basicInput(org.ID, contact.ID, creator.ID)
	in.OwnerID = &inactive.ID

	task, err := svc.Create(ctx(), in)
	require.NoError(t, err)
	assert.Equal(t, creator.ID, task.OwnerID, "work falls back to someone who can do it")
	_ = db
}

// "Due today" means the owner's today; a server elsewhere must not move it.
func TestCreate_AllDayUsesTheOwnersTimezone(t *testing.T) {
	_, svc, org, contact, creator := setup(t)

	karachi, err := time.LoadLocation("Asia/Karachi")
	require.NoError(t, err)

	day := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	in := basicInput(org.ID, contact.ID, creator.ID)
	in.DueAt = &day
	in.AllDay = true
	in.Location = karachi

	task, err := svc.Create(ctx(), in)
	require.NoError(t, err)

	local := task.DueAt.In(karachi)
	assert.Equal(t, 23, local.Hour())
	assert.Equal(t, 59, local.Minute())
	assert.Equal(t, 16, local.Day(), "the deadline stays on the owner's day")
}

func TestCreate_RejectsMissingTitleAndUnknownContact(t *testing.T) {
	_, svc, org, contact, creator := setup(t)

	in := basicInput(org.ID, contact.ID, creator.ID)
	in.Title = ""
	_, err := svc.Create(ctx(), in)
	assert.ErrorContains(t, err, "needs a title")

	in = basicInput(org.ID, uuid.New(), creator.ID)
	_, err = svc.Create(ctx(), in)
	assert.ErrorContains(t, err, "contact not found")
}

func TestCreate_RejectsArchivedTypes(t *testing.T) {
	db, svc, org, contact, creator := setup(t)

	require.NoError(t, db.Model(&models.TaskType{}).
		Where("organization_id = ? AND key = ?", org.ID, models.TaskTypeFollowUp).
		Update("archived_at", time.Now().UTC()).Error)

	_, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, creator.ID))
	assert.ErrorContains(t, err, "archived")
}

// --- Completing ---

func TestComplete(t *testing.T) {
	_, svc, org, contact, user := setup(t)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	done, err := svc.Complete(ctx(), org.ID, task.ID, crmevents.UserActor(user.ID, "Agent"))
	require.NoError(t, err)

	assert.Equal(t, models.TaskCompleted, done.Status)
	require.NotNil(t, done.CompletedAt)
	require.NotNil(t, done.CompletedByID)
	assert.Equal(t, user.ID, *done.CompletedByID)
}

// Two clicks on Complete should not produce an error.
func TestComplete_IsIdempotent(t *testing.T) {
	_, svc, org, contact, user := setup(t)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	first, err := svc.Complete(ctx(), org.ID, task.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	second, err := svc.Complete(ctx(), org.ID, task.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	assert.Equal(t, first.CompletedAt.Unix(), second.CompletedAt.Unix(),
		"completing twice must not move the completion time")
}

func TestCancel(t *testing.T) {
	_, svc, org, contact, user := setup(t)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	cancelled, err := svc.Cancel(ctx(), org.ID, task.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Equal(t, models.TaskCancelled, cancelled.Status)
	require.NotNil(t, cancelled.CancelledAt)
}

func TestComplete_ScopedToTheOrganization(t *testing.T) {
	db, svc, org, contact, user := setup(t)
	other := testutil.CreateTestOrganization(t, db)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	_, err = svc.Complete(ctx(), other.ID, task.ID, crmevents.SystemActor())
	assert.ErrorIs(t, err, tasks.ErrNotFound)
}

// --- Reassigning ---

func TestReassign(t *testing.T) {
	db, svc, org, contact, user := setup(t)
	newOwner := testutil.CreateTestUser(t, db, org.ID)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	moved, err := svc.Reassign(ctx(), org.ID, task.ID, newOwner.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Equal(t, newOwner.ID, moved.OwnerID)
}

func TestReassign_RejectsOutsiders(t *testing.T) {
	_, svc, org, contact, user := setup(t)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	_, err = svc.Reassign(ctx(), org.ID, task.ID, uuid.New(), crmevents.SystemActor())
	assert.ErrorContains(t, err, "not an active member")
}

// --- Listing ---

// Overdue is derived from status and due_at, never stored: a stored flag would
// be wrong between the moment a task lapses and the next job tick.
func TestList_OverdueIsDerived(t *testing.T) {
	db, svc, org, contact, user := setup(t)

	overdue, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)
	require.NoError(t, db.Model(&models.Task{}).Where("id = ?", overdue.ID).
		Update("due_at", time.Now().UTC().Add(-time.Hour)).Error)

	_, err = svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	rows, total, err := svc.List(ctx(), org.ID, tasks.ListOpts{OverdueOnly: true})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, overdue.ID, rows[0].ID)
	assert.True(t, rows[0].IsOverdue(time.Now().UTC()))

	// Completing it takes it out of overdue immediately, with no job involved.
	_, err = svc.Complete(ctx(), org.ID, overdue.ID, crmevents.SystemActor())
	require.NoError(t, err)

	_, total, err = svc.List(ctx(), org.ID, tasks.ListOpts{OverdueOnly: true})
	require.NoError(t, err)
	assert.Zero(t, total)
}

func TestList_FiltersByOwnerAndContact(t *testing.T) {
	db, svc, org, contact, user := setup(t)
	otherUser := testutil.CreateTestUser(t, db, org.ID)
	otherContact := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15554100001"))

	mine, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)

	in := basicInput(org.ID, otherContact.ID, user.ID)
	in.OwnerID = &otherUser.ID
	_, err = svc.Create(ctx(), in)
	require.NoError(t, err)

	rows, total, err := svc.List(ctx(), org.ID, tasks.ListOpts{OwnerID: &user.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, mine.ID, rows[0].ID)

	_, total, err = svc.List(ctx(), org.ID, tasks.ListOpts{ContactID: &otherContact.ID})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
}

func TestList_SortsBySoonestDeadline(t *testing.T) {
	db, svc, org, contact, user := setup(t)

	late, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)
	require.NoError(t, db.Model(&models.Task{}).Where("id = ?", late.ID).
		Update("due_at", time.Now().UTC().Add(48*time.Hour)).Error)

	soon, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)
	require.NoError(t, db.Model(&models.Task{}).Where("id = ?", soon.ID).
		Update("due_at", time.Now().UTC().Add(time.Hour)).Error)

	rows, _, err := svc.List(ctx(), org.ID, tasks.ListOpts{})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, soon.ID, rows[0].ID, "the most urgent task comes first")
}

// --- Events ---

func TestTaskLifecycle_RecordsEvents(t *testing.T) {
	db, svc, org, contact, user := setup(t)
	require.NoError(t, db.Exec("DELETE FROM crm_event_outbox WHERE organization_id = ?", org.ID).Error)

	task, err := svc.Create(ctx(), basicInput(org.ID, contact.ID, user.ID))
	require.NoError(t, err)
	_, err = svc.Complete(ctx(), org.ID, task.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	var types []string
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND contact_id = ?", org.ID, contact.ID).
		Order("occurred_at").Pluck("type", &types).Error)

	assert.Contains(t, types, "task.created")
	assert.Contains(t, types, "task.completed")
}
