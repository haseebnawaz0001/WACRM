package tasks_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// taskDueAt creates an open task for one owner with a fixed deadline.
func taskDueAt(t *testing.T, db *gorm.DB, orgID, ownerID, contactID, typeID uuid.UUID, due time.Time) uuid.UUID {
	t.Helper()
	task := models.Task{
		OrganizationID: orgID,
		ContactID:      contactID,
		TypeID:         typeID,
		OwnerID:        ownerID,
		Title:          "Follow up",
		Status:         models.TaskOpen,
		DueAt:          due,
	}
	require.NoError(t, db.Create(&task).Error)
	return task.ID
}

func seedTaskType(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	taskType := models.TaskType{OrganizationID: orgID, Key: "follow_up", Label: "Follow up"}
	require.NoError(t, db.Create(&taskType).Error)
	return taskType.ID
}

// The badge is "what can I act on now", not "how many follow-ups exist".
// A number that counts next month's plan is a number people learn to ignore.
func TestDueFor_CountsOverdueAndTodayOnly(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := seedTaskType(t, db, org.ID)

	now := time.Now().UTC()
	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, now.Add(-2*time.Hour))
	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, now.Add(-48*time.Hour))
	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, now.Add(30*time.Minute))
	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, now.Add(20*24*time.Hour))

	counts, err := tasks.New(db).DueFor(context.Background(), org.ID, owner.ID, time.UTC)
	require.NoError(t, err)

	assert.EqualValues(t, 2, counts.Overdue)
	assert.EqualValues(t, 1, counts.DueToday, "a task later today is actionable now")
	assert.EqualValues(t, 3, counts.Total())
}

// One person's badge must not count another person's work.
func TestDueFor_IsPerOwner(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	mine := testutil.CreateTestUser(t, db, org.ID)
	theirs := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := seedTaskType(t, db, org.ID)

	now := time.Now().UTC()
	taskDueAt(t, db, org.ID, mine.ID, contact.ID, typeID, now.Add(-time.Hour))
	taskDueAt(t, db, org.ID, theirs.ID, contact.ID, typeID, now.Add(-time.Hour))

	counts, err := tasks.New(db).DueFor(context.Background(), org.ID, mine.ID, time.UTC)
	require.NoError(t, err)
	assert.EqualValues(t, 1, counts.Overdue)
}

// A completed task is not waiting on anybody.
func TestDueFor_IgnoresClosedTasks(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := seedTaskType(t, db, org.ID)

	id := taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, time.Now().UTC().Add(-time.Hour))
	require.NoError(t, db.Model(&models.Task{}).Where("id = ?", id).
		Update("status", models.TaskCompleted).Error)

	counts, err := tasks.New(db).DueFor(context.Background(), org.ID, owner.ID, time.UTC)
	require.NoError(t, err)
	assert.Zero(t, counts.Total())
}

// "Due today" is a statement about the owner's calendar, not the server's. A
// task due at 23:00 in Lagos is today's problem for someone in Lagos.
func TestDueFor_UsesTheOwnersTimezone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := seedTaskType(t, db, org.ID)

	lagos, err := time.LoadLocation("Africa/Lagos")
	require.NoError(t, err)

	// End of the Lagos day, which is still today there and may already be
	// tomorrow in UTC.
	local := time.Now().In(lagos)
	endOfLagosDay := time.Date(local.Year(), local.Month(), local.Day(), 22, 30, 0, 0, lagos)
	if endOfLagosDay.Before(time.Now()) {
		t.Skip("the Lagos day has already ended; nothing to distinguish")
	}
	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, endOfLagosDay.UTC())

	counts, err := tasks.New(db).DueFor(context.Background(), org.ID, owner.ID, lagos)
	require.NoError(t, err)
	assert.EqualValues(t, 1, counts.DueToday)
}

// Deleting a contact keeps their tasks but takes them out of the counts: a
// badge asking someone to chase a customer the organization removed is wrong.
func TestDueFor_ExcludesDeletedContacts(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := seedTaskType(t, db, org.ID)

	taskDueAt(t, db, org.ID, owner.ID, contact.ID, typeID, time.Now().UTC().Add(-time.Hour))
	require.NoError(t, db.Delete(&models.Contact{}, "id = ?", contact.ID).Error)

	counts, err := tasks.New(db).DueFor(context.Background(), org.ID, owner.ID, time.UTC)
	require.NoError(t, err)
	assert.Zero(t, counts.Total())
}
