package websocket

import "github.com/google/uuid"

// ClientSendChan exposes the client's send channel for testing.
func ClientSendChan(c *Client) <-chan []byte {
	return c.send
}

// ClientAuthenticated returns whether the client has authenticated.
func ClientAuthenticated(c *Client) bool {
	return c.authenticated
}

// ClientUserID returns the client's user ID.
func ClientUserID(c *Client) uuid.UUID {
	return c.userID
}

// ClientOrgID returns the client's organization ID.
func ClientOrgID(c *Client) uuid.UUID {
	return c.organizationID
}

// ClientHandleAuthMessage exposes handleAuthMessage for testing.
func ClientHandleAuthMessage(c *Client, data []byte) bool {
	return c.handleAuthMessage(data)
}

// ClientSetCurrentContact exposes the guarded setter for testing.
func ClientSetCurrentContact(c *Client, contactID *uuid.UUID) {
	c.setCurrentContact(contactID)
}

// ClientFillSendBuffer packs the client's send channel so the next broadcast
// has nowhere to go — the condition the resync notice exists for.
func ClientFillSendBuffer(c *Client) {
	for {
		select {
		case c.send <- []byte("{}"):
		default:
			return
		}
	}
}

// ClientDrainOne takes one message off the client's send channel.
func ClientDrainOne(c *Client) []byte {
	select {
	case data := <-c.send:
		return data
	default:
		return nil
	}
}

// ClientHandleMessage exposes the client's inbound message handling, so tests
// exercise the wire format rather than an internal shortcut.
func ClientHandleMessage(c *Client, data []byte) {
	c.handleMessage(data)
}
