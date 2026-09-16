package handlers

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneNodeFlow builds the smallest flow that reaches a terminal node in a single
// run, so the completion settings can be exercised without driving a dialogue.
func oneNodeFlow(orgID uuid.UUID, accountName string) *models.ChatbotFlow {
	return &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		WhatsAppAccount: accountName,
		Name:            "settings-flow",
		IsEnabled:       true,
		Graph: models.JSONB{
			"version":    2,
			"entry_node": "m1",
			"nodes": []any{
				map[string]any{
					"id": "m1", "type": "message", "label": "only",
					"config": map[string]any{"message": "Hi"},
				},
			},
			"edges": []any{},
		},
	}
}

// The builder let an author write a completion message and saved it; the runner
// never sent it (plan 10, X14).
func TestRunChatGraph_SendsCompletionMessage(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := oneNodeFlow(org.ID, account.Name)
	flow.CompletionMessage = "Thanks, we have everything we need."
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	require.NoError(t, app.DB.First(session, session.ID).Error)
	assert.Equal(t, models.SessionStatusCompleted, session.Status)

	var messages []models.Message
	require.NoError(t, app.DB.Where("contact_id = ? AND direction = ?",
		contact.ID, models.DirectionOutgoing).Find(&messages).Error)

	var sent bool
	for _, m := range messages {
		if m.Content == flow.CompletionMessage {
			sent = true
		}
	}
	assert.True(t, sent, "the completion message the author configured must actually be sent")
}

// A flow with no completion message must not send an empty one.
func TestRunChatGraph_NoCompletionMessageSendsNothingExtra(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := oneNodeFlow(org.ID, account.Name)
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	var count int64
	require.NoError(t, app.DB.Model(&models.Message{}).
		Where("contact_id = ? AND direction = ? AND content = ''",
			contact.ID, models.DirectionOutgoing).Count(&count).Error)
	assert.Zero(t, count, "an unset completion message must not produce an empty send")
}

// on_complete_action=create_record stores the collected answers against the
// contact, and the runner's own bookkeeping keys are not answers.
func TestRunChatGraph_CreateRecordStoresAnswers(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := oneNodeFlow(org.ID, account.Name)
	flow.OnCompleteAction = "create_record"
	require.NoError(t, app.DB.Create(flow).Error)

	session.SessionData = models.JSONB{
		"order_number": "NH-10237",
		"__path__":     []any{"m1"},
	}
	require.NoError(t, app.DB.Save(session).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	var updated models.Contact
	require.NoError(t, app.DB.Where("id = ?", contact.ID).First(&updated).Error)

	responses, ok := updated.Metadata["flow_responses"].(map[string]any)
	require.True(t, ok, "flow answers should be stored on the contact")
	entry, ok := responses[flow.Name].(map[string]any)
	require.True(t, ok)
	answers, ok := entry["answers"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "NH-10237", answers["order_number"])
	assert.NotContains(t, answers, "__path__", "runner bookkeeping is not an answer")
}

func TestMatchesCancelKeyword(t *testing.T) {
	flow := &models.ChatbotFlow{CancelKeywords: models.StringArray{"cancel", "Stop"}}

	assert.True(t, matchesCancelKeyword(flow, "cancel"))
	assert.True(t, matchesCancelKeyword(flow, "  CANCEL "), "matching is case- and space-insensitive")
	assert.True(t, matchesCancelKeyword(flow, "stop"))

	assert.False(t, matchesCancelKeyword(flow, "cancellation policy"),
		"a keyword inside a sentence is an answer, not a cancellation")
	assert.False(t, matchesCancelKeyword(flow, ""))
	assert.False(t, matchesCancelKeyword(&models.ChatbotFlow{}, "cancel"))
	assert.False(t, matchesCancelKeyword(nil, "cancel"))
}

func TestIsExcludedNumber(t *testing.T) {
	list := models.JSONBArray{"+1 415 555 0123", "447700900456"}

	assert.True(t, isExcludedNumber(list, "14155550123"),
		"punctuation and spacing must not decide whether an exclusion applies")
	assert.True(t, isExcludedNumber(list, "+44 7700 900456"))
	assert.False(t, isExcludedNumber(list, "14155550124"))
	assert.False(t, isExcludedNumber(models.JSONBArray{}, "14155550123"))
	assert.False(t, isExcludedNumber(list, ""))
}

// A panel field marked save_to_field becomes a contact field when the flow
// completes (plan 10, S7).
//
// Panel fields are session data by default — they describe one conversation.
// Some of the values are facts about the customer, and an author should be able
// to say so on the field rather than adding a node just to copy it across.
func TestRunChatGraph_PanelSaveToFieldWritesContactField(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))

	flow := oneNodeFlow(org.ID, account.Name)
	flow.PanelConfig = models.JSONB{
		"sections": []any{
			map[string]any{
				"id": "s1", "label": "Order", "order": 1,
				"fields": []any{
					// Marked: belongs on the record.
					map[string]any{"key": "company_name", "label": "Company",
						"order": 1, "save_to_field": models.FieldKeyCompany},
					// Unmarked: stays session-only.
					map[string]any{"key": "order_number", "label": "Order", "order": 2},
				},
			},
		},
	}
	require.NoError(t, app.DB.Create(flow).Error)

	session.SessionData = models.JSONB{
		"company_name": "Harbour View Suites",
		"order_number": "NH-10237",
	}
	require.NoError(t, app.DB.Save(session).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	values, err := customfields.New(app.DB).Values(t.Context(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Harbour View Suites", values[models.FieldKeyCompany])
	assert.NotContains(t, values, "order_number",
		"an unmarked panel field stays session data")
}

// A flow with no panel config must not fail on completion.
func TestRunChatGraph_NoPanelConfigCompletesCleanly(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := oneNodeFlow(org.ID, account.Name)
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	require.NoError(t, app.DB.First(session, session.ID).Error)
	assert.Equal(t, models.SessionStatusCompleted, session.Status)
}
