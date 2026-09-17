// Package contacts owns the lifecycle of a contact record: how one is found,
// created, deleted and restored (plan 10, S2).
//
// Before this package, every code path that touched a phone number called
// contactutil.GetOrCreateContact, which restored soft-deleted contacts
// unconditionally. That made deletion meaningless: a campaign send, a reaction,
// a message echo or an inbound call would silently resurrect a contact an admin
// had deleted, and — once merging exists — resurrect one that had been merged
// away. Restoring is a decision that depends on who is asking and why, so it
// belongs to one service that every caller goes through.
package contacts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/phoneutil"
	"gorm.io/gorm"
)

// Outcome describes what Resolve did.
type Outcome string

const (
	// OutcomeFound matched a live contact.
	OutcomeFound Outcome = "found"
	// OutcomeCreated inserted a new contact.
	OutcomeCreated Outcome = "created"
	// OutcomeRestored undid a soft delete.
	OutcomeRestored Outcome = "restored"
	// OutcomeFollowedMerge resolved to the contact this number was merged into.
	OutcomeFollowedMerge Outcome = "followed_merge"
)

// Source values record how a contact first reached the product.
const (
	SourceInbound         = "inbound"
	SourceImport          = "import"
	SourceCampaign        = "campaign"
	SourceAPI             = "api"
	SourceManual          = "manual"
	SourceCall            = "call"
	SourceAddressBookSync = "address_book_sync"
)

// Delete reasons. Only an address-book-sync deletion may be undone
// automatically; a person who deleted a contact meant it.
const (
	ReasonUser            = "user"
	ReasonAddressBookSync = "address_book_sync"
	ReasonMerged          = "merged"
)

// ErrNotFound is returned when no contact matches and creation was not asked for.
var ErrNotFound = errors.New("contacts: no matching contact")

// Identity is how a contact is addressed by an inbound event.
type Identity struct {
	Phone string
	BSUID string
}

// ResolveOpts controls what Resolve is permitted to do.
type ResolveOpts struct {
	// CreateIfMissing inserts a contact when none matches.
	CreateIfMissing bool

	// AllowRestore permits undoing a soft delete. It is honoured only for
	// deletions the product performed itself (address-book sync); a contact
	// a person deleted is never silently restored, and a merged contact is
	// never restored at all.
	AllowRestore bool

	// Source records how the contact arrived, for contacts created here.
	Source string

	// ProfileName is the display name the channel reported.
	ProfileName string

	// UpdateName allows an existing contact's profile name to be overwritten.
	// Campaign CSVs pass false: a recipient name from a spreadsheet must not
	// replace the name WhatsApp reported for that person.
	UpdateName bool

	// Actor is recorded on the events this resolution emits.
	Actor crmevents.Actor

	// DefaultCountryCode expands local "0"-prefixed numbers during lookup.
	DefaultCountryCode string
}

// ConversationCloser resolves the contact's open conversation.
//
// It is an interface, and optional, so the lifecycle service keeps needing
// nothing but a database handle: the campaign worker and the calling manager
// construct one without a conversation service, and the HTTP layer supplies
// the real one. Closing a conversation stays the conversation service's job
// either way — this package does not learn a second way to do it.
type ConversationCloser interface {
	Resolve(ctx context.Context, orgID, contactID uuid.UUID, reason string, actor crmevents.Actor) (*models.Conversation, error)
}

// Service resolves contacts. It needs nothing but a database handle, so the
// campaign worker and the calling manager can use it as well as HTTP handlers.
type Service struct {
	DB *gorm.DB

	// Conversations closes the conversation of a deleted contact. Nil is
	// valid: the deletion still happens, it just leaves the conversation to
	// whoever resolves it next.
	Conversations ConversationCloser

	// Log records cascade problems. Nil discards them, which keeps the
	// zero value usable in tests and short-lived callers.
	Log func(format string, args ...any)
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// WithConversations returns a copy that closes conversations on delete.
func (s *Service) WithConversations(c ConversationCloser) *Service {
	out := *s
	out.Conversations = c
	return &out
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Resolve finds, creates or restores the contact for an identity.
//
// The lookup chain is: exact phone → normalised phone → BSUID → follow a merge
// pointer. Each step exists because the same person legitimately arrives under
// different identifiers, and missing one of them creates a duplicate.
func (s *Service) Resolve(ctx context.Context, orgID uuid.UUID, id Identity, opts ResolveOpts) (*models.Contact, Outcome, error) {
	db := s.DB.WithContext(ctx)

	phone := strings.TrimSpace(id.Phone)
	if phone == "" && id.BSUID == "" {
		return nil, "", fmt.Errorf("contacts: identity has neither phone nor BSUID")
	}

	contact, err := s.lookup(db, orgID, id, opts.DefaultCountryCode)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}

	if contact != nil {
		return s.resolveExisting(db, orgID, contact, id, opts)
	}

	if !opts.CreateIfMissing {
		return nil, "", ErrNotFound
	}
	return s.create(db, orgID, id, opts)
}

// lookup runs the identity chain, including soft-deleted rows so the caller can
// decide whether restoring is allowed.
func (s *Service) lookup(db *gorm.DB, orgID uuid.UUID, id Identity, countryCode string) (*models.Contact, error) {
	if id.Phone != "" {
		// Exact stored forms first: existing rows predate normalisation and
		// may carry a "+" or other formatting.
		if c, err := s.first(db, orgID, "phone_number IN ?", phoneutil.Variants(id.Phone)); err == nil {
			return c, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		if normalized := phoneutil.NormalizeWithCountry(id.Phone, countryCode); normalized != "" {
			if c, err := s.first(db, orgID, "phone_normalized = ?", normalized); err == nil {
				return c, nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		}
	}

	if id.BSUID != "" {
		if c, err := s.first(db, orgID, "bsuid = ?", id.BSUID); err == nil {
			return c, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	return nil, gorm.ErrRecordNotFound
}

func (s *Service) first(db *gorm.DB, orgID uuid.UUID, cond string, arg any) (*models.Contact, error) {
	var c models.Contact
	err := db.Unscoped().
		Where("organization_id = ?", orgID).
		Where(cond, arg).
		Order("deleted_at IS NOT NULL, created_at").
		First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// resolveExisting applies the restore policy and name rules to a match.
func (s *Service) resolveExisting(db *gorm.DB, orgID uuid.UUID, contact *models.Contact, id Identity, opts ResolveOpts) (*models.Contact, Outcome, error) {
	// A merged contact is never the answer; the survivor is.
	if contact.MergedIntoID != nil {
		var survivor models.Contact
		if err := db.Where("organization_id = ? AND id = ?", orgID, *contact.MergedIntoID).
			First(&survivor).Error; err == nil {
			s.touchName(db, &survivor, opts)
			return &survivor, OutcomeFollowedMerge, nil
		}
		// The survivor is gone; fall through and treat this row normally.
	}

	if !contact.DeletedAt.Valid {
		s.touchName(db, contact, opts)
		return contact, OutcomeFound, nil
	}

	// Soft-deleted. Restoring is allowed only when the caller asked for it
	// AND the deletion was one the product performed itself.
	if !opts.AllowRestore || contact.DeletedReason == ReasonUser || contact.DeletedReason == ReasonMerged {
		if !opts.CreateIfMissing {
			return nil, "", ErrNotFound
		}
		// A person deleted this contact and the number wrote in again.
		// Creating a fresh record keeps the deletion meaningful; plan 06
		// flags the pair as a duplicate candidate for review.
		return s.create(db, orgID, id, opts)
	}

	updates := map[string]any{"deleted_at": nil, "deleted_reason": ""}
	if err := db.Unscoped().Model(&models.Contact{}).
		Where("id = ?", contact.ID).Updates(updates).Error; err != nil {
		return nil, "", err
	}
	contact.DeletedAt.Valid = false
	contact.DeletedReason = ""

	s.touchName(db, contact, opts)
	s.publish(db, orgID, contact, "contact.restored", opts.Actor)
	return contact, OutcomeRestored, nil
}

// touchName updates the stored profile name when the caller is allowed to.
func (s *Service) touchName(db *gorm.DB, contact *models.Contact, opts ResolveOpts) {
	if !opts.UpdateName || opts.ProfileName == "" || contact.ProfileName == opts.ProfileName {
		return
	}
	if err := db.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("profile_name", opts.ProfileName).Error; err == nil {
		contact.ProfileName = opts.ProfileName
	}
}

func (s *Service) create(db *gorm.DB, orgID uuid.UUID, id Identity, opts ResolveOpts) (*models.Contact, Outcome, error) {
	phone := phoneutil.Normalize(id.Phone)
	if phone == "" {
		phone = strings.TrimSpace(id.Phone)
	}

	contact := &models.Contact{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		PhoneNumber:     phone,
		PhoneNormalized: phoneutil.NormalizeWithCountry(id.Phone, opts.DefaultCountryCode),
		BSUID:           id.BSUID,
		ProfileName:     opts.ProfileName,
		Source:          opts.Source,
	}

	if err := db.Create(contact).Error; err != nil {
		// Another worker may have created it between our lookup and insert.
		if existing, lookupErr := s.lookup(db, orgID, id, opts.DefaultCountryCode); lookupErr == nil && existing != nil {
			if !existing.DeletedAt.Valid {
				return existing, OutcomeFound, nil
			}
		}
		return nil, "", err
	}

	s.seedLifecycleStage(db, orgID, contact)

	s.publish(db, orgID, contact, "contact.created", opts.Actor)
	return contact, OutcomeCreated, nil
}

// seedLifecycleStage gives a brand-new contact the "new" lifecycle stage
// (plan 01).
//
// Without it the field is simply absent until somebody edits the contact by
// hand, so the lifecycle funnel starts at whatever stage people remembered to
// set and a segment on "new" never matches the contacts that actually are.
//
// A failure is logged by the caller's error path, not returned: the contact
// exists and the message that created it must not be lost over a default.
func (s *Service) seedLifecycleStage(db *gorm.DB, orgID uuid.UUID, contact *models.Contact) {
	var def models.CustomFieldDefinition
	if err := db.Where("organization_id = ? AND entity_type = ? AND key = ?",
		orgID, models.FieldEntityContact, models.FieldKeyLifecycleStage).
		First(&def).Error; err != nil {
		// Organizations seeded before plan 01 may not have the field yet.
		return
	}

	value := models.CustomFieldValue{
		OrganizationID: orgID,
		EntityType:     models.FieldEntityContact,
		EntityID:       contact.ID,
		FieldID:        def.ID,
		ValueOption:    ptrString(models.LifecycleNew),
	}
	// Ignore a conflict: a concurrent writer setting it first is fine.
	_ = db.Where("organization_id = ? AND entity_type = ? AND entity_id = ? AND field_id = ?",
		orgID, models.FieldEntityContact, contact.ID, def.ID).
		FirstOrCreate(&value).Error
}

func ptrString(s string) *string { return &s }

// Delete soft-deletes a contact, recording why so a later inbound message knows
// whether restoring is allowed.
//
// Deleting a contact also ends the work in flight for them (plan 10, S2). A
// deleted contact that still owned an open conversation, a queued transfer and
// a half-finished chatbot session left the conversation in an agent's inbox
// with no contact behind it, kept a place in the transfer queue that could
// never be picked up, and armed a prompt node that would swallow the first
// message from whoever got that number next. Tasks and deals are deliberately
// kept: they are a person's work, and deleting the customer record should not
// silently destroy someone's to-do list — they are hidden from lists until the
// contact comes back.
func (s *Service) Delete(ctx context.Context, orgID, contactID uuid.UUID, reason string, actor crmevents.Actor) error {
	db := s.DB.WithContext(ctx)

	var contact models.Contact
	if err := db.Where("organization_id = ? AND id = ?", orgID, contactID).First(&contact).Error; err != nil {
		return err
	}

	if err := db.Model(&models.Contact{}).Where("id = ?", contactID).
		Updates(map[string]any{
			"deleted_reason": reason,
			"deleted_at":     time.Now().UTC(),
		}).Error; err != nil {
		return err
	}

	// An address-book sync removes a contact from the phone's address book; it
	// says nothing about the conversation, which may still be live and is
	// restored automatically on the next message. Only a deliberate deletion
	// ends the work.
	if reason != ReasonAddressBookSync {
		s.endWorkInFlight(ctx, db, orgID, contactID, actor)
	}

	s.publish(db, orgID, &contact, "contact.deleted", actor)
	return nil
}

// endWorkInFlight closes what a deleted contact was in the middle of.
//
// Each step is best-effort and logged rather than fatal: the contact is already
// deleted, and refusing to finish because a chatbot session would not cancel
// would leave the product in a worse state than a stale session does.
func (s *Service) endWorkInFlight(ctx context.Context, db *gorm.DB, orgID, contactID uuid.UUID, actor crmevents.Actor) {
	now := time.Now().UTC()

	// A live session is a half-asked question. Cancelling it means the next
	// message from that number starts a conversation rather than being read as
	// an answer to a prompt nobody remembers.
	//
	// This runs before the conversation is resolved, because resolving also
	// ends the session — as *completed*, which is the right word for a
	// conversation somebody finished and the wrong one for a contact that was
	// deleted out from under it.
	if err := db.Model(&models.ChatbotSession{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			orgID, contactID, models.SessionStatusActive).
		Updates(map[string]any{
			"status":       models.SessionStatusCancelled,
			"completed_at": now,
		}).Error; err != nil {
		s.logf("contacts: cancelling chatbot sessions for deleted contact %s: %v", contactID, err)
	}

	// A transfer whose contact no longer exists cannot be picked up. Expiring
	// it frees the queue position and the unique active-transfer index.
	if err := db.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			orgID, contactID, models.TransferStatusActive).
		Update("status", models.TransferStatusExpired).Error; err != nil {
		s.logf("contacts: expiring transfers for deleted contact %s: %v", contactID, err)
	}

	// The conversation is closed through the conversation service so there is
	// exactly one place that moves a conversation to resolved, with the events
	// and counters that go with it (S5).
	if s.Conversations != nil {
		if _, err := s.Conversations.Resolve(ctx, orgID, contactID, models.ResolutionContactDeleted, actor); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			s.logf("contacts: resolving conversation for deleted contact %s: %v", contactID, err)
		}
	}
}

// Restore undoes a soft delete explicitly, on a person's instruction. Unlike
// the automatic path in Resolve it does not care why the contact was deleted,
// because someone is asking for it directly.
func (s *Service) Restore(ctx context.Context, orgID, contactID uuid.UUID, actor crmevents.Actor) error {
	db := s.DB.WithContext(ctx)

	var contact models.Contact
	if err := db.Unscoped().Where("organization_id = ? AND id = ?", orgID, contactID).
		First(&contact).Error; err != nil {
		return err
	}
	if contact.MergedIntoID != nil {
		return fmt.Errorf("contacts: %s was merged and cannot be restored", contactID)
	}

	if err := db.Unscoped().Model(&models.Contact{}).Where("id = ?", contactID).
		Updates(map[string]any{"deleted_at": nil, "deleted_reason": ""}).Error; err != nil {
		return err
	}

	s.publish(db, orgID, &contact, "contact.restored", actor)
	return nil
}

// publish records a lifecycle event. Failures are logged by the relay rather
// than failing the operation the caller asked for; the event types that are not
// yet in the catalog are skipped silently by design.
func (s *Service) publish(db *gorm.DB, orgID uuid.UUID, contact *models.Contact, eventType string, actor crmevents.Actor) {
	if !crmevents.IsKnown(eventType) {
		return
	}
	if actor.Type == "" {
		actor = crmevents.SystemActor()
	}
	event := crmevents.New(orgID, eventType, actor, map[string]any{
		"contact_id":       contact.ID.String(),
		"contact_phone":    contact.PhoneNumber,
		"contact_name":     contact.ProfileName,
		"whatsapp_account": contact.WhatsAppAccount,
	}).ForContact(contact.ID)

	_ = crmevents.PublishTx(db, event)
}
