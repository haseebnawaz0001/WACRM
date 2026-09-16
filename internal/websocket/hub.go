package websocket

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/zerodha/logf"
)

// Hub maintains the set of active clients and broadcasts messages to them
type Hub struct {
	// clients maps organization ID -> user ID -> set of clients (supports multiple tabs)
	clients map[uuid.UUID]map[uuid.UUID]map[*Client]struct{}

	// broadcast channel for messages
	broadcast chan BroadcastMessage

	// register channel for new clients
	register chan *Client

	// unregister channel for disconnecting clients
	unregister chan *Client

	// mutex for thread-safe access to clients map
	mu sync.RWMutex

	// logger
	log logf.Logger
}

// NewHub creates a new Hub instance
func NewHub(log logf.Logger) *Hub {
	return &Hub{
		clients:    make(map[uuid.UUID]map[uuid.UUID]map[*Client]struct{}),
		broadcast:  make(chan BroadcastMessage, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		log:        log,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)
		}
	}
}

// registerClient adds a client to the hub
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	orgClients, ok := h.clients[client.organizationID]
	if !ok {
		orgClients = make(map[uuid.UUID]map[*Client]struct{})
		h.clients[client.organizationID] = orgClients
	}

	userClients, ok := orgClients[client.userID]
	if !ok {
		userClients = make(map[*Client]struct{})
		orgClients[client.userID] = userClients
	}

	// Add this client to the set (allows multiple tabs)
	userClients[client] = struct{}{}

	h.log.Info("WebSocket client registered",
		"user_id", client.userID,
		"org_id", client.organizationID,
		"user_connections", len(userClients),
		"total_clients", h.countClients())
}

// unregisterClient removes a client from the hub
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if orgClients, ok := h.clients[client.organizationID]; ok {
		if userClients, ok := orgClients[client.userID]; ok {
			if _, exists := userClients[client]; exists {
				delete(userClients, client)
				close(client.send)

				// Clean up empty user map
				if len(userClients) == 0 {
					delete(orgClients, client.userID)
				}

				// Clean up empty org map
				if len(orgClients) == 0 {
					delete(h.clients, client.organizationID)
				}
			}
		}
	}

	h.log.Info("WebSocket client unregistered",
		"user_id", client.userID,
		"org_id", client.organizationID,
		"total_clients", h.countClients())
}

// broadcastMessage sends a message to all relevant clients
func (h *Hub) broadcastMessage(msg BroadcastMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	orgClients, ok := h.clients[msg.OrgID]
	if !ok {
		return
	}

	data, err := json.Marshal(msg.Message)
	if err != nil {
		h.log.Error("Failed to marshal broadcast message", "error", err)
		return
	}

	// If UserID is specified, only send to that user's clients
	if msg.UserID != uuid.Nil {
		userClients, ok := orgClients[msg.UserID]
		if !ok {
			return
		}
		for client := range userClients {
			select {
			case client.send <- data:
			default:
				h.dropped(client, msg.Message.Type)
			}
		}
		return
	}

	// Iterate through all users in the organization
	for _, userClients := range orgClients {
		// Iterate through all clients (tabs) for each user
		for client := range userClients {
			// If ContactID is specified, only send to clients viewing that contact
			// Read through the accessor: currentContact is written on the
			// client's own goroutine.
			viewing := client.CurrentContact()
			if msg.ContactID != uuid.Nil && viewing != nil && *viewing != msg.ContactID {
				continue
			}

			select {
			case client.send <- data:
			default:
				h.dropped(client, msg.Message.Type)
			}
		}
	}
}

// dropped records a message a client could not keep up with, and tells the
// client it has a hole in its stream (plan 10, S10).
//
// A full send buffer used to mean the message was discarded with only a server
// log line to show for it. The client went on believing it was up to date,
// showing a conversation that had since been resolved or an inbox missing the
// message that just arrived, and nothing would correct it until the agent
// happened to reload.
//
// If the notice does not fit either — which is the normal case, the buffer
// being full is why we are here — one queued message is discarded to make room.
// That is not a loss: this client is already missing messages, and "refetch
// everything" supersedes whatever was waiting in the queue.
func (h *Hub) dropped(client *Client, msgType string) {
	h.log.Warn("Client send buffer full, dropping message",
		"user_id", client.userID,
		"org_id", client.organizationID,
		"message_type", msgType)

	notice, err := json.Marshal(WSMessage{
		Type:    TypeResyncRequired,
		Payload: map[string]any{"dropped_type": msgType},
	})
	if err != nil {
		return
	}

	select {
	case client.send <- notice:
		return
	default:
	}

	select {
	case <-client.send:
	default:
		// The write pump emptied it in the meantime; nothing to evict.
	}
	select {
	case client.send <- notice:
	default:
		// Racing with a writer that refilled it. The connection is beyond
		// help; the read pump will close it and the client reconnects.
	}
}

// Broadcast sends a message to the broadcast channel
func (h *Hub) Broadcast(msg BroadcastMessage) {
	select {
	case h.broadcast <- msg:
	default:
		h.log.Warn("Broadcast channel full, dropping message")
	}
}

// BroadcastToOrg sends a message to all clients in an organization
func (h *Hub) BroadcastToOrg(orgID uuid.UUID, msg WSMessage) {
	h.Broadcast(BroadcastMessage{
		OrgID:   orgID,
		Message: msg,
	})
}

// BroadcastToContact sends a message to clients viewing a specific contact
func (h *Hub) BroadcastToContact(orgID, contactID uuid.UUID, msg WSMessage) {
	h.Broadcast(BroadcastMessage{
		OrgID:     orgID,
		ContactID: contactID,
		Message:   msg,
	})
}

// BroadcastToUser sends a message to a specific user
func (h *Hub) BroadcastToUser(orgID, userID uuid.UUID, msg WSMessage) {
	h.Broadcast(BroadcastMessage{
		OrgID:   orgID,
		UserID:  userID,
		Message: msg,
	})
}

// BroadcastToUsers sends a message to multiple users
func (h *Hub) BroadcastToUsers(orgID uuid.UUID, userIDs []uuid.UUID, msg WSMessage) {
	for _, userID := range userIDs {
		h.BroadcastToUser(orgID, userID, msg)
	}
}

// countClients returns the total number of connected clients
func (h *Hub) countClients() int {
	count := 0
	for _, orgClients := range h.clients {
		for _, userClients := range orgClients {
			count += len(userClients)
		}
	}
	return count
}

// GetClientCount returns the number of connected clients (thread-safe)
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.countClients()
}

// IsUserOnline returns true if the user has at least one active WebSocket connection.
func (h *Hub) IsUserOnline(orgID, userID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if orgClients, ok := h.clients[orgID]; ok {
		if userClients, ok := orgClients[userID]; ok {
			return len(userClients) > 0
		}
	}
	return false
}

// OnlineUserIDs returns every user ID in the org that has at least one
// active WebSocket connection. Used by ListUsers for the online-only
// filter and the online-count badge.
func (h *Hub) OnlineUserIDs(orgID uuid.UUID) []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	orgClients, ok := h.clients[orgID]
	if !ok {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(orgClients))
	for uid, clients := range orgClients {
		if len(clients) > 0 {
			ids = append(ids, uid)
		}
	}
	return ids
}

// FilterOnlineUsers returns only the user IDs that have active WebSocket connections.
func (h *Hub) FilterOnlineUsers(orgID uuid.UUID, userIDs []uuid.UUID) []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	orgClients, ok := h.clients[orgID]
	if !ok {
		return nil
	}

	online := make([]uuid.UUID, 0, len(userIDs))
	for _, uid := range userIDs {
		if userClients, ok := orgClients[uid]; ok && len(userClients) > 0 {
			online = append(online, uid)
		}
	}
	return online
}

// Register adds a client to the hub via the register channel
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub via the unregister channel
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}
