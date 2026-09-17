package websocket

import "github.com/google/uuid"

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Message types
const (
	TypeAuth          = "auth"
	TypeNewMessage    = "new_message"
	TypeStatusUpdate  = "status_update"
	TypeContactUpdate = "contact_update"
	TypeSetContact    = "set_contact"
	// TypeSubscribe / TypeUnsubscribe replace the single-valued set_contact
	// (plan 10, S10). A client says what it is watching — a contact, a
	// conversation, a pipeline board — rather than naming one contact and
	// silently losing the others.
	TypeSubscribe   = "subscribe"
	TypeUnsubscribe = "unsubscribe"
	TypePing        = "ping"
	TypePong        = "pong"

	// Agent transfer types
	TypeAgentTransfer       = "agent_transfer"
	TypeAgentTransferResume = "agent_transfer_resume"
	TypeAgentTransferAssign = "agent_transfer_assign"
	TypeTransferEscalation  = "transfer_escalation"
	TypeTransferExpired     = "transfer_expired"
	TypeTransferEscalated   = "transfer_escalated"

	// Campaign types
	TypeCampaignStatsUpdate = "campaign_stats_update"

	// Permission types
	TypePermissionsUpdated = "permissions_updated"

	// TypeCRMEvent carries a domain event relayed from the CRM event outbox.
	// The payload is ids and the event type only — never event content — so
	// it is safe to broadcast org-wide. Clients refetch the affected record
	// with their own permissions.
	TypeCRMEvent = "crm_event"

	// TypeNotificationCreated pushes a stored notification to its owner.
	TypeNotificationCreated = "notification_created"

	// TypeDealUpdated tells open boards that a card moved. The payload carries
	// ids and positions only: a board the viewer may not see refetches with
	// their own permissions rather than trusting the broadcast.
	TypeDealUpdated = "deal_updated"

	// Conversation note types
	TypeConversationNoteCreated = "conversation_note_created"
	TypeConversationNoteUpdated = "conversation_note_updated"
	TypeConversationNoteDeleted = "conversation_note_deleted"

	// Call types
	TypeCallIncoming = "call_incoming"
	TypeCallAnswered = "call_answered"
	TypeCallEnded    = "call_ended"

	// Call transfer types
	TypeCallTransferWaiting    = "call_transfer_waiting"
	TypeCallTransferConnected  = "call_transfer_connected"
	TypeCallTransferCompleted  = "call_transfer_completed"
	TypeCallTransferAbandoned  = "call_transfer_abandoned"
	TypeCallTransferNoAnswer   = "call_transfer_no_answer"
	TypeCallTransferReassigned = "call_transfer_reassigned"

	// Call hold types
	TypeCallHold    = "call_hold"
	TypeCallResumed = "call_resumed"

	// Outgoing call types
	TypeOutgoingCallInitiated = "outgoing_call_initiated"
	TypeOutgoingCallRinging   = "outgoing_call_ringing"
	TypeOutgoingCallAnswered  = "outgoing_call_answered"
	TypeOutgoingCallRejected  = "outgoing_call_rejected"
	TypeOutgoingCallEnded     = "outgoing_call_ended"

	// Call permission types
	TypeCallPermissionUpdate = "call_permission_update"
)

// BroadcastMessage represents a message to be broadcast to clients
type BroadcastMessage struct {
	OrgID     uuid.UUID
	UserID    uuid.UUID // Optional: only send to specific user
	ContactID uuid.UUID // Optional: only send to users viewing this contact
	// Topic delivers only to clients that subscribed to it (plan 10, S10).
	// It takes precedence over ContactID: a client that has said what it is
	// watching should be taken at its word.
	Topic   string
	Message WSMessage
}

// AuthPayload is the payload for auth messages from client
type AuthPayload struct {
	Token string `json:"token"`
}

// SetContactPayload is the payload for set_contact messages from client
type SetContactPayload struct {
	ContactID string `json:"contact_id"`
}

// StatusUpdatePayload is the payload for status_update messages
type StatusUpdatePayload struct {
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

// TypeResyncRequired tells a client its stream has a hole in it (plan 10, S10).
//
// A client whose send buffer is full had its messages dropped and was told
// nothing, so its view silently stopped matching the server: a conversation
// that had been resolved still looked open, and the only cure was a page
// reload the agent had no reason to perform. Saying "you missed something"
// costs one message and lets the client refetch.
const TypeResyncRequired = "resync_required"

// SubscribePayload is the payload for subscribe / unsubscribe messages.
type SubscribePayload struct {
	Topics []string `json:"topics"`
}

// Topic names. Kept as constructors rather than raw strings so the server and
// the client cannot disagree about the spelling — a mismatch would be a
// subscription that silently receives nothing.
func ContactTopic(id string) string       { return "contact:" + id }
func ConversationTopic(id string) string  { return "conversation:" + id }
func BoardTopic(pipelineID string) string { return "board:" + pipelineID }

// New WebSocket event types (plan 10, S10). Payloads carry ids and
// non-sensitive fields; clients refetch details with their own permissions.
const (
	TypeContactUpdated      = "contact_updated"
	TypeContactMerged       = "contact_merged"
	TypeConversationUpdated = "conversation_updated"
	TypeTaskUpdated         = "task_updated"
	TypeCustomFieldsUpdated = "custom_fields_updated"
	TypeSegmentCountUpdated = "segment_count_updated"
)
