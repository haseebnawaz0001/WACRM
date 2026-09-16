package contactquery

import (
	"sort"
	"strings"
	"sync"
)

// FieldType decides which operators a field accepts and how its values compile.
type FieldType string

const (
	TypeText    FieldType = "text"
	TypeNumber  FieldType = "number"
	TypeDate    FieldType = "date"
	TypeOption  FieldType = "option"
	TypeTags    FieldType = "tags"
	TypeBoolean FieldType = "boolean"
	TypeUser    FieldType = "user"
	// TypeSegment references another saved filter (plan 05). It compiles by
	// inlining the referenced segment rather than comparing a column.
	TypeSegment FieldType = "segment"
)

// Operators.
const (
	OpEquals       = "equals"
	OpNotEquals    = "not_equals"
	OpContains     = "contains"
	OpStartsWith   = "starts_with"
	OpIsEmpty      = "is_empty"
	OpIsNotEmpty   = "is_not_empty"
	OpEq           = "eq"
	OpNeq          = "neq"
	OpGt           = "gt"
	OpGte          = "gte"
	OpLt           = "lt"
	OpLte          = "lte"
	OpBetween      = "between"
	OpOn           = "on"
	OpBefore       = "before"
	OpAfter        = "after"
	OpWithinLast   = "within_last"
	OpMoreThanAgo  = "more_than_ago"
	OpIn           = "in"
	OpNotIn        = "not_in"
	OpContainsAny  = "contains_any"
	OpContainsAll  = "contains_all"
	OpContainsNone = "contains_none"
	OpIsTrue       = "is_true"
	OpIsFalse      = "is_false"
	OpIs           = "is"
	OpIsNot        = "is_not"
	OpIsMe         = "is_me"
	OpIsUnassigned = "is_unassigned"
)

// operatorsByType is the allowed operator set for each field type.
var operatorsByType = map[FieldType][]string{
	TypeText:    {OpEquals, OpNotEquals, OpContains, OpStartsWith, OpIsEmpty, OpIsNotEmpty},
	TypeNumber:  {OpEq, OpNeq, OpGt, OpGte, OpLt, OpLte, OpBetween, OpIsEmpty},
	TypeDate:    {OpOn, OpBefore, OpAfter, OpBetween, OpWithinLast, OpMoreThanAgo, OpIsEmpty},
	TypeOption:  {OpIn, OpNotIn, OpIsEmpty},
	TypeTags:    {OpContainsAny, OpContainsAll, OpContainsNone, OpIsEmpty},
	TypeBoolean: {OpIsTrue, OpIsFalse},
	TypeUser:    {OpIs, OpIsNot, OpIsMe, OpIsUnassigned},
	TypeSegment: {OpIn, OpNotIn},
}

// OperatorsFor returns the operators a field type accepts.
func OperatorsFor(t FieldType) []string {
	ops := operatorsByType[t]
	out := make([]string, len(ops))
	copy(out, ops)
	return out
}

// Field describes one filterable attribute.
type Field struct {
	// Key is what a filter names, e.g. "tags" or "field.lifecycle_stage".
	Key string

	// LabelKey is the i18n key for the builder UI.
	LabelKey string

	Type FieldType

	// Column is the SQL expression this field compiles to. It comes only from
	// here, never from the request, which is what keeps the compiler free of
	// injection risk.
	Column string

	// Build overrides the default compilation for fields that are not a plain
	// column — a custom field value, or a condition on a related table.
	Build BuildFunc

	// Options lists the allowed values for an option field, for the UI.
	Options []Option

	// Sortable marks fields the list may be ordered by.
	Sortable bool
}

// Option is one choice of an option field.
type Option struct {
	Value    string `json:"value"`
	LabelKey string `json:"label_key,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Supports reports whether the field accepts an operator.
func (f Field) Supports(operator string) bool {
	for _, op := range operatorsByType[f.Type] {
		if op == operator {
			return true
		}
	}
	return false
}

// Registry holds the filterable fields.
//
// Each plan registers the fields it owns — 01 adds custom fields, 03 adds
// conversation state, 05 adds segments — so the builder UI, the compiler and
// the API catalog all stay in step without a hand-maintained list per feature.
type Registry struct {
	mu     sync.RWMutex
	fields map[string]Field
}

// NewRegistry builds a registry with the core contact fields (F6).
func NewRegistry() *Registry {
	r := &Registry{fields: make(map[string]Field)}

	r.Register(Field{
		Key: "phone_number", LabelKey: "contacts.phoneNumber", Type: TypeText,
		Column: "contacts.phone_number",
	})
	r.Register(Field{
		Key: "profile_name", LabelKey: "contacts.name", Type: TypeText,
		Column: "contacts.profile_name", Sortable: true,
	})
	r.Register(Field{
		Key: "whatsapp_account", LabelKey: "contacts.account", Type: TypeOption,
		Column: "contacts.whatsapp_account",
	})
	r.Register(Field{
		Key: "tags", LabelKey: "contacts.tags", Type: TypeTags,
		Column: "contacts.tags",
	})
	r.Register(Field{
		Key: "assigned_user_id", LabelKey: "contacts.owner", Type: TypeUser,
		Column: "contacts.assigned_user_id",
	})
	r.Register(Field{
		Key: "created_at", LabelKey: "common.createdAt", Type: TypeDate,
		Column: "contacts.created_at", Sortable: true,
	})
	r.Register(Field{
		Key: "last_message_at", LabelKey: "contacts.lastMessageAt", Type: TypeDate,
		Column: "contacts.last_message_at", Sortable: true,
	})
	r.Register(Field{
		Key: "last_inbound_at", LabelKey: "contacts.lastInboundAt", Type: TypeDate,
		Column: "contacts.last_inbound_at", Sortable: true,
	})
	r.Register(Field{
		Key: "marketing_opt_out", LabelKey: "contacts.optedOut", Type: TypeBoolean,
		Column: "contacts.marketing_opt_out",
	})
	r.Register(Field{
		Key: "source", LabelKey: "contacts.source", Type: TypeOption,
		Column: "contacts.source",
	})

	return r
}

// Register adds or replaces a field.
func (r *Registry) Register(f Field) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if f.Key == "" {
		panic("contactquery: field needs a key")
	}
	if f.Build == nil && f.Column == "" {
		panic("contactquery: field " + f.Key + " needs a column or a builder")
	}
	if _, ok := operatorsByType[f.Type]; !ok {
		panic("contactquery: field " + f.Key + " has unknown type " + string(f.Type))
	}
	r.fields[f.Key] = f
}

// Lookup returns a field by key.
func (r *Registry) Lookup(key string) (Field, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.fields[strings.TrimSpace(key)]
	return f, ok
}

// Fields returns every registered field, sorted by key, for the builder UI.
func (r *Registry) Fields() []Field {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Field, 0, len(r.fields))
	for _, f := range r.fields {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SortableFields returns the keys the list may be ordered by. Sorting is
// whitelisted for the same reason filtering is: the column reaches SQL.
func (r *Registry) SortableFields() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.fields))
	for key, f := range r.fields {
		if f.Sortable {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// SortColumn returns the SQL column for a sort key, and whether it is allowed.
func (r *Registry) SortColumn(key string) (string, bool) {
	f, ok := r.Lookup(key)
	if !ok || !f.Sortable || f.Column == "" {
		return "", false
	}
	return f.Column, true
}
