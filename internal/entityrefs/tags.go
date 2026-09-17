package entityrefs

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Tags inside stored filters and rule configs (plan 10, S8 / X10).
//
// Contacts store tags by name, which is fine on its own: a tag is a word, and
// a word is what an agent types. What was not fine is that the same word is
// also written into segment filter ASTs and automation trigger and action
// configs, as JSONB, and a rename updated only the contacts.
//
// The result was silent: a segment for "vip" kept returning zero contacts after
// the tag became "VIP customer", an automation that added "vip" carried on
// adding a tag nobody had, and neither said anything. Nothing errored, so
// nobody looked.
//
// Rewriting JSONB in place needs a parameterised statement per shape, which is
// why this is a small explicit list rather than a generic walk: every place a
// tag name can hide is named here, and a new one has to be added deliberately.

// TagUsage counts where a tag name appears outside the contacts table.
type TagUsage struct {
	Segments    int64 `json:"segments"`
	Automations int64 `json:"automations"`
}

// Total is what a confirmation dialog shows.
func (u TagUsage) Total() int64 { return u.Segments + u.Automations }

// CountTagRefs reports how many saved filters and rules mention a tag.
func CountTagRefs(db *gorm.DB, orgID uuid.UUID, name string) (TagUsage, error) {
	var usage TagUsage

	if err := db.Table("segments").
		Where("organization_id = ? AND deleted_at IS NULL", orgID).
		Where("filter::text LIKE ?", "%"+jsonQuoted(name)+"%").
		Count(&usage.Segments).Error; err != nil {
		return usage, err
	}

	if err := db.Table("automation_rules").
		Where("organization_id = ? AND deleted_at IS NULL", orgID).
		Where("trigger_config::text LIKE ? OR actions::text LIKE ? OR contact_filter::text LIKE ?",
			"%"+jsonQuoted(name)+"%", "%"+jsonQuoted(name)+"%", "%"+jsonQuoted(name)+"%").
		Count(&usage.Automations).Error; err != nil {
		return usage, err
	}

	return usage, nil
}

// RenameTag rewrites a tag name everywhere it is stored.
//
// The contacts table is the caller's job — it has its own, well-tested
// statement. This covers the places a rename used to miss.
//
// The rewrite is a whole-token JSON replacement, not a substring one: renaming
// "vip" must not turn "vip-2024" into "VIP customer-2024". That is what
// quoting the value on both sides buys.
func RenameTag(tx *gorm.DB, orgID uuid.UUID, oldName, newName string) error {
	if oldName == newName {
		return nil
	}

	from, to := jsonQuoted(oldName), jsonQuoted(newName)

	if err := tx.Exec(`
		UPDATE segments
		SET filter = replace(filter::text, ?, ?)::jsonb
		WHERE organization_id = ? AND deleted_at IS NULL
		  AND filter::text LIKE ?`,
		from, to, orgID, "%"+from+"%").Error; err != nil {
		return err
	}

	// contact_filter is nullable, so it is coalesced rather than left to turn
	// the whole row's update into NULL.
	return tx.Exec(`
		UPDATE automation_rules
		SET trigger_config = replace(trigger_config::text, ?, ?)::jsonb,
		    actions        = replace(actions::text, ?, ?)::jsonb,
		    contact_filter = CASE
		        WHEN contact_filter IS NULL THEN NULL
		        ELSE replace(contact_filter::text, ?, ?)::jsonb
		    END
		WHERE organization_id = ? AND deleted_at IS NULL
		  AND (trigger_config::text LIKE ? OR actions::text LIKE ?
		       OR coalesce(contact_filter::text, '') LIKE ?)`,
		from, to, from, to, from, to, orgID,
		"%"+from+"%", "%"+from+"%", "%"+from+"%").Error
}

// jsonQuoted renders a value the way it appears inside JSONB text, so a
// replacement matches a whole string and not a fragment of a longer one.
func jsonQuoted(value string) string {
	// Escape the two characters that would otherwise break out of the quoted
	// form. JSONB stores text the same way JSON does.
	escaped := make([]rune, 0, len(value)+2)
	for _, r := range value {
		if r == '"' || r == '\\' {
			escaped = append(escaped, '\\')
		}
		escaped = append(escaped, r)
	}
	return `"` + string(escaped) + `"`
}
