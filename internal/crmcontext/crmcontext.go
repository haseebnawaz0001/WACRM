// Package crmcontext builds the one variable namespace the product exposes to
// templates (plan 10, S6).
//
// Four places grew their own idea of what `{{...}}` may refer to: canned
// responses resolved `{{contact_name}}` in the browser, custom actions resolved
// `{{contact.name}}` on the server, chatbot flows had session variables, and
// the feature plans used both `field.<key>` and `contact.fields.<key>`. An
// author who learned one syntax found it silently did nothing in the next
// screen along — and "silently" is the problem: an unresolved placeholder in a
// message to a customer looks like a product defect, not a typo.
//
// One namespace, built once per inbound message, call or automation run:
//
//	contact.{id,name,phone_number,tags,lifecycle_stage,source,created_at}
//	contact.fields.<key>
//	contact.owner.{id,name,email}
//	conversation.{id,status,handling,opened_at}
//	conversation.assignee.{id,name}
//	conversation.team.{id,name}
//	user.{id,name,email,role}
//	org.{id,name,timezone}
//	event.*, task.*, deal.*   (when the caller has one)
//	now
package crmcontext

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templating"
	"gorm.io/gorm"
)

// Reserved namespace roots. Chatbot session variables may never be written
// under these, or a flow could overwrite `contact.name` for the rest of the
// conversation and every later message would quietly say the wrong thing.
var reservedRoots = map[string]bool{
	"contact":      true,
	"conversation": true,
	"user":         true,
	"org":          true,
	"event":        true,
	"task":         true,
	"deal":         true,
	"now":          true,
}

// IsReserved reports whether a variable name collides with the namespace.
func IsReserved(name string) bool {
	root := name
	for i, r := range name {
		if r == '.' {
			root = name[:i]
			break
		}
	}
	return reservedRoots[root]
}

// Opts says which optional parts to build. Each one costs a query, and most
// callers need only the contact.
type Opts struct {
	ContactID      uuid.UUID
	UserID         *uuid.UUID
	ConversationID *uuid.UUID

	// IncludeFields loads the contact's typed custom fields. Off by default
	// because it is a second query and most templates never mention them.
	IncludeFields bool

	// Extra is merged in last, for the caller's own roots (event, task, deal).
	// Reserved roots in Extra are ignored rather than allowed to shadow the
	// real record.
	Extra map[string]any

	// MaskPhone hides the middle of phone numbers for viewers who may not see
	// them. The caller decides, because the rule is per organization and per
	// viewer, not per template.
	MaskPhone func(string) string

	// Now fixes the clock, so a rendered template and the record it describes
	// agree. Zero means time.Now().
	Now time.Time
}

// Builder assembles contexts. It needs only a database handle.
type Builder struct {
	DB *gorm.DB
}

// New builds a Builder.
func New(db *gorm.DB) *Builder { return &Builder{DB: db} }

// Build assembles the namespace for one organization and contact.
//
// Missing pieces are absent rather than fatal: a conversation that has been
// resolved, a user who has left, a contact with no owner are all ordinary, and
// a template mentioning them should render an empty value, not fail the send.
func (b *Builder) Build(ctx context.Context, orgID uuid.UUID, opts Opts) (map[string]any, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	out := map[string]any{"now": now.Format(time.RFC3339)}

	var org models.Organization
	if err := b.DB.WithContext(ctx).Where("id = ?", orgID).First(&org).Error; err != nil {
		return nil, err
	}
	timezone, _ := org.Settings["timezone"].(string)
	if timezone == "" {
		timezone = "UTC"
	}
	out["org"] = map[string]any{
		"id":       org.ID.String(),
		"name":     org.Name,
		"timezone": timezone,
	}

	if opts.ContactID != uuid.Nil {
		contact, err := b.contact(ctx, orgID, opts)
		if err != nil {
			return nil, err
		}
		out["contact"] = contact
	}

	if opts.UserID != nil {
		var user models.User
		if err := b.DB.WithContext(ctx).Where("id = ?", *opts.UserID).First(&user).Error; err == nil {
			out["user"] = map[string]any{
				"id":    user.ID.String(),
				"name":  user.FullName,
				"email": user.Email,
				"role":  user.Role,
			}
		}
	}

	if conv := b.conversation(ctx, orgID, opts); conv != nil {
		out["conversation"] = conv
	}

	for key, value := range opts.Extra {
		if reservedRoots[key] {
			// The record wins over whatever the caller passed: a template
			// author reading `contact.name` must get the contact.
			continue
		}
		out[key] = value
	}

	return out, nil
}

func (b *Builder) contact(ctx context.Context, orgID uuid.UUID, opts Opts) (map[string]any, error) {
	var contact models.Contact
	if err := b.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", opts.ContactID, orgID).
		First(&contact).Error; err != nil {
		return nil, err
	}

	phone := contact.PhoneNumber
	if opts.MaskPhone != nil {
		phone = opts.MaskPhone(phone)
	}

	out := map[string]any{
		"id":           contact.ID.String(),
		"name":         contact.ProfileName,
		"profile_name": contact.ProfileName,
		"phone_number": phone,
		"tags":         contact.Tags,
		"created_at":   contact.CreatedAt.Format(time.RFC3339),
		// metadata is the untyped blob the typed fields replaced, kept so
		// templates written against it keep working.
		"metadata": contact.Metadata,
	}

	if opts.IncludeFields {
		values, err := customfields.New(b.DB).ValuesFor(ctx, orgID,
			[]uuid.UUID{contact.ID}, models.FieldEntityContact)
		if err != nil {
			return nil, err
		}
		fields := values[contact.ID]
		if fields == nil {
			fields = map[string]any{}
		}
		out["fields"] = fields
		// The two built-in fields templates reach for constantly are promoted
		// to the top level, because `contact.lifecycle_stage` is what an author
		// writes before they know custom fields exist.
		out["lifecycle_stage"] = fields["lifecycle_stage"]
		out["source"] = fields["source"]
	} else {
		out["fields"] = map[string]any{}
	}

	if contact.AssignedUserID != nil {
		var owner models.User
		if err := b.DB.WithContext(ctx).Where("id = ?", *contact.AssignedUserID).
			First(&owner).Error; err == nil {
			out["owner"] = map[string]any{
				"id":    owner.ID.String(),
				"name":  owner.FullName,
				"email": owner.Email,
			}
		}
	}

	return out, nil
}

func (b *Builder) conversation(ctx context.Context, orgID uuid.UUID, opts Opts) map[string]any {
	var conv models.Conversation
	q := b.DB.WithContext(ctx).Where("organization_id = ?", orgID)

	switch {
	case opts.ConversationID != nil:
		q = q.Where("id = ?", *opts.ConversationID)
	case opts.ContactID != uuid.Nil:
		q = q.Where("contact_id = ? AND status <> ?", opts.ContactID, models.ConversationResolved)
	default:
		return nil
	}

	if err := q.Order("opened_at DESC").First(&conv).Error; err != nil {
		return nil
	}

	out := map[string]any{
		"id":        conv.ID.String(),
		"status":    string(conv.Status),
		"handling":  string(conv.Handling),
		"opened_at": conv.OpenedAt.Format(time.RFC3339),
	}

	if conv.AssigneeID != nil {
		var assignee models.User
		if err := b.DB.WithContext(ctx).Where("id = ?", *conv.AssigneeID).
			First(&assignee).Error; err == nil {
			out["assignee"] = map[string]any{
				"id":   assignee.ID.String(),
				"name": assignee.FullName,
			}
		}
	}
	if conv.TeamID != nil {
		var team models.Team
		if err := b.DB.WithContext(ctx).Where("id = ?", *conv.TeamID).
			First(&team).Error; err == nil {
			out["team"] = map[string]any{
				"id":   team.ID.String(),
				"name": team.Name,
			}
		}
	}

	return out
}

// legacyAliases map the flat token names the product shipped before the
// namespace existed onto their new paths.
//
// Canned responses resolved `{{contact_name}}` in the browser, and thousands of
// them exist in customers' accounts. Dropping the spelling would turn every one
// of those into a literal `{{contact_name}}` in a message to a customer, so the
// old names keep working and the picker offers only the new ones.
var legacyAliases = map[string]string{
	"contact_name": "contact.name",
	"profile_name": "contact.name",
	"phone_number": "contact.phone_number",
	"user_name":    "user.name",
	"agent_name":   "user.name",
	"org_name":     "org.name",
}

// WithLegacyAliases adds the old flat token names as top-level keys.
//
// They are added, never substituted: a context that contains both is one where
// an author can use either spelling, which is the point.
func WithLegacyAliases(data map[string]any) map[string]any {
	for alias, path := range legacyAliases {
		if _, taken := data[alias]; taken {
			continue
		}
		if value, ok := lookup(data, path); ok {
			data[alias] = value
		}
	}
	return data
}

// lookup walks a dotted path through nested maps.
func lookup(data map[string]any, path string) (any, bool) {
	var current any = data
	start := 0
	for i := 0; i <= len(path); i++ {
		if i != len(path) && path[i] != '.' {
			continue
		}
		segment := path[start:i]
		start = i + 1

		asMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = asMap[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// Owns reports whether a placeholder path is one this namespace answers for.
//
// It exists because "not found" is not the same as "not ours" (plan 10, S6).
// A canned response saying "your order {{order_id}} is ready" has a placeholder
// the author fills by hand; the namespace has no `order_id` and never will.
// Rendering it as empty is worse than leaving it alone — the agent's own input
// then has nothing to substitute into, and the customer is told their order
// number is blank.
//
// So: a path whose root is a reserved namespace root, or a legacy alias, is
// ours to resolve. Everything else is left exactly as written.
func Owns(path string) bool {
	if _, alias := legacyAliases[path]; alias {
		return true
	}
	return IsReserved(path)
}

// RenderOwned renders only the placeholders this namespace owns, leaving every
// other one exactly as the author wrote it.
func RenderOwned(template string, data map[string]any, mode templating.EscapeMode) string {
	return templating.ProcessVariablesWith(template, data, func(r templating.Resolved) string {
		path := strings.TrimSpace(r.Raw[2 : len(r.Raw)-2])
		if !Owns(path) || !r.Found {
			return r.Raw
		}
		return templating.Encode(mode, r.Value)
	})
}
