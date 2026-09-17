package entityrefs

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Re-pointing a merged contact (plan 10, S8 / plan 06).
//
// Merge moved rows from seven tables named in a literal map. Everything else
// that references a contact stayed behind pointing at a record that had been
// soft-deleted: campaign recipients, chatbot sessions, automation runs and the
// per-contact automation state, custom field values, the contact's own
// identities and duplicate candidates.
//
// The failure is quiet, which is what makes it bad. Nothing errors — the rows
// are still there, they just belong to a contact nobody can open. "Why did this
// automation not fire for her?" has no visible answer, because the rule's
// once-per-contact state is filed under the id that was merged away.
//
// The table list is discovered from the live schema rather than written out,
// for the same reason the org purge discovers its own: a table added next year
// with a contact_id is a table that would otherwise be forgotten, and the cost
// of forgetting is history that silently detaches.

// contactRefExceptions are tables the generic sweep must not touch, with the
// reason. Each is handled deliberately elsewhere.
var contactRefExceptions = map[string]string{
	// The contact rows themselves.
	"contacts": "the merge updates these directly",
	// Composite primary key on (rule_id, contact_id, subject_key): a blind
	// UPDATE collides when both contacts have state for the same rule and
	// subject.
	"automation_contact_state": "needs an upsert, see repointAutomationState",
	// The merge writes its own row.
	"contact_merges": "records the merge",
	// Candidate pairs are resolved by the merge, not moved.
	"contact_duplicate_candidates": "dismissed or resolved by the merge",
	// Conversations are consolidated under the one-active rule, not moved
	// blindly.
	"conversations": "consolidated by the merge under the one-active rule",
}

// ContactRefTables lists every table with a contact_id column, from the live
// schema, minus the ones handled deliberately.
func ContactRefTables(db *gorm.DB) ([]string, error) {
	var tables []string
	err := db.Raw(`
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = CURRENT_SCHEMA()
		  AND c.column_name = 'contact_id'
		  AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name`).Scan(&tables).Error
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(tables))
	for _, table := range tables {
		if _, skip := contactRefExceptions[table]; skip {
			continue
		}
		out = append(out, table)
	}
	return out, nil
}

// RepointContact moves every reference from one contact to another.
//
// Returns what moved, keyed by table, so the merge snapshot can say what it
// did — which is what makes a merge reviewable rather than a leap of faith.
func RepointContact(tx *gorm.DB, from, to uuid.UUID) (map[string]int64, error) {
	tables, err := ContactRefTables(tx)
	if err != nil {
		return nil, err
	}

	moved := make(map[string]int64, len(tables)+2)
	for _, table := range tables {
		result := tx.Exec(
			fmt.Sprintf(`UPDATE %q SET contact_id = ? WHERE contact_id = ?`, table),
			to, from)
		if result.Error != nil {
			return nil, fmt.Errorf("entityrefs: re-point %s: %w", table, result.Error)
		}
		if result.RowsAffected > 0 {
			moved[table] = result.RowsAffected
		}
	}

	state, err := repointAutomationState(tx, from, to)
	if err != nil {
		return nil, err
	}
	if state > 0 {
		moved["automation_contact_state"] = state
	}

	notifications, err := repointNotifications(tx, from, to)
	if err != nil {
		return nil, err
	}
	if notifications > 0 {
		moved["notifications"] = notifications
	}

	return moved, nil
}

// repointAutomationState moves per-contact automation state, resolving the
// collision when both contacts have state for the same rule and subject.
//
// The primary key is (rule_id, contact_id, subject_key), so a plain UPDATE
// fails whenever the same rule has run for both. Keeping the *later* run and
// the *summed* count preserves the meaning of the policies that read this
// table: once-per-contact stays honoured after the merge, and a cooldown
// measured from the last run does not reset because two records were joined.
func repointAutomationState(tx *gorm.DB, from, to uuid.UUID) (int64, error) {
	if !tx.Migrator().HasTable("automation_contact_state") {
		return 0, nil
	}

	result := tx.Exec(`
		INSERT INTO automation_contact_state (rule_id, contact_id, subject_key, last_run_at, run_count)
		SELECT rule_id, ?, subject_key, last_run_at, run_count
		FROM automation_contact_state
		WHERE contact_id = ?
		ON CONFLICT (rule_id, contact_id, subject_key) DO UPDATE SET
			last_run_at = GREATEST(automation_contact_state.last_run_at, EXCLUDED.last_run_at),
			run_count   = automation_contact_state.run_count + EXCLUDED.run_count`,
		to, from)
	if result.Error != nil {
		return 0, fmt.Errorf("entityrefs: re-point automation state: %w", result.Error)
	}
	moved := result.RowsAffected

	if err := tx.Exec(
		`DELETE FROM automation_contact_state WHERE contact_id = ?`, from).Error; err != nil {
		return 0, fmt.Errorf("entityrefs: clear merged automation state: %w", err)
	}
	return moved, nil
}

// repointNotifications moves notifications that point at the merged contact.
//
// notifications.entity_id is a generic reference with an entity_type beside it,
// so it cannot be found by column name — which is exactly why it was missed.
// A notification linking to a contact that no longer opens is a dead end in
// somebody's bell.
func repointNotifications(tx *gorm.DB, from, to uuid.UUID) (int64, error) {
	if !tx.Migrator().HasTable("notifications") {
		return 0, nil
	}

	result := tx.Exec(`
		UPDATE notifications SET entity_id = ?
		WHERE entity_type = 'contact' AND entity_id = ?`, to, from)
	if result.Error != nil {
		return 0, fmt.Errorf("entityrefs: re-point notifications: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// RepointEntityValues moves generic entity-keyed rows, such as custom field
// values, that key on (entity_type, entity_id) rather than a contact_id column.
//
// Values already present on the primary win: the surviving record's own data is
// the one a person curated, and a merge should add what is missing rather than
// overwrite what is there.
func RepointEntityValues(tx *gorm.DB, table, entityType string, from, to uuid.UUID) (int64, error) {
	if !tx.Migrator().HasTable(table) {
		return 0, nil
	}

	result := tx.Exec(fmt.Sprintf(`
		UPDATE %q SET entity_id = ?
		WHERE entity_type = ? AND entity_id = ?`, table), to, entityType, from)
	if result.Error != nil {
		return 0, fmt.Errorf("entityrefs: re-point %s: %w", table, result.Error)
	}
	return result.RowsAffected, nil
}
