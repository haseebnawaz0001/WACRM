package reports_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/reports"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// conversationWith inserts a conversation with explicit timings, which is the
// only way to test percentiles against hand-computed values.
func conversationWith(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, row models.Conversation) models.Conversation {
	t.Helper()
	row.ID = uuid.New()
	row.OrganizationID = orgID
	row.ContactID = contactID
	if row.WhatsAppAccount == "" {
		row.WhatsAppAccount = "acct"
	}
	if row.Status == "" {
		row.Status = models.ConversationOpen
	}
	require.NoError(t, db.Create(&row).Error)
	return row
}

// anotherContact makes a distinct contact, because the schema allows only one
// active conversation per contact.
func anotherContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, n int) *models.Contact {
	t.Helper()
	return testutil.CreateTestContactWith(t, db, orgID,
		testutil.WithPhoneNumber(fmt.Sprintf("1555940%04d", n)))
}

// One conversation left open over a weekend moves an average enough to hide a
// whole team's week, which is why the report is medians.
func TestAgentPerformance_ReportsMedianAndP90FirstResponse(t *testing.T) {
	db, svc, org, viewer := setup(t)
	agent := testutil.CreateTestUser(t, db, org.ID)
	base := time.Now().UTC().Add(-2 * time.Hour)
	// Responses of 60s, 120s, 600s: the median is 120. Each needs its own
	// contact, because a contact may only have one active conversation.
	for i, seconds := range []int{60, 120, 600} {
		opened := base.Add(time.Duration(i) * time.Minute)
		customer := opened
		responded := opened.Add(time.Duration(seconds) * time.Second)
		conversationWith(t, db, org.ID, anotherContact(t, db, org.ID, i).ID, models.Conversation{
			BaseModel:              models.BaseModel{ID: uuid.New()},
			OpenedAt:               opened,
			FirstCustomerMessageAt: &customer,
			FirstResponseAt:        &responded,
			FirstResponderID:       &agent.ID,
		})
	}

	result, err := svc.AgentPerformance(ctx(), viewer, lastMonth(), nil)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)

	row := result.Rows[0]
	assert.Equal(t, int64(3), row.Handled)
	require.NotNil(t, row.FirstResponseMedianSeconds)
	assert.InDelta(t, 120, *row.FirstResponseMedianSeconds, 0.01)
	require.NotNil(t, row.FirstResponseP90Seconds)
	assert.Greater(t, *row.FirstResponseP90Seconds, *row.FirstResponseMedianSeconds)
}

// A conversation that comes straight back was not really resolved.
func TestAgentPerformance_ReportsTheReopenedRate(t *testing.T) {
	db, svc, org, viewer := setup(t)
	agent := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)

	opened := time.Now().UTC().Add(-3 * time.Hour)
	resolved := opened.Add(time.Hour)

	for i, reopened := range []int{0, 1} {
		conversationWith(t, db, org.ID, contact.ID, models.Conversation{
			BaseModel:     models.BaseModel{ID: uuid.New()},
			OpenedAt:      opened.Add(time.Duration(i) * time.Minute),
			ResolvedAt:    &resolved,
			ResolvedByID:  &agent.ID,
			ReopenedCount: reopened,
			Status:        models.ConversationResolved,
		})
	}

	result, err := svc.AgentPerformance(ctx(), viewer, lastMonth(), nil)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(2), result.Rows[0].Resolved)
	assert.InDelta(t, 50.0, result.Rows[0].ReopenedRate, 0.01)
}

// A leaderboard everyone can read is a performance review nobody agreed to.
func TestAgentPerformance_AnAgentSeesOnlyTheirOwnRow(t *testing.T) {
	db, svc, org, _ := setup(t)
	mine := testutil.CreateTestUser(t, db, org.ID)
	theirs := testutil.CreateTestUser(t, db, org.ID)

	opened := time.Now().UTC().Add(-time.Hour)
	for i, agent := range []uuid.UUID{mine.ID, theirs.ID} {
		responder := agent
		conversationWith(t, db, org.ID, anotherContact(t, db, org.ID, 10+i).ID, models.Conversation{
			BaseModel:        models.BaseModel{ID: uuid.New()},
			OpenedAt:         opened,
			FirstResponderID: &responder,
		})
	}

	restricted := reports.Viewer{OrgID: org.ID, UserID: mine.ID, SeesEveryone: false}
	result, err := svc.AgentPerformance(ctx(), restricted, lastMonth(), nil)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, mine.ID.String(), result.Rows[0].UserID)
}

// "Median response time" means three different things depending on who is
// asked, so the report says which one it is.
func TestAgentPerformance_SaysWhatTheDurationsMeasure(t *testing.T) {
	_, svc, _, viewer := setup(t)

	result, err := svc.AgentPerformance(ctx(), viewer, lastMonth(), nil)
	require.NoError(t, err)
	assert.Contains(t, result.Note, "calendar time")
}

// --- R4: tasks by agent ---

func newTask(t *testing.T, db *gorm.DB, orgID, contactID, ownerID, typeID uuid.UUID, row models.Task) {
	t.Helper()
	row.ID = uuid.New()
	row.OrganizationID = orgID
	row.ContactID = contactID
	row.OwnerID = ownerID
	row.TypeID = typeID
	if row.Title == "" {
		row.Title = "Follow up"
	}
	if row.Status == "" {
		row.Status = models.TaskOpen
	}
	if row.Priority == "" {
		row.Priority = "normal"
	}
	if row.Source == "" {
		row.Source = models.TaskSourceManual
	}
	require.NoError(t, db.Create(&row).Error)
}

func taskTypeFor(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	row := models.TaskType{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: orgID,
		Key:            models.TaskTypeFollowUp,
		Label:          "Follow up",
	}
	require.NoError(t, db.Create(&row).Error)
	return row.ID
}

// A stored overdue flag is wrong from the moment a task lapses until the next
// job tick, which is exactly when somebody is reading this.
func TestTasksByAgent_CountsOverdueFromTheDeadline(t *testing.T) {
	db, svc, org, viewer := setup(t)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := taskTypeFor(t, db, org.ID)

	newTask(t, db, org.ID, contact.ID, owner.ID, typeID, models.Task{
		DueAt: time.Now().UTC().Add(-time.Hour),
	})
	newTask(t, db, org.ID, contact.ID, owner.ID, typeID, models.Task{
		DueAt: time.Now().UTC().Add(72 * time.Hour),
	})

	result, err := svc.TasksByAgent(ctx(), viewer, lastMonth(), nil, "")
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, int64(2), result.Rows[0].Open)
	assert.Equal(t, int64(1), result.Rows[0].Overdue)
}

// Whether deadlines mean anything is the whole point of the column.
func TestTasksByAgent_ReportsTheOnTimeRate(t *testing.T) {
	db, svc, org, viewer := setup(t)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := taskTypeFor(t, db, org.ID)

	due := time.Now().UTC().Add(-24 * time.Hour)
	early := due.Add(-time.Hour)
	late := due.Add(2 * time.Hour)

	newTask(t, db, org.ID, contact.ID, owner.ID, typeID, models.Task{
		DueAt: due, Status: models.TaskCompleted, CompletedAt: &early,
	})
	newTask(t, db, org.ID, contact.ID, owner.ID, typeID, models.Task{
		DueAt: due, Status: models.TaskCompleted, CompletedAt: &late,
	})

	result, err := svc.TasksByAgent(ctx(), viewer, lastMonth(), nil, "")
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)

	row := result.Rows[0]
	assert.Equal(t, int64(2), row.Completed)
	assert.InDelta(t, 50.0, row.OnTimeRate, 0.01)
	require.NotNil(t, row.MedianLateSeconds)
	assert.InDelta(t, 7200, *row.MedianLateSeconds, 1)
}

func TestTasksByAgent_AnAgentSeesOnlyTheirOwnRow(t *testing.T) {
	db, svc, org, _ := setup(t)
	mine := testutil.CreateTestUser(t, db, org.ID)
	theirs := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	typeID := taskTypeFor(t, db, org.ID)

	for _, owner := range []uuid.UUID{mine.ID, theirs.ID} {
		newTask(t, db, org.ID, contact.ID, owner, typeID, models.Task{
			DueAt: time.Now().UTC().Add(time.Hour),
		})
	}

	restricted := reports.Viewer{OrgID: org.ID, UserID: mine.ID, SeesEveryone: false}
	result, err := svc.TasksByAgent(ctx(), restricted, lastMonth(), nil, "")
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Equal(t, mine.ID.String(), result.Rows[0].UserID)
}
