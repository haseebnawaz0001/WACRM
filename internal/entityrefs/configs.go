package entityrefs

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// References buried inside stored configuration (plan 10, S8).
//
// Segments store a filter AST as JSONB. Automation rules store a trigger
// config, a contact filter and an action list the same way. Both are full of
// ids and names pointing at things an administrator can delete or rename: a
// template, a pipeline stage, another segment, a dropdown option on a custom
// field.
//
// Nothing pointed back. Deleting a template left the automation that sent it
// with an id resolving to nothing, and the rule carried on "running" — the send
// failed per contact, the failure counter climbed, and the reason was visible
// only in a run record nobody had opened. Archiving a pipeline stage left
// segment filters silently matching zero contacts.
//
// Two operations answer that: count what points at a thing before removing it,
// so the product can say "forty rules use this", and rewrite the value when it
// is renamed rather than deleted.
//
// Matching is textual over the JSONB, which is blunt but honest about what it
// can do: it finds a value wherever it is stored without this package needing
// to know every config shape, and quoting both sides means it matches a whole
// value rather than a fragment of a longer one.

// ConfigHolder is one table whose JSONB columns can name other entities.
type ConfigHolder struct {
	// Table is the table name.
	Table string
	// Label names it in a message shown to a person.
	Label string
	// Columns are the JSONB columns to search. A nullable column is coalesced
	// so a NULL does not make the whole row's comparison NULL.
	Columns []string
	// NameColumn is what to report so the message says which rule or segment.
	NameColumn string
}

// configHolders is every place a reference can hide.
//
// Explicit rather than discovered: a JSONB column that happens to contain a
// uuid is not necessarily a reference, and treating one as such would block a
// delete for a reason nobody could explain.
var configHolders = []ConfigHolder{
	{
		Table:      "segments",
		Label:      "segment",
		Columns:    []string{"filter"},
		NameColumn: "name",
	},
	{
		Table:      "automation_rules",
		Label:      "automation",
		Columns:    []string{"trigger_config", "actions", "contact_filter"},
		NameColumn: "name",
	},
}

// Dependent is one thing that points at the entity being removed.
type Dependent struct {
	// Kind is "segment" or "automation".
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Describe renders a dependent for a message shown to a person.
func (d Dependent) Describe() string { return d.Kind + " \"" + d.Name + "\"" }

// FindDependents lists the saved filters and rules that name a value.
//
// value is an id or a stored name, whichever the config holds. Pass what the
// config would contain, not what the entity is called on screen.
func FindDependents(db *gorm.DB, orgID uuid.UUID, value string) ([]Dependent, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	needle := "%" + jsonQuoted(value) + "%"

	var out []Dependent
	for _, holder := range configHolders {
		clauses := make([]string, 0, len(holder.Columns))
		args := make([]any, 0, len(holder.Columns)+1)
		args = append(args, orgID)
		for _, column := range holder.Columns {
			clauses = append(clauses, fmt.Sprintf("coalesce(%q::text, '') LIKE ?", column))
			args = append(args, needle)
		}

		type row struct {
			ID   string
			Name string
		}
		var rows []row
		query := fmt.Sprintf(
			`SELECT id::text AS id, %q AS name FROM %q
			 WHERE organization_id = ? AND deleted_at IS NULL AND (%s)`,
			holder.NameColumn, holder.Table, strings.Join(clauses, " OR "))

		if err := db.Raw(query, args...).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("entityrefs: find dependents in %s: %w", holder.Table, err)
		}
		for _, r := range rows {
			out = append(out, Dependent{Kind: holder.Label, Name: r.Name, ID: r.ID})
		}
	}
	return out, nil
}

// DescribeDependents renders a list for a 409 message.
//
// Truncated at five: a message naming forty rules is a message nobody reads,
// and the count tells them the rest is there.
func DescribeDependents(dependents []Dependent) string {
	if len(dependents) == 0 {
		return ""
	}

	shown := dependents
	suffix := ""
	if len(shown) > 5 {
		shown = shown[:5]
		suffix = fmt.Sprintf(" and %d more", len(dependents)-5)
	}

	parts := make([]string, 0, len(shown))
	for _, d := range shown {
		parts = append(parts, d.Describe())
	}
	return strings.Join(parts, ", ") + suffix
}

// RewriteConfigValue replaces a stored value everywhere it appears in saved
// filters and rule configs.
//
// Used when something is renamed rather than removed: the rename should follow,
// not leave every config pointing at a name that no longer exists.
//
// Unrestricted, so it suits a value that is unique on its own — an id. For a
// bare word, narrow it with RewriteConfigValueWithin.
func RewriteConfigValue(tx *gorm.DB, orgID uuid.UUID, oldValue, newValue string) error {
	return RewriteConfigValueWithin(tx, orgID, oldValue, newValue, "")
}

// RewriteConfigValueWithin is RewriteConfigValue confined to rows that also
// mention mustContain.
//
// Matching a bare value across every rule in an organization is too blunt to
// write with, even though it is fine to warn with: renaming the dropdown option
// "new" would rewrite any rule that happens to store "new" for something else.
// Passing the owning field's key confines the rewrite to configs that refer to
// that field. An empty mustContain keeps the unrestricted behaviour.
func RewriteConfigValueWithin(tx *gorm.DB, orgID uuid.UUID, oldValue, newValue, mustContain string) error {
	if oldValue == newValue || strings.TrimSpace(oldValue) == "" {
		return nil
	}
	from, to := jsonQuoted(oldValue), jsonQuoted(newValue)

	for _, holder := range configHolders {
		sets := make([]string, 0, len(holder.Columns))
		args := make([]any, 0, len(holder.Columns)*4+1)

		for _, column := range holder.Columns {
			// A nullable column stays NULL: turning it into '""'::jsonb would
			// invent a filter that was never there.
			sets = append(sets, fmt.Sprintf(
				`%q = CASE WHEN %q IS NULL THEN NULL ELSE replace(%q::text, ?, ?)::jsonb END`,
				column, column, column))
			args = append(args, from, to)
		}
		args = append(args, orgID)

		conditions := []string{holder.anyColumnLike(&args, "%"+from+"%")}
		if strings.TrimSpace(mustContain) != "" {
			conditions = append(conditions, holder.anyColumnLike(&args, "%"+mustContain+"%"))
		}

		query := fmt.Sprintf(
			`UPDATE %q SET %s WHERE organization_id = ? AND deleted_at IS NULL AND %s`,
			holder.Table, strings.Join(sets, ", "), strings.Join(conditions, " AND "))

		if err := tx.Exec(query, args...).Error; err != nil {
			return fmt.Errorf("entityrefs: rewrite %s in %s: %w", oldValue, holder.Table, err)
		}
	}
	return nil
}

// anyColumnLike builds "(col LIKE ? OR ...)" and appends one argument per
// column, so the caller's argument order follows the clause order.
func (h ConfigHolder) anyColumnLike(args *[]any, needle string) string {
	clauses := make([]string, 0, len(h.Columns))
	for _, column := range h.Columns {
		clauses = append(clauses, fmt.Sprintf("coalesce(%q::text, '') LIKE ?", column))
		*args = append(*args, needle)
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}
