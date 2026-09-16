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

// crmNodeFlow wires one node between a start and two labelled ends, so the
// outcome the node returns is observable as the node the session lands on.
func crmNodeFlow(orgID uuid.UUID, account string, nodeType ChatNodeType,
	config map[string]any, edges []any) *models.ChatbotFlow {

	return &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		WhatsAppAccount: account,
		Name:            "crm-node-flow",
		IsEnabled:       true,
		Graph: models.JSONB{
			"version":    2,
			"entry_node": "n1",
			"nodes": []any{
				map[string]any{"id": "n1", "type": string(nodeType), "label": "node", "config": config},
				map[string]any{"id": "yes", "type": "message", "label": "yes",
					"config": map[string]any{"message": "YES"}},
				map[string]any{"id": "no", "type": "message", "label": "no",
					"config": map[string]any{"message": "NO"}},
			},
			"edges": edges,
		},
	}
}

func lastOutgoing(t *testing.T, app *App, contactID uuid.UUID) string {
	t.Helper()
	var msg models.Message
	err := app.DB.Where("contact_id = ? AND direction = ?", contactID, models.DirectionOutgoing).
		Order("created_at DESC").First(&msg).Error
	if err != nil {
		return ""
	}
	return msg.Content
}

// A crm_action node must actually change the contact — that is the whole point
// of giving the chatbot the action library (plan 10, S7).
func TestChatCRMActionNode_RunsLibraryAction(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := crmNodeFlow(org.ID, account.Name, ChatNodeCRMAction, map[string]any{
		"actions": []any{
			map[string]any{"type": "add_tags", "config": map[string]any{"tags": []any{"VIP"}}},
		},
	}, []any{
		map[string]any{"from": "n1", "to": "yes", "condition": "default"},
		map[string]any{"from": "n1", "to": "no", "condition": "error"},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	var updated models.Contact
	require.NoError(t, app.DB.Where("id = ?", contact.ID).First(&updated).Error)

	var tags []string
	for _, tag := range updated.Tags {
		if s, ok := tag.(string); ok {
			tags = append(tags, s)
		}
	}
	assert.Contains(t, tags, "VIP", "the action must have run against the real contact")
	assert.Equal(t, "YES", lastOutgoing(t, app, contact.ID), "a successful run takes the default edge")
}

// A failing action takes the "error" edge so a flow can apologise rather than
// carry on as though it worked.
func TestChatCRMActionNode_UnknownActionTakesErrorEdge(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := crmNodeFlow(org.ID, account.Name, ChatNodeCRMAction, map[string]any{
		"actions": []any{map[string]any{"type": "no_such_action", "config": map[string]any{}}},
	}, []any{
		map[string]any{"from": "n1", "to": "yes", "condition": "default"},
		map[string]any{"from": "n1", "to": "no", "condition": "error"},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	assert.Equal(t, "NO", lastOutgoing(t, app, contact.ID))
}

// The condition asks the database about the contact, so it agrees with what the
// same filter would select in the contacts list.
func TestChatCRMConditionNode_BranchesOnContactState(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("tags", models.JSONBArray{"VIP"}).Error)

	config := map[string]any{
		"filter": map[string]any{
			"op": "and",
			"rules": []any{
				map[string]any{"field": "tags", "operator": "contains_any", "value": []any{"VIP"}},
			},
		},
	}
	edges := []any{
		map[string]any{"from": "n1", "to": "yes", "condition": "true"},
		map[string]any{"from": "n1", "to": "no", "condition": "false"},
	}

	flow := crmNodeFlow(org.ID, account.Name, ChatNodeCRMCondition, config, edges)
	require.NoError(t, app.DB.Create(flow).Error)
	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	assert.Equal(t, "YES", lastOutgoing(t, app, contact.ID), "a VIP contact takes the true edge")

	// The same flow, a contact without the tag.
	app2, org2, account2, contact2, session2 := newGraphTestFixtures(t)
	flow2 := crmNodeFlow(org2.ID, account2.Name, ChatNodeCRMCondition, config, edges)
	require.NoError(t, app2.DB.Create(flow2).Error)
	require.NoError(t, app2.runChatGraph(account2, contact2, session2, flow2, "start", "", nil))
	assert.Equal(t, "NO", lastOutgoing(t, app2, contact2.ID))
}

// An unusable condition must not strand the customer mid-conversation.
func TestChatCRMConditionNode_UnevaluableTakesFalseEdge(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := crmNodeFlow(org.ID, account.Name, ChatNodeCRMCondition, map[string]any{
		"segment_id": uuid.New().String(), // no such segment
	}, []any{
		map[string]any{"from": "n1", "to": "yes", "condition": "true"},
		map[string]any{"from": "n1", "to": "no", "condition": "false"},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	assert.Equal(t, "NO", lastOutgoing(t, app, contact.ID))
}

// save_to_field is what turns a collected answer into a contact record rather
// than a session scratchpad that disappears when the flow ends.
func TestChatPrompt_SaveToFieldWritesContactField(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	flow := &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "collect-email",
		IsEnabled:       true,
		Graph: models.JSONB{
			"version":    2,
			"entry_node": "p1",
			"nodes": []any{
				map[string]any{"id": "p1", "type": "prompt", "label": "ask",
					"config": map[string]any{
						"body":          "What is your email?",
						"store_as":      "email",
						"save_to_field": models.FieldKeyEmail,
					}},
			},
			"edges": []any{},
		},
	}
	require.NoError(t, app.DB.Create(flow).Error)

	// The built-in field definitions come from the org seed, which a bare test
	// organization does not run.
	require.NoError(t, orgseed.Seed(app.DB, org.ID))

	// First run sends the prompt and waits.
	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	// Second run supplies the answer.
	require.NoError(t, app.runChatGraph(account, contact, session, flow, "ada@example.com", "", nil))

	values, err := customfields.New(app.DB).Values(t.Context(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "ada@example.com", values[models.FieldKeyEmail],
		"the answer should be on the contact record, not only in the session")
}
