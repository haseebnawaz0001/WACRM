package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apiCallFlow builds a one-node flow whose api_call hits the given URL.
func apiCallFlow(orgID uuid.UUID, account, url string, config map[string]any) *models.ChatbotFlow {
	nodeConfig := map[string]any{"url": url, "method": "GET"}
	for k, v := range config {
		nodeConfig[k] = v
	}
	return &models.ChatbotFlow{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		WhatsAppAccount: account,
		Name:            "lookup",
		IsEnabled:       true,
		Graph: models.JSONB{
			"version":    2,
			"entry_node": "a1",
			"nodes": []any{
				map[string]any{"id": "a1", "type": "api_call", "label": "lookup", "config": nodeConfig},
			},
			"edges": []any{},
		},
	}
}

// Plan 10, S7: an api_call can write straight onto the contact record.
//
// response_mapping cannot do this — its keys are stored flat in SessionData, so
// a field key put there becomes a session variable of that literal name and
// never reaches the contact.
func TestExecChatAPICall_FieldMappingWritesContactFields(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"account": map[string]any{"name": "Casa Luma Hotel"},
				"contact": map[string]any{"email": "ada@casaluma.example"},
				"missing": nil,
			},
		})
	}))
	defer server.Close()

	flow := apiCallFlow(org.ID, account.Name, server.URL, map[string]any{
		"field_mapping": map[string]any{
			models.FieldKeyCompany: "data.account.name",
			models.FieldKeyEmail:   "data.contact.email",
		},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	values, err := customfields.New(app.DB).Values(t.Context(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Casa Luma Hotel", values[models.FieldKeyCompany])
	assert.Equal(t, "ada@casaluma.example", values[models.FieldKeyEmail])
}

// An API that omits a key is not saying "delete this": a path that resolves to
// nothing must leave the field the customer already has alone.
func TestExecChatAPICall_FieldMappingDoesNotBlankOnMissingPath(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)
	require.NoError(t, orgseed.Seed(app.DB, org.ID))

	fields := customfields.New(app.DB)
	_, err := fields.SetValues(app.DB, org.ID, contact.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyCompany: "Existing Ltd"}, nil)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer server.Close()

	flow := apiCallFlow(org.ID, account.Name, server.URL, map[string]any{
		"field_mapping": map[string]any{models.FieldKeyCompany: "data.account.name"},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))

	values, err := fields.Values(t.Context(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Existing Ltd", values[models.FieldKeyCompany],
		"a missing path must not erase what the contact already had")
}

// response_mapping keeps its own behaviour: session scratch state, untouched.
func TestExecChatAPICall_ResponseMappingStillGoesToSession(t *testing.T) {
	app, org, account, contact, session := newGraphTestFixtures(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "CUST-7"}})
	}))
	defer server.Close()

	flow := apiCallFlow(org.ID, account.Name, server.URL, map[string]any{
		"response_mapping": map[string]any{"customer_id": "data.id"},
	})
	require.NoError(t, app.DB.Create(flow).Error)

	require.NoError(t, app.runChatGraph(account, contact, session, flow, "start", "", nil))
	require.NoError(t, app.DB.First(session, session.ID).Error)

	assert.Equal(t, "CUST-7", session.SessionData["customer_id"])
}
