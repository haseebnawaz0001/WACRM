// Package dedupe finds contacts that are the same person and merges them
// (plan 06).
//
// Duplicates arrive constantly: the same customer messages from a second
// number, an import adds a row for someone already present, a colleague types
// a contact that exists. Left alone they split a person's history in two, so
// neither record tells the truth and campaigns message them twice.
package dedupe

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/phoneutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNotFound is returned when a contact or candidate does not exist.
var ErrNotFound = errors.New("dedupe: not found")

// ErrSameContact is returned when asked to merge a contact into itself.
var ErrSameContact = errors.New("dedupe: cannot merge a contact into itself")

// Match signals and their scores.
const (
	ReasonPhoneNormalized = "phone_normalized"
	ReasonEmail           = "email"
	ReasonNameAndSuffix   = "name_phone_suffix"

	ScorePhoneNormalized = 95
	ScoreEmail           = 85
	ScoreNameAndSuffix   = 60
)

// Service detects and merges duplicates.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Candidate is a suggested duplicate pair.
type Candidate struct {
	ContactAID uuid.UUID
	ContactBID uuid.UUID
	Reasons    []string
	Score      int
}

// Scan looks for duplicate pairs in an organization and records them.
//
// It only ever suggests. Merging automatically on a strong signal would be
// wrong often enough to matter — two family members legitimately share an email,
// and a shop and its owner share a phone — and an incorrect merge is far more
// expensive to undo than a duplicate is to live with.
func (s *Service) Scan(ctx context.Context, orgID uuid.UUID) (int, error) {
	found, err := s.findPairs(ctx, orgID)
	if err != nil {
		return 0, err
	}

	recorded := 0
	for _, pair := range found {
		a, b := orderPair(pair.ContactAID, pair.ContactBID)

		reasons := make(models.JSONBArray, 0, len(pair.Reasons))
		for _, reason := range pair.Reasons {
			reasons = append(reasons, reason)
		}

		row := models.ContactDuplicateCandidate{
			ID:             uuid.New(),
			OrganizationID: orgID,
			ContactAID:     a,
			ContactBID:     b,
			Reasons:        reasons,
			Score:          pair.Score,
			Status:         models.DuplicatePending,
		}

		// A pair someone already dismissed must stay dismissed: re-suggesting
		// it every scan would train people to ignore the whole list.
		result := s.DB.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "organization_id"}, {Name: "contact_a_id"}, {Name: "contact_b_id"}},
			DoNothing: true,
		}).Create(&row)
		if result.Error != nil {
			return recorded, result.Error
		}
		recorded += int(result.RowsAffected)
	}

	return recorded, nil
}

// findPairs gathers duplicate signals.
func (s *Service) findPairs(ctx context.Context, orgID uuid.UUID) ([]Candidate, error) {
	byPair := map[[2]uuid.UUID]*Candidate{}

	add := func(a, b uuid.UUID, reason string, score int) {
		if a == b {
			return
		}
		key := [2]uuid.UUID{}
		key[0], key[1] = orderPair(a, b)

		existing, ok := byPair[key]
		if !ok {
			byPair[key] = &Candidate{
				ContactAID: key[0], ContactBID: key[1],
				Reasons: []string{reason}, Score: score,
			}
			return
		}
		existing.Reasons = append(existing.Reasons, reason)
		// Several signals agreeing is stronger than any one of them, but the
		// score stays a confidence rather than a sum, so it is capped.
		if score > existing.Score {
			existing.Score = score
		}
	}

	// Same normalised phone: the strongest signal there is, and the one the
	// non-partial unique index used to make impossible to even record.
	type pairRow struct {
		A uuid.UUID
		B uuid.UUID
	}
	var phoneMatches []pairRow
	if err := s.DB.WithContext(ctx).Raw(`
		SELECT c1.id AS a, c2.id AS b
		FROM contacts c1
		JOIN contacts c2
		  ON c1.organization_id = c2.organization_id
		 AND c1.id < c2.id
		 AND c1.phone_normalized = c2.phone_normalized
		WHERE c1.organization_id = ?
		  AND c1.phone_normalized <> ''
		  AND c1.phone_normalized IS NOT NULL
		  AND c1.deleted_at IS NULL AND c2.deleted_at IS NULL
		  AND c1.merged_into_id IS NULL AND c2.merged_into_id IS NULL`,
		orgID).Scan(&phoneMatches).Error; err != nil {
		return nil, err
	}
	for _, m := range phoneMatches {
		add(m.A, m.B, ReasonPhoneNormalized, ScorePhoneNormalized)
	}

	// Same email custom field value.
	var emailMatches []pairRow
	if err := s.DB.WithContext(ctx).Raw(`
		SELECT v1.entity_id AS a, v2.entity_id AS b
		FROM custom_field_values v1
		JOIN custom_field_values v2
		  ON v1.field_id = v2.field_id
		 AND v1.entity_id < v2.entity_id
		 AND lower(v1.value_text) = lower(v2.value_text)
		JOIN custom_field_definitions d ON d.id = v1.field_id
		JOIN contacts c1 ON c1.id = v1.entity_id
		JOIN contacts c2 ON c2.id = v2.entity_id
		WHERE v1.organization_id = ?
		  AND v1.entity_type = 'contact'
		  AND d.type = 'email'
		  AND v1.value_text IS NOT NULL AND v1.value_text <> ''
		  AND c1.deleted_at IS NULL AND c2.deleted_at IS NULL
		  AND c1.merged_into_id IS NULL AND c2.merged_into_id IS NULL`,
		orgID).Scan(&emailMatches).Error; err != nil {
		return nil, err
	}
	for _, m := range emailMatches {
		add(m.A, m.B, ReasonEmail, ScoreEmail)
	}

	out := make([]Candidate, 0, len(byPair))
	for _, candidate := range byPair {
		out = append(out, *candidate)
	}
	return out, nil
}

// orderPair returns the two ids in canonical order, so (A,B) and (B,A) are one
// pair rather than two rows that never notice each other.
func orderPair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return a, b
			}
			return b, a
		}
	}
	return a, b
}

// Dismiss marks a pair as not duplicates.
func (s *Service) Dismiss(ctx context.Context, orgID, candidateID, userID uuid.UUID) error {
	now := time.Now().UTC()
	result := s.DB.WithContext(ctx).Model(&models.ContactDuplicateCandidate{}).
		Where("id = ? AND organization_id = ?", candidateID, orgID).
		Updates(map[string]any{
			"status":         models.DuplicateDismissed,
			"resolved_by_id": userID,
			"resolved_at":    now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// PendingCandidates returns unresolved pairs, strongest first.
func (s *Service) PendingCandidates(ctx context.Context, orgID uuid.UUID, limit int) ([]models.ContactDuplicateCandidate, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []models.ContactDuplicateCandidate
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND status = ?", orgID, models.DuplicatePending).
		Order("score DESC, created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// MergeInput describes a merge.
type MergeInput struct {
	OrgID     uuid.UUID
	PrimaryID uuid.UUID
	// SecondaryID is the contact that stops existing as a separate record.
	SecondaryID uuid.UUID
	ActorID     uuid.UUID
}

// Merge folds the secondary contact into the primary.
//
// Everything that pointed at the secondary is re-pointed rather than copied, so
// history moves wholesale and nothing is duplicated. The secondary keeps its
// row — soft-deleted with merged_into_id set — because a message arriving for
// its number afterwards still has to find the survivor.
func (s *Service) Merge(ctx context.Context, in MergeInput) (*models.ContactMerge, error) {
	if in.PrimaryID == in.SecondaryID {
		return nil, ErrSameContact
	}

	var record *models.ContactMerge

	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock both contacts in a fixed order. Two merges touching the same
		// pair from opposite directions would otherwise deadlock.
		first, second := orderPair(in.PrimaryID, in.SecondaryID)
		for _, id := range []uuid.UUID{first, second} {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, id.String()).Error; err != nil {
				return err
			}
		}

		var primary, secondary models.Contact
		if err := tx.Where("id = ? AND organization_id = ?", in.PrimaryID, in.OrgID).
			First(&primary).Error; err != nil {
			return ErrNotFound
		}
		// Unscoped: a contact merged earlier is soft-deleted, and finding it
		// is what lets this say "already merged" instead of "not found".
		if err := tx.Unscoped().Where("id = ? AND organization_id = ?", in.SecondaryID, in.OrgID).
			First(&secondary).Error; err != nil {
			return ErrNotFound
		}
		if secondary.MergedIntoID != nil {
			return fmt.Errorf("dedupe: that contact has already been merged")
		}
		if secondary.DeletedAt.Valid {
			return fmt.Errorf("dedupe: that contact has been deleted")
		}

		moved := map[string]any{}

		// Re-point everything that referenced the secondary. Moving rows keeps
		// one history rather than leaving half of it unreachable.
		//
		// Through the reference registry (plan 10, S8) rather than a literal
		// list: this used to name seven tables, and everything else — campaign
		// recipients, chatbot sessions, automation runs and per-contact state,
		// notifications — stayed pointing at a record nobody could open. The
		// registry discovers them from the live schema, so a table added later
		// is covered the day it exists.
		repointed, err := entityrefs.RepointContact(tx, in.SecondaryID, in.PrimaryID)
		if err != nil {
			return err
		}
		for table, count := range repointed {
			moved[table] = count
		}

		// Conversations are special: the primary may already have an active
		// one, and two active conversations for one contact is exactly what
		// the unique index forbids. The secondary's are resolved instead of
		// moved live.
		if err := tx.Exec(`
			UPDATE conversations SET status = 'resolved', resolved_at = now(),
			                         resolution_reason = ?
			WHERE contact_id = ? AND status <> 'resolved'`,
			models.ResolutionMerged, in.SecondaryID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE conversations SET contact_id = ? WHERE contact_id = ?`,
			in.PrimaryID, in.SecondaryID).Error; err != nil {
			return err
		}

		// Custom field values: the primary's win. A merge should never silently
		// overwrite the record someone chose to keep.
		if err := tx.Exec(`
			UPDATE custom_field_values SET entity_id = ?
			WHERE entity_type = 'contact' AND entity_id = ?
			  AND field_id NOT IN (
			    SELECT field_id FROM custom_field_values
			    WHERE entity_type = 'contact' AND entity_id = ?)`,
			in.PrimaryID, in.SecondaryID, in.PrimaryID).Error; err != nil {
			return err
		}
		if err := tx.Exec(`
			DELETE FROM custom_field_values
			WHERE entity_type = 'contact' AND entity_id = ?`, in.SecondaryID).Error; err != nil {
			return err
		}

		// Tags are merged, not replaced: both records' tags describe the same
		// person.
		merged := mergeTags(primary.Tags, secondary.Tags)
		if err := tx.Model(&models.Contact{}).Where("id = ?", in.PrimaryID).
			Update("tags", merged).Error; err != nil {
			return err
		}

		// The secondary's number becomes an identity of the primary, so a
		// message from it still reaches the right person.
		if err := s.addIdentity(tx, in.OrgID, in.PrimaryID, models.IdentityPhone,
			secondary.PhoneNumber, models.IdentitySourceMerge); err != nil {
			return err
		}
		if secondary.BSUID != "" {
			if err := s.addIdentity(tx, in.OrgID, in.PrimaryID, models.IdentityBSUID,
				secondary.BSUID, models.IdentitySourceMerge); err != nil {
				return err
			}
		}

		snapshot := models.JSONB{
			"secondary": map[string]any{
				"id":           secondary.ID.String(),
				"phone_number": secondary.PhoneNumber,
				"profile_name": secondary.ProfileName,
				"tags":         secondary.Tags,
				"source":       secondary.Source,
			},
			"moved": moved,
		}

		now := time.Now().UTC()
		if err := tx.Model(&models.Contact{}).Where("id = ?", in.SecondaryID).
			Updates(map[string]any{
				"merged_into_id": in.PrimaryID,
				"deleted_reason": "merged",
				"deleted_at":     now,
			}).Error; err != nil {
			return err
		}

		record = &models.ContactMerge{
			ID:                 uuid.New(),
			OrganizationID:     in.OrgID,
			PrimaryContactID:   in.PrimaryID,
			SecondaryContactID: in.SecondaryID,
			MergedByID:         in.ActorID,
			Snapshot:           snapshot,
			FieldResolution:    models.JSONB{"strategy": "primary_wins"},
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}

		// Any candidate naming this pair is settled now.
		a, b := orderPair(in.PrimaryID, in.SecondaryID)
		if err := tx.Model(&models.ContactDuplicateCandidate{}).
			Where("organization_id = ? AND contact_a_id = ? AND contact_b_id = ?", in.OrgID, a, b).
			Updates(map[string]any{
				"status": models.DuplicateMerged, "resolved_by_id": in.ActorID, "resolved_at": now,
			}).Error; err != nil {
			return err
		}

		event := crmevents.New(in.OrgID, "contact.merged",
			crmevents.UserActor(in.ActorID, ""), map[string]any{
				"primary_contact_id":   in.PrimaryID.String(),
				"secondary_contact_id": in.SecondaryID.String(),
			}).ForContact(in.PrimaryID)
		return crmevents.PublishTx(tx, event)
	})

	if err != nil {
		return nil, err
	}
	return record, nil
}

// addIdentity records an alternate identifier, ignoring one that already exists.
func (s *Service) addIdentity(tx *gorm.DB, orgID, contactID uuid.UUID, identityType, value, source string) error {
	normalized := value
	if identityType == models.IdentityPhone {
		normalized = phoneutil.Normalize(value)
	}
	if normalized == "" {
		return nil
	}

	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "organization_id"}, {Name: "type"}, {Name: "normalized"}},
		DoNothing: true,
	}).Create(&models.ContactIdentity{
		ID:             uuid.New(),
		OrganizationID: orgID,
		ContactID:      contactID,
		Type:           identityType,
		Value:          value,
		Normalized:     normalized,
		Source:         source,
	}).Error
}

// mergeTags unions two tag lists without duplicates.
func mergeTags(a, b models.JSONBArray) models.JSONBArray {
	seen := map[string]bool{}
	out := models.JSONBArray{}

	for _, list := range []models.JSONBArray{a, b} {
		for _, raw := range list {
			tag, ok := raw.(string)
			if !ok || seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

// ResolveIdentity finds the contact an identifier belongs to, following a merge.
func (s *Service) ResolveIdentity(ctx context.Context, orgID uuid.UUID, identityType, value string) (uuid.UUID, error) {
	normalized := value
	if identityType == models.IdentityPhone {
		normalized = phoneutil.Normalize(value)
	}

	var identity models.ContactIdentity
	if err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND type = ? AND normalized = ?", orgID, identityType, normalized).
		First(&identity).Error; err != nil {
		return uuid.Nil, ErrNotFound
	}
	return identity.ContactID, nil
}
