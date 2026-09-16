// Package crmactions is the one place an action on a CRM record is defined
// (plan 10, S7).
//
// "Add a tag", "create a follow-up", "set a field" are wanted by the automation
// engine (plan 08), by chatbot flow nodes, by keyword rules and by bulk
// actions. Implementing each one four times is how four subtly different
// behaviours end up in the same product — one path emits events, another does
// not; one respects opt-out, another does not. Every caller runs the same
// executor here, so an action behaves identically wherever it is triggered.
package crmactions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Action types. These strings are stored in rule configuration, so they are
// part of the data format and must not be renamed.
const (
	TypeAddTags               = "add_tags"
	TypeRemoveTags            = "remove_tags"
	TypeSetField              = "set_field"
	TypeSetContactOwner       = "set_contact_owner"
	TypeSetConversationStatus = "set_conversation_status"
	TypeAssignConversation    = "assign_conversation"
	TypeCreateTask            = "create_task"
	TypeAddNote               = "add_note"
	TypeNotifyUsers           = "notify_users"
	TypeCallWebhook           = "call_webhook"
	TypeCreateDeal            = "create_deal"
	TypeMoveDealStage         = "move_deal_stage"
	TypeSendMessage           = "send_message"
	TypeSendTemplate          = "send_template"
)

// Config is one action's settings as stored in JSON.
type Config map[string]any

// Permanent marks a failure that retrying cannot fix: a contact who has opted
// out, a message outside the service window, a template that no longer exists.
// Retrying these only delays the moment somebody notices.
type Permanent struct{ Err error }

func (e Permanent) Error() string { return e.Err.Error() }
func (e Permanent) Unwrap() error { return e.Err }

// Retryable marks a failure that may succeed later: a timeout, a 5xx from
// somebody else's server.
type Retryable struct{ Err error }

func (e Retryable) Error() string { return e.Err.Error() }
func (e Retryable) Unwrap() error { return e.Err }

// IsPermanent reports whether retrying is pointless.
func IsPermanent(err error) bool {
	var p Permanent
	return errors.As(err, &p)
}

// IsRetryable reports whether the caller should try again later.
func IsRetryable(err error) bool {
	var r Retryable
	return errors.As(err, &r)
}

// ErrUnavailable is returned when an action's dependency was not wired into
// this process. It is permanent for the run but it is a deployment problem,
// not a user error, so it reads differently from a bad configuration.
var ErrUnavailable = errors.New("crmactions: this action is not available in this process")

// Messenger sends WhatsApp messages on behalf of an action.
//
// It is an interface rather than a dependency because sending lives in the
// handlers package, which already imports half the application; a direct import
// would be a cycle. Callers that cannot send (the worker, a test) leave it nil
// and the sending actions fail with ErrUnavailable rather than silently doing
// nothing.
type Messenger interface {
	// SendText sends a free-form message, which WhatsApp only allows inside
	// the 24-hour service window.
	SendText(ctx context.Context, orgID, contactID uuid.UUID, text string) (messageID uuid.UUID, err error)

	// SendTemplate sends an approved template, which works outside the window
	// but must respect the contact's marketing opt-out.
	SendTemplate(ctx context.Context, orgID, contactID, templateID uuid.UUID, params map[string]any) (messageID uuid.UUID, err error)
}

// Assigner routes a conversation to a team, which involves the transfer queue
// and business hours rather than a column update.
type Assigner interface {
	AssignToTeam(ctx context.Context, orgID, contactID, teamID uuid.UUID) (outcome string, err error)
}

// Deps are the services actions execute against.
//
// Everything optional is nil-checked at execution: a process that wires fewer
// services runs fewer actions rather than panicking on the first one it cannot
// perform.
type Deps struct {
	DB *gorm.DB

	// HTTPClient must be the SSRF-safe client. A webhook action takes a URL
	// from a rule someone wrote, so without it an action could be pointed at
	// the cloud metadata endpoint.
	HTTPClient *http.Client

	Messenger Messenger
	Assigner  Assigner
}

// RunContext is everything one execution knows about why it is running.
type RunContext struct {
	OrgID     uuid.UUID
	ContactID uuid.UUID

	// Event is the event that triggered this run, for templating. It is the
	// zero value when an action is invoked directly (a bulk action, a flow).
	Event crmevents.Event

	// Actor is recorded on everything the action writes, so a contact's
	// history says "Automation: Overdue follow-up" rather than "system".
	Actor crmevents.Actor

	// Origin carries loop protection into the events this action emits.
	Origin crmevents.Origin

	// CreatorID is the person who wrote the rule. An automation is not a user,
	// so anything that needs a human — a task's last-resort owner, a note's
	// author — falls back to whoever set it up.
	CreatorID *uuid.UUID

	// Vars are the template variables available to text fields.
	Vars map[string]any

	// DryRun renders what the action would do and writes nothing. Everything
	// that mutates must check it; an action that ignores it makes the rule
	// tester dangerous.
	DryRun bool

	// Location is the organization's timezone, for deadlines and dates.
	Location *time.Location
}

// Action is one thing that can be done to a CRM record.
type Action interface {
	// Type is the stored identifier.
	Type() string

	// Validate rejects a configuration at save time rather than at 3am when
	// the rule finally fires.
	Validate(cfg Config) error

	// Execute performs the action and returns what it did, for the run log.
	Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error)
}

// registry holds every known action, keyed by type.
var registry = map[string]Action{}

func register(a Action) {
	if _, exists := registry[a.Type()]; exists {
		panic("crmactions: duplicate action type " + a.Type())
	}
	registry[a.Type()] = a
}

// Lookup returns the action for a type.
func Lookup(actionType string) (Action, bool) {
	a, ok := registry[actionType]
	return a, ok
}

// Types lists every registered action type, sorted.
func Types() []string {
	out := make([]string, 0, len(registry))
	for key := range registry {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// Validate checks one action configuration.
func Validate(actionType string, cfg Config) error {
	action, ok := Lookup(actionType)
	if !ok {
		return fmt.Errorf("crmactions: unknown action %q", actionType)
	}
	return action.Validate(cfg)
}

// Execute runs one action.
func Execute(ctx context.Context, d Deps, rc RunContext, actionType string, cfg Config) (map[string]any, error) {
	action, ok := Lookup(actionType)
	if !ok {
		return nil, Permanent{fmt.Errorf("crmactions: unknown action %q", actionType)}
	}
	return action.Execute(ctx, d, rc, cfg)
}

// loadContact reads the contact an action is about.
func loadContact(ctx context.Context, d Deps, rc RunContext) (*models.Contact, error) {
	var contact models.Contact
	err := d.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", rc.ContactID, rc.OrgID).
		First(&contact).Error
	if err != nil {
		return nil, Permanent{fmt.Errorf("crmactions: contact not found")}
	}
	return &contact, nil
}
