package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	ws "github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subscribedClient returns a registered client watching the given topics.
func subscribedClient(t *testing.T, hub *ws.Hub, orgID, userID uuid.UUID, topics ...string) *ws.Client {
	t.Helper()
	client := ws.NewClient(hub, nil, userID, orgID)
	hub.Register(client)

	if len(topics) > 0 {
		body, err := json.Marshal(map[string]any{
			"type":    ws.TypeSubscribe,
			"payload": map[string]any{"topics": topics},
		})
		require.NoError(t, err)
		ws.ClientHandleMessage(client, body)
	}
	return client
}

func receives(t *testing.T, client *ws.Client, wantType string) bool {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case data := <-ws.ClientSendChan(client):
			var msg ws.WSMessage
			if err := json.Unmarshal(data, &msg); err == nil && msg.Type == wantType {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// A single "current contact" could describe only one thing at a time, which is
// why a split view — profile beside chat — could not work: opening the second
// silently unsubscribed the first.
func TestSubscribe_ClientWatchesSeveralTopicsAtOnce(t *testing.T) {
	hub := newTestHub(t)
	orgID, userID := uuid.New(), uuid.New()

	first, second := uuid.New().String(), uuid.New().String()
	client := subscribedClient(t, hub, orgID, userID,
		ws.ContactTopic(first), ws.ConversationTopic(second))

	assert.True(t, client.Subscribed(ws.ContactTopic(first)))
	assert.True(t, client.Subscribed(ws.ConversationTopic(second)))
	assert.Len(t, client.Topics(), 2)
}

// A topic message reaches only the clients that asked for it. Notes for a
// contact an agent is not looking at used to land in their notes store.
func TestBroadcastToTopic_OnlyReachesSubscribers(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	contactID := uuid.New().String()

	watching := subscribedClient(t, hub, orgID, uuid.New(), ws.ContactTopic(contactID))
	elsewhere := subscribedClient(t, hub, orgID, uuid.New(), ws.ContactTopic(uuid.New().String()))

	hub.BroadcastToTopic(orgID, ws.ContactTopic(contactID), ws.WSMessage{
		Type:    ws.TypeContactUpdated,
		Payload: map[string]any{"contact_id": contactID},
	})

	assert.True(t, receives(t, watching, ws.TypeContactUpdated))
	assert.False(t, receives(t, elsewhere, ws.TypeContactUpdated),
		"a client that did not ask for this contact must not be told about it")
}

func TestUnsubscribe_StopsDelivery(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	contactID := uuid.New().String()
	topic := ws.ContactTopic(contactID)

	client := subscribedClient(t, hub, orgID, uuid.New(), topic)

	body, err := json.Marshal(map[string]any{
		"type":    ws.TypeUnsubscribe,
		"payload": map[string]any{"topics": []string{topic}},
	})
	require.NoError(t, err)
	ws.ClientHandleMessage(client, body)

	assert.False(t, client.Subscribed(topic))

	hub.BroadcastToTopic(orgID, topic, ws.WSMessage{Type: ws.TypeContactUpdated})
	assert.False(t, receives(t, client, ws.TypeContactUpdated))
}

// Subscriptions are client-controlled, so the list has to be bounded.
func TestSubscribe_IsBounded(t *testing.T) {
	hub := newTestHub(t)
	client := subscribedClient(t, hub, uuid.New(), uuid.New())

	topics := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		topics = append(topics, ws.ContactTopic(uuid.New().String()))
	}
	body, err := json.Marshal(map[string]any{
		"type":    ws.TypeSubscribe,
		"payload": map[string]any{"topics": topics},
	})
	require.NoError(t, err)
	ws.ClientHandleMessage(client, body)

	assert.LessOrEqual(t, len(client.Topics()), 50,
		"one socket must not be able to make the server remember an unbounded list")
}

// An older frontend that never subscribes must keep working while both
// delivery models are live.
func TestBroadcast_UnsubscribedClientsStillGetUntargetedMessages(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()

	legacy := subscribedClient(t, hub, orgID, uuid.New())

	hub.BroadcastToOrg(orgID, ws.WSMessage{Type: ws.TypeContactUpdated})
	assert.True(t, receives(t, legacy, ws.TypeContactUpdated))
}
