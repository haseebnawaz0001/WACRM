// Package crmevents carries domain events from the transaction that caused
// them to everything that reacts to them: the activity timeline, realtime
// clients, outbound webhooks and (later) the automation engine.
//
// The transport is a transactional outbox (plan 10, S3) rather than in-process
// callbacks. Side effects used to be fired from goroutines at each mutation
// site, so they were lost whenever the process died between the commit and the
// goroutine running, and producers without an *App — the campaign worker, the
// calling manager — could not emit them at all. Writing a row in the caller's
// own transaction fixes both: the event commits atomically with the change, and
// a producer needs nothing but a *gorm.DB.
package crmevents

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Actor types.
const (
	ActorUser       = "user"
	ActorSystem     = "system"
	ActorAutomation = "automation"
	ActorContact    = "contact"
	ActorAPI        = "api"
	// ActorBot is the chatbot acting on its own, as distinct from an agent or
	// a rule (plan 02). Without it a flow's edits to a record were logged as
	// "system", which reads as "the product did this" when the truthful answer
	// is "the chatbot did, while talking to them".
	ActorBot = "bot"
)

// Subject types.
const (
	SubjectContact      = "contact"
	SubjectConversation = "conversation"
	SubjectMessage      = "message"
	SubjectTransfer     = "transfer"
	SubjectTask         = "task"
	SubjectDeal         = "deal"
	SubjectTag          = "tag"
	SubjectField        = "field"
)

// Actor is who caused an event.
type Actor struct {
	Type string
	ID   *uuid.UUID
	Name string
}

// SystemActor is the actor for events with no human behind them.
func SystemActor() Actor { return Actor{Type: ActorSystem} }

// UserActor builds an actor for an acting user.
func UserActor(id uuid.UUID, name string) Actor {
	return Actor{Type: ActorUser, ID: &id, Name: name}
}

// ContactActor builds an actor for something the customer did.
func ContactActor(id uuid.UUID, name string) Actor {
	return Actor{Type: ActorContact, ID: &id, Name: name}
}

// BotActor is the chatbot acting while talking to somebody.
func BotActor() Actor { return Actor{Type: ActorBot} }

// Subject identifies the record an event is about.
type Subject struct {
	Type string
	ID   *uuid.UUID
}

// Origin carries automation loop protection: which rule produced the event and
// how long the causal chain already is.
type Origin struct {
	RuleID *uuid.UUID
	Depth  int
}

// Event is a domain event ready to be written to the outbox.
type Event struct {
	ID         uuid.UUID
	Type       string
	OrgID      uuid.UUID
	ContactID  *uuid.UUID
	Subject    Subject
	Actor      Actor
	Data       map[string]any
	OccurredAt time.Time
	Origin     Origin
}

// New builds an event with its id and timestamp filled in.
func New(orgID uuid.UUID, eventType string, actor Actor, data map[string]any) Event {
	return Event{
		ID:         uuid.New(),
		Type:       eventType,
		OrgID:      orgID,
		Actor:      actor,
		Data:       data,
		OccurredAt: time.Now().UTC(),
	}
}

// ForContact attaches the contact an event belongs to, and makes the contact
// the subject when nothing more specific has been set.
func (e Event) ForContact(contactID uuid.UUID) Event {
	e.ContactID = &contactID
	if e.Subject.Type == "" {
		e.Subject = Subject{Type: SubjectContact, ID: &contactID}
	}
	return e
}

// About sets the record the event is about.
func (e Event) About(subjectType string, subjectID uuid.UUID) Event {
	e.Subject = Subject{Type: subjectType, ID: &subjectID}
	return e
}

// FromAutomation marks the event as produced by an automation rule, carrying
// the causal depth so rules cannot trigger each other indefinitely.
func (e Event) FromAutomation(ruleID uuid.UUID, depth int) Event {
	e.Origin = Origin{RuleID: &ruleID, Depth: depth}
	return e
}

// toRow converts an event into the outbox row written inside the caller's
// transaction.
func (e Event) toRow() *models.CRMEventOutbox {
	data := models.JSONB(e.Data)
	if data == nil {
		data = models.JSONB{}
	}
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	id := e.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	return &models.CRMEventOutbox{
		ID:             id,
		OrganizationID: e.OrgID,
		Type:           e.Type,
		ContactID:      e.ContactID,
		SubjectType:    e.Subject.Type,
		SubjectID:      e.Subject.ID,
		ActorType:      actorTypeOrSystem(e.Actor.Type),
		ActorID:        e.Actor.ID,
		ActorName:      e.Actor.Name,
		OriginRuleID:   e.Origin.RuleID,
		OriginDepth:    e.Origin.Depth,
		Data:           data,
		OccurredAt:     occurred,
	}
}

// eventFromRow rebuilds an event from its outbox row, for the relay.
func eventFromRow(row models.CRMEventOutbox) Event {
	return Event{
		ID:         row.ID,
		Type:       row.Type,
		OrgID:      row.OrganizationID,
		ContactID:  row.ContactID,
		Subject:    Subject{Type: row.SubjectType, ID: row.SubjectID},
		Actor:      Actor{Type: row.ActorType, ID: row.ActorID, Name: row.ActorName},
		Data:       map[string]any(row.Data),
		OccurredAt: row.OccurredAt,
		Origin:     Origin{RuleID: row.OriginRuleID, Depth: row.OriginDepth},
	}
}

func actorTypeOrSystem(t string) string {
	if t == "" {
		return ActorSystem
	}
	return t
}
