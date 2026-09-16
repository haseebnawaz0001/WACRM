package automation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// waitingConversation creates a conversation the customer has gone quiet on.
func waitingConversation(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, agentAt time.Time) *models.Conversation {
	t.Helper()
	row := models.Conversation{
		BaseModel:          models.BaseModel{ID: uuid.New()},
		OrganizationID:     orgID,
		ContactID:          contactID,
		Status:             models.ConversationOpen,
		BotActive:          false,
		WhatsAppAccount:    "acct",
		LastAgentMessageAt: &agentAt,
	}
	require.NoError(t, db.Create(&row).Error)
	return &row
}

func timeRule(t *testing.T, svc *automation.Service, orgID uuid.UUID, triggerType string, cfg map[string]any) *models.AutomationRule {
	t.Helper()
	enabled := true
	rule, err := svc.Create(ctx(), orgID, automation.Input{
		Name:          "Chase",
		TriggerType:   triggerType,
		TriggerConfig: cfg,
		Enabled:       &enabled,
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags,
			Config: crmactions.Config{"tags": []any{"Chased"}},
		}},
	})
	require.NoError(t, err)
	return rule
}

// Nothing happens when a customer does not reply, which is exactly why
// somebody has to go looking.
func TestRunTimeTriggers_FiresWhenTheCustomerHasGoneQuiet(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	timeRule(t, svc, org.ID, automation.TriggerNoCustomerReply, map[string]any{
		"after": map[string]any{"amount": 2.0, "unit": "days"},
	})

	waitingConversation(t, db, org.ID, contact.ID, time.Now().UTC().Add(-72*time.Hour))

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 1, fired)
	assert.Contains(t, contactTags(t, db, contact.ID), "Chased")
}

func TestRunTimeTriggers_LeavesARecentConversationAlone(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	timeRule(t, svc, org.ID, automation.TriggerNoCustomerReply, map[string]any{
		"after": map[string]any{"amount": 2.0, "unit": "days"},
	})

	waitingConversation(t, db, org.ID, contact.ID, time.Now().UTC().Add(-time.Hour))

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 0, fired)
}

// A customer who has replied is not quiet, however long ago we wrote.
func TestRunTimeTriggers_ACustomerReplyDisarmsTheRule(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	timeRule(t, svc, org.ID, automation.TriggerNoCustomerReply, map[string]any{
		"after": map[string]any{"amount": 2.0, "unit": "days"},
	})

	agentAt := time.Now().UTC().Add(-72 * time.Hour)
	replied := agentAt.Add(time.Hour)
	conversation := waitingConversation(t, db, org.ID, contact.ID, agentAt)
	require.NoError(t, db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).
		Update("last_customer_message_at", replied).Error)

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 0, fired)
}

// A tick every five minutes must not chase the same customer every five
// minutes.
func TestRunTimeTriggers_FireOncePerWaitingPeriod(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	timeRule(t, svc, org.ID, automation.TriggerNoCustomerReply, map[string]any{
		"after": map[string]any{"amount": 2.0, "unit": "days"},
	})

	conversation := waitingConversation(t, db, org.ID, contact.ID, time.Now().UTC().Add(-72*time.Hour))

	first, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	require.Equal(t, 1, first)

	second, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 0, second, "the same waiting period must not fire twice")

	// A new agent message starts a new waiting period, and the rule re-arms.
	require.NoError(t, db.Model(&models.Conversation{}).Where("id = ?", conversation.ID).
		Update("last_agent_message_at", time.Now().UTC().Add(-71*time.Hour)).Error)

	third, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 1, third, "a fresh agent message re-arms the rule")
}

// Nobody is late while the chatbot is mid-answer.
func TestRunTimeTriggers_NoAgentReplySkipsBotHandledConversations(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	timeRule(t, svc, org.ID, automation.TriggerNoAgentReply, map[string]any{
		"after": map[string]any{"amount": 1.0, "unit": "hours"},
	})

	waiting := time.Now().UTC().Add(-4 * time.Hour)
	row := models.Conversation{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       contact.ID,
		Status:          models.ConversationOpen,
		BotActive:       true,
		WhatsAppAccount: "acct",
		WaitingSince:    &waiting,
	}
	require.NoError(t, db.Create(&row).Error)

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 0, fired)

	require.NoError(t, db.Model(&models.Conversation{}).Where("id = ?", row.ID).
		Update("bot_active", false).Error)

	fired, err = engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 1, fired, "once a person owns it, the clock is real")
}

// A renewal date seven days out is the canonical reason to want this.
func TestRunTimeTriggers_DateFieldFiresAtTheOffset(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)

	field := models.CustomFieldDefinition{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		EntityType:     models.FieldEntityContact,
		Key:            "renewal_date",
		Label:          "Renewal date",
		Type:           models.FieldTypeDate,
	}
	require.NoError(t, db.Create(&field).Error)

	due := time.Now().AddDate(0, 0, 7)
	require.NoError(t, db.Create(&models.CustomFieldValue{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		EntityType:     models.FieldEntityContact,
		EntityID:       contact.ID,
		FieldID:        field.ID,
		ValueDate:      &due,
	}).Error)

	timeRule(t, svc, org.ID, automation.TriggerDateField, map[string]any{
		"field": "renewal_date", "offset_days": -7.0,
	})

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 1, fired)
	assert.Contains(t, contactTags(t, db, contact.ID), "Chased")
}

func TestRunTimeTriggers_DateFieldIgnoresOtherDates(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)

	field := models.CustomFieldDefinition{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		EntityType:     models.FieldEntityContact,
		Key:            "renewal_date",
		Label:          "Renewal date",
		Type:           models.FieldTypeDate,
	}
	require.NoError(t, db.Create(&field).Error)

	far := time.Now().AddDate(0, 2, 0)
	require.NoError(t, db.Create(&models.CustomFieldValue{
		ID:             uuid.New(),
		OrganizationID: org.ID,
		EntityType:     models.FieldEntityContact,
		EntityID:       contact.ID,
		FieldID:        field.ID,
		ValueDate:      &far,
	}).Error)

	timeRule(t, svc, org.ID, automation.TriggerDateField, map[string]any{
		"field": "renewal_date", "offset_days": -7.0,
	})

	fired, err := engine.RunTimeTriggers(ctx())
	require.NoError(t, err)
	assert.Equal(t, 0, fired)
}

// Switching on a rule against a long-quiet inbox must not message everyone at
// once — the failure people most fear, and the hardest to take back.
func TestTimeTriggerBatch_IsBounded(t *testing.T) {
	assert.LessOrEqual(t, automation.TimeTriggerBatch, 1000)
}
