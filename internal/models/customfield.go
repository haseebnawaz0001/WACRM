package models

import (
	"time"

	"github.com/google/uuid"
)

// Custom field types (plan 01).
const (
	FieldTypeText     = "text"
	FieldTypeNumber   = "number"
	FieldTypeDate     = "date"
	FieldTypeDropdown = "dropdown"
	FieldTypeEmail    = "email"
	FieldTypePhone    = "phone"
)

// Entity types a custom field can describe. Deals (plan 07) reuse the same
// tables, which is why the entity is a column rather than being implied.
const (
	FieldEntityContact = "contact"
	FieldEntityDeal    = "deal"
)

// Built-in field keys every organization is seeded with.
const (
	FieldKeyEmail          = "email"
	FieldKeyCompany        = "company"
	FieldKeyAddress        = "address"
	FieldKeySource         = "source"
	FieldKeyLifecycleStage = "lifecycle_stage"
)

// Lifecycle stages, the options of the built-in lifecycle_stage field.
//
// They are constants rather than literals repeated at each use because the
// field seed, the default applied to a new contact and the funnel report all
// have to agree on the spelling; a typo in any one of them produces a stage
// that exists but which nothing else can ever match.
const (
	LifecycleNew       = "new"
	LifecycleLead      = "lead"
	LifecycleQualified = "qualified"
	LifecycleCustomer  = "customer"
	LifecycleChurned   = "churned"
)

// CustomFieldDefinition describes one org-defined field.
//
// Contacts previously carried a free-form metadata JSONB: no types, no
// validation, no editing UI and nothing to filter on. A definition gives a
// field a type so its values can be validated once, sorted meaningfully and
// queried without guessing what is inside the blob.
type CustomFieldDefinition struct {
	BaseModel
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	EntityType     string    `gorm:"size:20;not null;default:'contact'" json:"entity_type"`

	// Key is the slug used in filters, templates and the API. It is immutable
	// after creation: renaming it would silently break every segment,
	// automation and template that refers to it.
	Key   string `gorm:"size:64;not null" json:"key"`
	Label string `gorm:"size:100;not null" json:"label"`

	Description string `gorm:"size:500;not null;default:''" json:"description"`
	Type        string `gorm:"size:20;not null" json:"type"`

	// Options lists the choices of a dropdown:
	// [{"value":"lead","label":"Lead","color":"blue"}].
	Options JSONBArray `gorm:"type:jsonb;not null;default:'[]'" json:"options"`

	// Validation holds per-type rules, e.g. {"max_length":255,"min":0}.
	Validation JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"validation"`

	DefaultValue JSONB `gorm:"type:jsonb" json:"default_value,omitempty"`

	// IsSystem marks the fields seeded for every org. They can be relabelled
	// and reordered but not deleted, because product features reference them.
	IsSystem bool `gorm:"not null;default:false" json:"is_system"`

	// IsRequired is enforced on manual create and edit only. An inbound
	// message must never be rejected because an org made a field mandatory.
	IsRequired bool `gorm:"not null;default:false" json:"is_required"`

	ShowInList      bool   `gorm:"not null;default:false" json:"show_in_list"`
	ShowInChatPanel bool   `gorm:"not null;default:true" json:"show_in_chat_panel"`
	GroupLabel      string `gorm:"size:64;not null;default:''" json:"group_label"`
	Position        int    `gorm:"not null;default:0" json:"position"`

	// ArchivedAt hides a field from editing while keeping its values readable,
	// so historical records do not lose data when a field falls out of use.
	ArchivedAt *time.Time `json:"archived_at,omitempty"`

	CreatedByID *uuid.UUID `gorm:"type:uuid" json:"created_by_id,omitempty"`
	UpdatedByID *uuid.UUID `gorm:"type:uuid" json:"updated_by_id,omitempty"`
}

func (CustomFieldDefinition) TableName() string {
	return "custom_field_definitions"
}

// IsArchived reports whether the field is archived.
func (d CustomFieldDefinition) IsArchived() bool {
	return d.ArchivedAt != nil
}

// CustomFieldValue is one field's value for one entity.
//
// Values are stored in typed columns rather than a single text column so the
// database can sort, range-scan and index them. A date field compared as text
// sorts "2026-01-09" after "2026-01-10", and a number field cannot answer
// "greater than" at all.
type CustomFieldValue struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	EntityType     string    `gorm:"size:20;not null;default:'contact'" json:"entity_type"`
	EntityID       uuid.UUID `gorm:"type:uuid;not null" json:"entity_id"`
	FieldID        uuid.UUID `gorm:"type:uuid;not null" json:"field_id"`

	// Exactly one of these carries the value, chosen by the field's type.
	ValueText *string `gorm:"type:text" json:"value_text,omitempty"`
	// The column stays numeric(20,6) so stored values are exact; float64 is
	// only the Go representation, which is ample for the quantities and
	// scores a CRM field holds.
	ValueNumber *float64   `gorm:"type:numeric(20,6)" json:"value_number,omitempty"`
	ValueDate   *time.Time `gorm:"type:date" json:"value_date,omitempty"`
	ValueOption *string    `gorm:"size:100" json:"value_option,omitempty"`

	UpdatedByID *uuid.UUID `gorm:"type:uuid" json:"updated_by_id,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`

	Field *CustomFieldDefinition `gorm:"foreignKey:FieldID" json:"field,omitempty"`
}

func (CustomFieldValue) TableName() string {
	return "custom_field_values"
}

// IsEmpty reports whether the value holds nothing.
func (v CustomFieldValue) IsEmpty() bool {
	return v.ValueText == nil && v.ValueNumber == nil && v.ValueDate == nil && v.ValueOption == nil
}

// HasOption reports whether a dropdown offers this value.
//
// Writing a value a dropdown does not offer produces a record that no filter
// on that field can ever match and that the editor shows as blank, so the
// callers that set a field from code — the contact lifecycle's source, an
// automation action — check first rather than storing something unreachable.
func (d CustomFieldDefinition) HasOption(value string) bool {
	for _, raw := range d.Options {
		option, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stored, ok := option["value"].(string); ok && stored == value {
			return true
		}
	}
	return false
}
