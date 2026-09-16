package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/activity"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/schedule"
	"github.com/shridarpatil/whatomate/internal/websocket"
	"gorm.io/gorm"
)

// OutboundEventPayload is the webhook envelope for events delivered through
// the CRM event outbox.
//
// It extends the legacy OutboundWebhookPayload shape with two ids, so a
// receiver can deduplicate retries and correlate what it received with what we
// recorded: event_id is stable across every delivery of the same domain event,
// delivery_id identifies this one attempt at this one endpoint.
type OutboundEventPayload struct {
	Event      string    `json:"event"`
	EventID    string    `json:"event_id"`
	DeliveryID string    `json:"delivery_id"`
	Timestamp  time.Time `json:"timestamp"`
	Data       any       `json:"data"`
}

// eventData converts a typed webhook payload struct into the generic map the
// outbox stores.
//
// It round-trips through JSON deliberately: the resulting keys are exactly the
// json tags external receivers already depend on, so moving a call site onto
// the outbox cannot silently change the shape of a delivered payload.
func eventData(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// PublishEvent writes a domain event to the outbox outside a transaction.
//
// Errors are logged rather than returned: these call sites sit at the end of a
// request that has already succeeded, and failing the user's request because an
// event could not be recorded would be worse than the missing event. Call sites
// that do run in a transaction should use PublishEventTx instead, which is the
// only form that guarantees the event and the change stay consistent.
func (a *App) PublishEvent(e crmevents.Event) {
	a.PublishEventTx(a.DB, e)
}

// PublishEventTx writes a domain event to the outbox using the caller's
// transaction, so the event commits or rolls back with the change.
func (a *App) PublishEventTx(tx *gorm.DB, e crmevents.Event) {
	if err := crmevents.PublishTx(tx, e); err != nil {
		a.Log.Error("failed to publish CRM event", "event_type", e.Type, "error", err)
	}
}

// DispatchEvent delivers one outbox event to every webhook in the org that
// subscribes to it, and records each attempt in webhook_deliveries.
//
// It satisfies crmevents.WebhookDispatcher. The relay calls this while holding
// its claiming transaction, so the work is handed to a background goroutine and
// this returns immediately.
func (a *App) DispatchEvent(orgID uuid.UUID, eventID uuid.UUID, eventType string, data map[string]any) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		a.deliverEventToWebhooks(ctx, orgID, eventID, eventType, data)
	}()
}

func (a *App) deliverEventToWebhooks(ctx context.Context, orgID, eventID uuid.UUID, eventType string, data map[string]any) {
	webhooks, err := a.getWebhooksCached(orgID)
	if err != nil {
		a.Log.Error("failed to fetch webhooks for event", "error", err, "event_type", eventType)
		return
	}

	for _, webhook := range webhooks {
		if !containsEvent(webhook.Events, eventType) {
			continue
		}
		if ctx.Err() != nil {
			a.Log.Warn("event webhook dispatch cancelled", "reason", ctx.Err(), "event_type", eventType)
			return
		}
		a.deliverAndLog(ctx, webhook, orgID, eventID, eventType, data)
	}
}

// deliverAndLog performs the delivery with retries and writes one
// webhook_deliveries row describing the outcome.
func (a *App) deliverAndLog(ctx context.Context, webhook models.Webhook, orgID, eventID uuid.UUID, eventType string, data map[string]any) {
	deliveryID := uuid.New()

	payload := OutboundEventPayload{
		Event:      eventType,
		EventID:    eventID.String(),
		DeliveryID: deliveryID.String(),
		Timestamp:  time.Now().UTC(),
		Data:       data,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		a.Log.Error("failed to marshal event webhook payload", "error", err, "webhook_id", webhook.ID)
		return
	}

	var (
		start      = time.Now()
		lastErr    error
		statusCode int
		attempts   int
	)

	const maxRetries = 3
retry:
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 2s, 4s.
			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
				break retry
			case <-time.After(time.Duration(1<<attempt) * time.Second):
			}
		}
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		attempts++
		statusCode, lastErr = a.sendWebhookRequest(ctx, webhook, body)
		if lastErr == nil {
			break
		}
	}

	record := &models.WebhookDelivery{
		ID:             deliveryID,
		OrganizationID: orgID,
		WebhookID:      webhook.ID,
		EventID:        &eventID,
		EventType:      eventType,
		URL:            webhook.URL,
		StatusCode:     statusCode,
		Success:        lastErr == nil,
		Attempts:       attempts,
		DurationMs:     time.Since(start).Milliseconds(),
	}
	if lastErr != nil {
		record.Error = lastErr.Error()
		a.Log.Error("event webhook delivery failed",
			"webhook_id", webhook.ID, "event", eventType, "url", webhook.URL, "error", lastErr)
	} else {
		a.Log.Debug("event webhook delivered",
			"webhook_id", webhook.ID, "event", eventType, "url", webhook.URL)
	}

	if err := a.DB.Create(record).Error; err != nil {
		a.Log.Error("failed to record webhook delivery", "error", err, "webhook_id", webhook.ID)
	}
}

// StartCRMEventSubscriber subscribes this replica to the CRM event fan-out
// channel and relays what arrives to its local WebSocket hub.
//
// Every replica subscribes, which is what lets an event published by a process
// that holds no browser sockets — the campaign worker, the calling manager, a
// scheduler job — still reach connected clients.
//
// Only ids and the event type are forwarded, never the event payload. Event
// data can contain message content and phone numbers, and this broadcast is
// org-wide; clients refetch the affected record through the API, which applies
// their own permissions. Narrowing delivery to the users who can see a record
// is plan 10's S10/S9 work.
func (a *App) StartCRMEventSubscriber(ctx context.Context) error {
	if a.WSHub == nil {
		a.Log.Warn("WebSocket hub not initialized, skipping CRM event subscriber")
		return nil
	}

	pubsub := a.Redis.Subscribe(ctx, crmevents.WSFanoutChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return err
	}

	ch := pubsub.Channel()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() { _ = pubsub.Close() }()
		for {
			select {
			case <-ctx.Done():
				a.Log.Info("CRM event subscriber shutting down")
				return
			case msg, ok := <-ch:
				if !ok {
					a.Log.Info("CRM event fan-out channel closed")
					return
				}
				event, err := crmevents.DecodeFanout([]byte(msg.Payload))
				if err != nil {
					a.Log.Error("failed to decode CRM event fan-out message", "error", err)
					continue
				}
				a.WSHub.BroadcastToOrg(event.OrgID, websocket.WSMessage{
					Type:    websocket.TypeCRMEvent,
					Payload: crmEventPayload(event),
				})
			}
		}
	}()

	a.Log.Info("CRM event subscriber started")
	return nil
}

// crmEventPayload builds the ids-only payload sent to browser clients.
func crmEventPayload(e crmevents.Event) map[string]any {
	payload := map[string]any{
		"event_id":    e.ID.String(),
		"type":        e.Type,
		"occurred_at": e.OccurredAt,
	}
	if e.ContactID != nil {
		payload["contact_id"] = e.ContactID.String()
	}
	if e.Subject.Type != "" {
		payload["subject_type"] = e.Subject.Type
	}
	if e.Subject.ID != nil {
		payload["subject_id"] = e.Subject.ID.String()
	}
	return payload
}

// NewEventRelay builds the outbox relay with the sinks this deployment needs.
// The webhook sink is wired to the App so deliveries reuse the webhook cache,
// the decrypted signing secret and the shared HTTP client.
func (a *App) NewEventRelay() *crmevents.Relay {
	return &crmevents.Relay{
		DB:  a.DB,
		Log: a.Log,
		Sinks: []crmevents.Sink{
			// Activity first: the timeline row is written inside the relay's
			// transaction, so it commits with the claim rather than depending
			// on a delivery that may fail.
			&crmevents.ActivitySink{DB: a.DB, Recorder: activity.Recorder{}},
			&crmevents.StreamSink{Redis: a.Redis},
			&crmevents.WSFanoutSink{Redis: a.Redis},
			&crmevents.WebhookSink{Dispatcher: a},
		},
	}
}

// Contacts returns the contact lifecycle service (plan 10, S2). Every path that
// resolves a phone number to a contact goes through it, so the restore policy
// is decided in one place rather than re-implemented per caller.
func (a *App) Contacts() *contacts.Service {
	return contacts.New(a.DB)
}

// OrgLocation returns the organization's timezone as a location, falling back
// to UTC (plan 10, S11).
//
// Business hours, SLA windows and date ranges are the organization's, not the
// server's. Evaluating them against the server's local clock meant a
// deployment in a different region routed customers by the wrong hours.
func (a *App) OrgLocation(orgID uuid.UUID) *time.Location {
	// Cached: business hours, task deadlines and every report bucket ask for
	// this, and none of them should cost a query.
	name, _ := a.getOrgSettingsCached(orgID)["timezone"].(string)
	return schedule.Location(name)
}

// Conversations returns the conversation state machine (plan 03).
//
// Every status transition goes through it, so the rules — when a conversation
// opens, reopens, or counts an agent as having responded — live in one place
// rather than being re-derived at each call site.
func (a *App) Conversations() *conversation.Service {
	svc := conversation.New(a.DB)
	svc.Set = a.inboxSettings()
	return svc
}

// inboxSettings reads the organization's inbox rules, falling back to the
// defaults. Settings are per-org, so this is resolved per call rather than
// cached on the App.
func (a *App) inboxSettings() conversation.Settings {
	return conversation.DefaultSettings()
}

// crmActorForUser builds an event actor for an acting user.
func crmActorForUser(userID uuid.UUID) crmevents.Actor {
	return crmevents.UserActor(userID, "")
}
