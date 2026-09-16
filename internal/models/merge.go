package models

import (
	"time"

	"github.com/google/uuid"
)

// Identity types (plan 06).
const (
	IdentityPhone = "phone"
	IdentityBSUID = "bsuid"
	IdentityEmail = "email"
)

// Where an identity came from.
const (
	IdentitySourceMerge  = "merge"
	IdentitySourceManual = "manual"
	IdentitySourceImport = "import"
)

// Duplicate candidate statuses.
const (
	DuplicatePending   = "pending"
	DuplicateDismissed = "dismissed"
	DuplicateMerged    = "merged"
)

// ContactIdentity is an alternate identifier that resolves to a contact
// (plan 06).
//
// After a merge the losing contact's phone number still has to reach the
// survivor: the customer does not know they were merged and will keep using
// whichever number they always used.
type ContactIdentity struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`
	ContactID      uuid.UUID `gorm:"type:uuid;not null;index" json:"contact_id"`

	Type  string `gorm:"size:16;not null" json:"type"`
	Value string `gorm:"size:255;not null" json:"value"`
	// Normalized is what lookups compare against, so "+92 321…" and "923 21…"
	// both find the same contact.
	Normalized string `gorm:"size:255;not null" json:"normalized"`
	Source     string `gorm:"size:16;not null" json:"source"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (ContactIdentity) TableName() string {
	return "contact_identities"
}

// ContactDuplicateCandidate is a suggested pair of contacts that may be the
// same person.
//
// The pair is stored with the lower uuid first, so (A,B) and (B,A) are the same
// row and the unique index actually prevents duplicates of duplicates.
type ContactDuplicateCandidate struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	ContactAID uuid.UUID `gorm:"type:uuid;not null" json:"contact_a_id"`
	ContactBID uuid.UUID `gorm:"type:uuid;not null" json:"contact_b_id"`

	// Reasons names the signals that matched, so a person deciding can see why
	// rather than being asked to trust a number.
	Reasons JSONBArray `gorm:"type:jsonb;not null;default:'[]'" json:"reasons"`
	Score   int        `gorm:"not null" json:"score"`

	Status       string     `gorm:"size:12;not null;default:'pending'" json:"status"`
	ResolvedByID *uuid.UUID `gorm:"type:uuid" json:"resolved_by_id,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (ContactDuplicateCandidate) TableName() string {
	return "contact_duplicate_candidates"
}

// ContactMerge records one merge, with enough detail to understand it later.
//
// Merging is destructive in the sense that two records become one. The snapshot
// is what makes it reviewable: without it, "why does this contact have that
// company name?" has no answer.
type ContactMerge struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	OrganizationID uuid.UUID `gorm:"type:uuid;not null;index" json:"organization_id"`

	PrimaryContactID   uuid.UUID `gorm:"type:uuid;not null;index" json:"primary_contact_id"`
	SecondaryContactID uuid.UUID `gorm:"type:uuid;not null" json:"secondary_contact_id"`
	MergedByID         uuid.UUID `gorm:"type:uuid;not null" json:"merged_by_id"`

	// Snapshot holds the secondary contact as it was, plus how many rows moved
	// from each table.
	Snapshot JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"snapshot"`
	// FieldResolution records which value won for each conflicting field.
	FieldResolution JSONB `gorm:"type:jsonb;not null;default:'{}'" json:"field_resolution"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

func (ContactMerge) TableName() string {
	return "contact_merges"
}
