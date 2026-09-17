package entityrefs_test

import (
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A table added later with a contact_id is exactly the one nobody remembers to
// add to a literal list, and the cost of forgetting is history that silently
// detaches from the surviving record.
func TestContactRefTables_IsDiscoveredAndExcludesTheSpecialCases(t *testing.T) {
	db := testutil.SetupTestDB(t)

	tables, err := entityrefs.ContactRefTables(db)
	require.NoError(t, err)

	for _, expected := range []string{
		"messages", "tasks", "deals", "call_logs", "agent_transfers",
		"contact_activities", "conversation_notes",
		// The ones the old literal list missed.
		"bulk_message_recipients", "chatbot_sessions", "contact_identities",
	} {
		assert.Contains(t, tables, expected, "%s references a contact and must be re-pointed", expected)
	}

	for _, excluded := range []string{
		"contacts", "conversations", "automation_contact_state", "contact_merges",
	} {
		assert.NotContains(t, tables, excluded, "%s is handled deliberately, not swept", excluded)
	}
}

func TestRepointContact_MovesEverythingThatReferencedTheSecondary(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	primary := testutil.CreateTestContact(t, db, org.ID)
	secondary := testutil.CreateTestContact(t, db, org.ID)

	session := models.ChatbotSession{
		OrganizationID:  org.ID,
		ContactID:       secondary.ID,
		WhatsAppAccount: "acct",
		Status:          models.SessionStatusCompleted,
		StartedAt:       time.Now().UTC(),
	}
	require.NoError(t, db.Create(&session).Error)

	moved, err := entityrefs.RepointContact(db, secondary.ID, primary.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, moved["chatbot_sessions"],
		"the merge snapshot has to say what it moved, or a merge is a leap of faith")

	var after models.ChatbotSession
	require.NoError(t, db.Where("id = ?", session.ID).First(&after).Error)
	assert.Equal(t, primary.ID, after.ContactID)
}

// A notification linking to a contact that no longer opens is a dead end in
// somebody's bell. It keys on (entity_type, entity_id), so it cannot be found
// by column name — which is exactly why it was missed.
func TestRepointContact_MovesNotifications(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)
	primary := testutil.CreateTestContact(t, db, org.ID)
	secondary := testutil.CreateTestContact(t, db, org.ID)

	notification := models.Notification{
		OrganizationID: org.ID,
		UserID:         user.ID,
		Type:           "task_due",
		Title:          "Task due",
		EntityType:     "contact",
		EntityID:       &secondary.ID,
	}
	require.NoError(t, db.Create(&notification).Error)

	moved, err := entityrefs.RepointContact(db, secondary.ID, primary.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, moved["notifications"])

	var after models.Notification
	require.NoError(t, db.Where("id = ?", notification.ID).First(&after).Error)
	require.NotNil(t, after.EntityID)
	assert.Equal(t, primary.ID, *after.EntityID)
}

// Both contacts having state for the same rule is the collision a plain UPDATE
// cannot survive — and getting it wrong means a once-per-contact rule fires
// again for somebody it has already messaged.
func TestRepointContact_MergesAutomationStateOnCollision(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	primary := testutil.CreateTestContact(t, db, org.ID)
	secondary := testutil.CreateTestContact(t, db, org.ID)

	rule := models.AutomationRule{
		OrganizationID: org.ID,
		Name:           "Welcome",
		TriggerType:    "contact.created",
		Actions:        models.JSONB{"list": []any{}},
	}
	require.NoError(t, db.Create(&rule).Error)

	older := time.Now().UTC().Add(-48 * time.Hour)
	newer := time.Now().UTC().Add(-time.Hour)

	require.NoError(t, db.Create(&models.AutomationContactState{
		RuleID: rule.ID, ContactID: primary.ID, LastRunAt: older, RunCount: 2,
	}).Error)
	require.NoError(t, db.Create(&models.AutomationContactState{
		RuleID: rule.ID, ContactID: secondary.ID, LastRunAt: newer, RunCount: 3,
	}).Error)

	_, err := entityrefs.RepointContact(db, secondary.ID, primary.ID)
	require.NoError(t, err)

	var state models.AutomationContactState
	require.NoError(t, db.Where("rule_id = ? AND contact_id = ?", rule.ID, primary.ID).
		First(&state).Error)
	assert.Equal(t, 5, state.RunCount, "the merged record has done both records' runs")
	assert.WithinDuration(t, newer, state.LastRunAt, time.Second,
		"a cooldown measured from the last run must not reset because two records were joined")

	var leftover int64
	require.NoError(t, db.Model(&models.AutomationContactState{}).
		Where("contact_id = ?", secondary.ID).Count(&leftover).Error)
	assert.Zero(t, leftover, "nothing may be left filed under the id that was merged away")
}

// State for only one side is the ordinary case and must simply move.
func TestRepointContact_MovesAutomationStateWithoutACollision(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	primary := testutil.CreateTestContact(t, db, org.ID)
	secondary := testutil.CreateTestContact(t, db, org.ID)

	rule := models.AutomationRule{
		OrganizationID: org.ID,
		Name:           "Welcome",
		TriggerType:    "contact.created",
		Actions:        models.JSONB{"list": []any{}},
	}
	require.NoError(t, db.Create(&rule).Error)
	require.NoError(t, db.Create(&models.AutomationContactState{
		RuleID: rule.ID, ContactID: secondary.ID, LastRunAt: time.Now().UTC(), RunCount: 1,
	}).Error)

	_, err := entityrefs.RepointContact(db, secondary.ID, primary.ID)
	require.NoError(t, err)

	var state models.AutomationContactState
	require.NoError(t, db.Where("rule_id = ? AND contact_id = ?", rule.ID, primary.ID).
		First(&state).Error)
	assert.Equal(t, 1, state.RunCount)
}

func TestRepointContact_ReportsNothingWhenThereIsNothingToMove(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	primary := testutil.CreateTestContact(t, db, org.ID)
	secondary := testutil.CreateTestContact(t, db, org.ID)

	moved, err := entityrefs.RepointContact(db, secondary.ID, primary.ID)
	require.NoError(t, err)
	assert.Empty(t, moved)
}
