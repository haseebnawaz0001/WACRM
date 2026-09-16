package crmevents

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Redis keys used by the relay.
const (
	// EventStreamKey is the automation engine's event stream (plan 08).
	EventStreamKey = "wacrm:crm_events"

	// EventStreamMaxLen bounds the stream so an idle consumer cannot grow
	// it without limit. Approximate trimming is much cheaper than exact.
	EventStreamMaxLen = 200000

	// WSFanoutChannel carries realtime events to every server replica.
	// Each replica relays what it receives to its own local hub, which is
	// what lets an event published by the worker reach browser clients.
	WSFanoutChannel = "wacrm:ws_fanout"
)

// Sink handles one event during relay. A sink must be fast: the relay holds
// the claiming transaction open while sinks run, so anything slow (an outbound
// HTTP call) has to be handed off asynchronously rather than awaited.
type Sink interface {
	Name() string
	Handle(ctx context.Context, e Event) error
}

// envelope is the wire format for both the stream and the fan-out channel.
// event_id lets a consumer deduplicate if it sees an event twice.
type envelope struct {
	EventID     uuid.UUID      `json:"event_id"`
	Type        string         `json:"type"`
	OrgID       uuid.UUID      `json:"organization_id"`
	ContactID   *uuid.UUID     `json:"contact_id,omitempty"`
	SubjectType string         `json:"subject_type,omitempty"`
	SubjectID   *uuid.UUID     `json:"subject_id,omitempty"`
	ActorType   string         `json:"actor_type"`
	ActorID     *uuid.UUID     `json:"actor_id,omitempty"`
	ActorName   string         `json:"actor_name,omitempty"`
	Data        map[string]any `json:"data"`
	OccurredAt  string         `json:"occurred_at"`

	// Origin travels with the event because automation loop protection is
	// evaluated by a consumer in another process: a depth that stopped at the
	// outbox row would leave the engine unable to tell a rule's own output
	// from a person's change.
	OriginRuleID *uuid.UUID `json:"origin_rule_id,omitempty"`
	OriginDepth  int        `json:"origin_depth,omitempty"`
}

func newEnvelope(e Event) envelope {
	return envelope{
		EventID:      e.ID,
		Type:         e.Type,
		OrgID:        e.OrgID,
		ContactID:    e.ContactID,
		SubjectType:  e.Subject.Type,
		SubjectID:    e.Subject.ID,
		ActorType:    e.Actor.Type,
		ActorID:      e.Actor.ID,
		ActorName:    e.Actor.Name,
		Data:         e.Data,
		OccurredAt:   e.OccurredAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
		OriginRuleID: e.Origin.RuleID,
		OriginDepth:  e.Origin.Depth,
	}
}

// StreamSink appends automatable events to the Redis stream the automation
// engine consumes. Events that are not flagged automatable are skipped.
type StreamSink struct {
	Redis *redis.Client
}

func (s *StreamSink) Name() string { return "stream" }

func (s *StreamSink) Handle(ctx context.Context, e Event) error {
	spec, ok := Lookup(e.Type)
	if !ok || !spec.Automatable {
		return nil
	}
	payload, err := json.Marshal(newEnvelope(e))
	if err != nil {
		return err
	}
	return s.Redis.XAdd(ctx, &redis.XAddArgs{
		Stream: EventStreamKey,
		MaxLen: EventStreamMaxLen,
		Approx: true,
		Values: map[string]any{
			"event_id": e.ID.String(),
			"type":     e.Type,
			"org_id":   e.OrgID.String(),
			"payload":  payload,
		},
	}).Err()
}

// WSFanoutSink publishes every event to the Redis channel each replica
// subscribes to, so realtime delivery no longer depends on the event having
// been produced by the process that happens to hold the client's socket.
type WSFanoutSink struct {
	Redis *redis.Client
}

func (s *WSFanoutSink) Name() string { return "ws_fanout" }

func (s *WSFanoutSink) Handle(ctx context.Context, e Event) error {
	payload, err := json.Marshal(newEnvelope(e))
	if err != nil {
		return err
	}
	return s.Redis.Publish(ctx, WSFanoutChannel, payload).Err()
}

// DecodeFanout parses a message received on WSFanoutChannel. Subscribers use
// it to rebuild the event without importing the envelope type.
func DecodeFanout(payload []byte) (Event, error) {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return Event{}, err
	}
	return Event{
		ID:        env.EventID,
		Type:      env.Type,
		OrgID:     env.OrgID,
		ContactID: env.ContactID,
		Subject:   Subject{Type: env.SubjectType, ID: env.SubjectID},
		Actor:     Actor{Type: env.ActorType, ID: env.ActorID, Name: env.ActorName},
		Data:      env.Data,
		Origin:    Origin{RuleID: env.OriginRuleID, Depth: env.OriginDepth},
	}, nil
}

// WebhookDispatcher delivers one event to the organization's subscribed
// webhooks. It is satisfied by the handlers package, which owns the webhook
// cache, the signing secret and the HTTP client.
//
// Implementations must return promptly — start the delivery and return rather
// than waiting on the remote endpoint, because the relay calls this while its
// claiming transaction is open.
type WebhookDispatcher interface {
	DispatchEvent(orgID uuid.UUID, eventID uuid.UUID, eventType string, data map[string]any)
}

// WebhookSink hands events flagged for webhook delivery to the dispatcher.
type WebhookSink struct {
	Dispatcher WebhookDispatcher
}

func (s *WebhookSink) Name() string { return "webhook" }

func (s *WebhookSink) Handle(_ context.Context, e Event) error {
	spec, ok := Lookup(e.Type)
	if !ok || !spec.Webhook || s.Dispatcher == nil {
		return nil
	}
	s.Dispatcher.DispatchEvent(e.OrgID, e.ID, e.Type, e.Data)
	return nil
}

// ActivityRecorder writes a contact-timeline row for an event. It is satisfied
// by internal/activity, which crmevents cannot import directly without a cycle.
type ActivityRecorder interface {
	Handle(tx *gorm.DB, e Event) error
}

// ActivitySink records timeline entries for events the catalog flags with
// RecordActivity.
//
// It runs inside the relay's claiming transaction, so an event and its timeline
// row are written together: a contact's history cannot end up missing an entry
// for a change that was delivered everywhere else.
type ActivitySink struct {
	DB       *gorm.DB
	Recorder ActivityRecorder
}

func (s *ActivitySink) Name() string { return "activity" }

func (s *ActivitySink) Handle(_ context.Context, e Event) error {
	spec, ok := Lookup(e.Type)
	if !ok || !spec.RecordActivity || e.ContactID == nil || s.Recorder == nil {
		return nil
	}
	return s.Recorder.Handle(s.DB, e)
}
