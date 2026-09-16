package websocket_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForMessage reads one message from a client's channel, or fails.
func waitForMessage(t *testing.T, client *websocket.Client) websocket.WSMessage {
	t.Helper()
	select {
	case data := <-websocket.ClientSendChan(client):
		var msg websocket.WSMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a message")
		return websocket.WSMessage{}
	}
}

// Plan 10, S10: a message the server could not deliver has to be admitted to.
//
// A full client buffer meant the message was discarded with nothing but a
// server log line. The client went on believing it was up to date — showing a
// conversation that had since been resolved, or an inbox missing the message
// that just arrived — and nothing corrected it until the agent happened to
// reload the page, which they had no reason to do.
func TestHub_TellsAClientWhenItsMessagesAreBeingDropped(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	userID := uuid.New()

	client := newTestClient(hub, userID, orgID)
	hub.Register(client)
	waitForClientCount(t, hub, 1)

	websocket.ClientFillSendBuffer(client)

	hub.BroadcastToUser(orgID, userID, websocket.WSMessage{
		Type:    websocket.TypeNewMessage,
		Payload: map[string]any{"id": uuid.NewString()},
	})

	// Let the hub process while the buffer is still full — draining first
	// would free a slot and the message would simply fit.
	time.Sleep(200 * time.Millisecond)

	// The notice has to arrive even though the buffer was full — that is the
	// whole point. Drain past the filler looking for it.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data := websocket.ClientDrainOne(client)
		if data == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var msg websocket.WSMessage
		if err := json.Unmarshal(data, &msg); err == nil && msg.Type == websocket.TypeResyncRequired {
			return
		}
	}
	t.Fatal("a dropped message must produce a resync notice, not silence")
}

// A client keeping up gets the message itself and no notice.
func TestHub_DeliversNormallyWhenTheClientKeepsUp(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	userID := uuid.New()

	client := newTestClient(hub, userID, orgID)
	hub.Register(client)
	waitForClientCount(t, hub, 1)

	hub.BroadcastToUser(orgID, userID, websocket.WSMessage{
		Type:    websocket.TypeNewMessage,
		Payload: map[string]any{"id": "abc"},
	})

	msg := waitForMessage(t, client)
	assert.Equal(t, websocket.TypeNewMessage, msg.Type)
}

// A contact-targeted message reaches the client looking at that contact.
func TestHub_ContactTargetedMessageReachesTheViewer(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	contactID := uuid.New()

	viewer := newTestClient(hub, uuid.New(), orgID)
	websocket.ClientSetCurrentContact(viewer, &contactID)
	hub.Register(viewer)
	waitForClientCount(t, hub, 1)

	hub.BroadcastToContact(orgID, contactID, websocket.WSMessage{
		Type:    websocket.TypeConversationNoteCreated,
		Payload: map[string]any{"contact_id": contactID.String()},
	})

	msg := waitForMessage(t, viewer)
	assert.Equal(t, websocket.TypeConversationNoteCreated, msg.Type)
}

// And not the client looking at a different one.
func TestHub_ContactTargetedMessageSkipsSomeoneOnAnotherContact(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	contactID := uuid.New()
	otherContact := uuid.New()

	elsewhere := newTestClient(hub, uuid.New(), orgID)
	websocket.ClientSetCurrentContact(elsewhere, &otherContact)
	hub.Register(elsewhere)
	waitForClientCount(t, hub, 1)

	hub.BroadcastToContact(orgID, contactID, websocket.WSMessage{
		Type:    websocket.TypeConversationNoteCreated,
		Payload: map[string]any{"contact_id": contactID.String()},
	})

	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, websocket.ClientDrainOne(elsewhere),
		"a note for another contact must not reach this client")
}

// The setter and the hub's read run on different goroutines; under -race this
// is the test that fails if the guard is removed.
func TestHub_CurrentContactSurvivesConcurrentUpdates(t *testing.T) {
	hub := newTestHub(t)
	orgID := uuid.New()
	contactID := uuid.New()

	client := newTestClient(hub, uuid.New(), orgID)
	hub.Register(client)
	waitForClientCount(t, hub, 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			id := uuid.New()
			websocket.ClientSetCurrentContact(client, &id)
			websocket.ClientSetCurrentContact(client, nil)
		}
	}()

	for i := 0; i < 200; i++ {
		hub.BroadcastToContact(orgID, contactID, websocket.WSMessage{
			Type:    websocket.TypeConversationNoteCreated,
			Payload: map[string]any{"n": i},
		})
		websocket.ClientDrainOne(client)
	}
	<-done
}
