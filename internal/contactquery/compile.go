package contactquery

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Viewer is who is running the query. It resolves "is me" and, through Scope,
// which contacts they may see at all.
type Viewer struct {
	UserID uuid.UUID
	OrgID  uuid.UUID

	// Location is the organisation's timezone. Date rules are evaluated in
	// it, so "today" means the business's today rather than the server's.
	Location *time.Location

	// CanSeeAllContacts is true for roles that are not restricted to their
	// own contacts.
	CanSeeAllContacts bool
}

// BuildFunc compiles one rule into a SQL fragment and its bound arguments.
//
// Returning a fragment rather than mutating the query keeps every rule
// composable under AND/OR, and keeps values in the args slice where they stay
// parameters.
type BuildFunc func(f Field, rule Node, v Viewer) (string, []any, error)

// Compiled is a WHERE fragment with its bound arguments.
type Compiled struct {
	SQL  string
	Args []any
}

// Apply validates a filter and adds it to a query.
//
// The returned query is always scoped to the organisation, and to what the
// viewer is allowed to see, whether or not the filter says anything about it.
func Apply(db *gorm.DB, r *Registry, v Viewer, f Filter) (*gorm.DB, error) {
	q := db.Where("contacts.organization_id = ?", v.OrgID)

	if scope := Scope(v); scope.SQL != "" {
		q = q.Where(scope.SQL, scope.Args...)
	}

	if f.IsEmpty() {
		return q, nil
	}
	if err := Validate(r, f); err != nil {
		return nil, err
	}

	compiled, err := compileNode(r, f, v)
	if err != nil {
		return nil, err
	}
	if compiled.SQL == "" {
		return q, nil
	}
	return q.Where(compiled.SQL, compiled.Args...), nil
}

// Scope restricts a query to the contacts a viewer may see.
//
// This used to live inline in the contacts handler, which meant every new
// contact-bearing surface — timeline, tasks, deals, notes — either duplicated
// it or forgot it. One definition is what stops those from disagreeing.
func Scope(v Viewer) Compiled {
	if v.CanSeeAllContacts {
		return Compiled{}
	}
	// An agent sees contacts they own, plus any contact with an active
	// transfer assigned to them.
	return Compiled{
		SQL: `(contacts.assigned_user_id = ? OR EXISTS (
			SELECT 1 FROM agent_transfers t
			WHERE t.contact_id = contacts.id
			  AND t.organization_id = contacts.organization_id
			  AND t.status = 'active'
			  AND t.agent_id = ?
			  AND t.deleted_at IS NULL))`,
		Args: []any{v.UserID, v.UserID},
	}
}

func compileNode(r *Registry, n Node, v Viewer) (Compiled, error) {
	if n.IsGroup() {
		joiner := " AND "
		if strings.EqualFold(n.Op, OpOr) {
			joiner = " OR "
		}

		parts := make([]string, 0, len(n.Rules))
		args := make([]any, 0, len(n.Rules))
		for _, child := range n.Rules {
			compiled, err := compileNode(r, child, v)
			if err != nil {
				return Compiled{}, err
			}
			if compiled.SQL == "" {
				continue
			}
			parts = append(parts, compiled.SQL)
			args = append(args, compiled.Args...)
		}
		if len(parts) == 0 {
			return Compiled{}, nil
		}
		return Compiled{SQL: "(" + strings.Join(parts, joiner) + ")", Args: args}, nil
	}

	field, ok := r.Lookup(n.Field)
	if !ok {
		return Compiled{}, invalid("filter", "unknown field %q", n.Field)
	}
	if field.Build != nil {
		sql, args, err := field.Build(field, n, v)
		if err != nil {
			return Compiled{}, err
		}
		return Compiled{SQL: sql, Args: args}, nil
	}
	return CompileLeaf(field, n, v)
}

// CompileLeaf turns one rule into SQL against a field's column.
//
// It is exported so a package owning a relational field — custom field values
// live in their own table — can reuse the operator semantics inside its own
// subquery instead of reimplementing them and drifting.
//
// The column is taken from the Field, which only ever comes from the registry;
// everything the caller supplied becomes a bound argument.
func CompileLeaf(f Field, rule Node, v Viewer) (Compiled, error) {
	col := f.Column

	switch f.Type {
	case TypeText:
		return compileText(col, rule)
	case TypeNumber:
		return compileNumber(col, rule)
	case TypeDate:
		return compileDate(col, rule, v)
	case TypeOption:
		return compileOption(col, rule)
	case TypeTags:
		return compileTags(col, rule)
	case TypeBoolean:
		return compileBoolean(col, rule)
	case TypeUser:
		return compileUser(col, rule, v)
	}
	return Compiled{}, invalid("filter", "field %q has unsupported type %q", f.Key, f.Type)
}

func compileText(col string, rule Node) (Compiled, error) {
	switch rule.Operator {
	case OpEquals:
		return Compiled{SQL: col + " = ?", Args: []any{rule.Value}}, nil
	case OpNotEquals:
		return Compiled{SQL: "(" + col + " IS DISTINCT FROM ?)", Args: []any{rule.Value}}, nil
	case OpContains:
		return Compiled{SQL: col + " ILIKE ?", Args: []any{"%" + escapeLike(asString(rule.Value)) + "%"}}, nil
	case OpStartsWith:
		return Compiled{SQL: col + " ILIKE ?", Args: []any{escapeLike(asString(rule.Value)) + "%"}}, nil
	case OpIsEmpty:
		return Compiled{SQL: "(" + col + " IS NULL OR " + col + " = '')"}, nil
	case OpIsNotEmpty:
		return Compiled{SQL: "(" + col + " IS NOT NULL AND " + col + " <> '')"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported text operator %q", rule.Operator)
}

func compileNumber(col string, rule Node) (Compiled, error) {
	switch rule.Operator {
	case OpEq:
		return Compiled{SQL: col + " = ?", Args: []any{rule.Value}}, nil
	case OpNeq:
		return Compiled{SQL: "(" + col + " IS DISTINCT FROM ?)", Args: []any{rule.Value}}, nil
	case OpGt:
		return Compiled{SQL: col + " > ?", Args: []any{rule.Value}}, nil
	case OpGte:
		return Compiled{SQL: col + " >= ?", Args: []any{rule.Value}}, nil
	case OpLt:
		return Compiled{SQL: col + " < ?", Args: []any{rule.Value}}, nil
	case OpLte:
		return Compiled{SQL: col + " <= ?", Args: []any{rule.Value}}, nil
	case OpBetween:
		pair, _ := toList(rule.Value)
		return Compiled{SQL: col + " BETWEEN ? AND ?", Args: []any{pair[0], pair[1]}}, nil
	case OpIsEmpty:
		return Compiled{SQL: col + " IS NULL"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported number operator %q", rule.Operator)
}

func compileDate(col string, rule Node, v Viewer) (Compiled, error) {
	loc := v.Location
	if loc == nil {
		loc = time.UTC
	}

	switch rule.Operator {
	case OpOn:
		at, err := parseTime(rule.Value)
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		// "On a day" is a range in the organisation's timezone, not a
		// string comparison: the same instant is a different date in
		// different zones.
		start := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
		return Compiled{
			SQL:  "(" + col + " >= ? AND " + col + " < ?)",
			Args: []any{start, start.AddDate(0, 0, 1)},
		}, nil

	case OpBefore:
		at, err := parseTime(rule.Value)
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		return Compiled{SQL: col + " < ?", Args: []any{at}}, nil

	case OpAfter:
		at, err := parseTime(rule.Value)
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		return Compiled{SQL: col + " > ?", Args: []any{at}}, nil

	case OpBetween:
		pair, _ := toList(rule.Value)
		from, err := parseTime(pair[0])
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		to, err := parseTime(pair[1])
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		return Compiled{SQL: "(" + col + " >= ? AND " + col + " <= ?)", Args: []any{from, to}}, nil

	case OpWithinLast:
		d, err := parseDuration(rule.Value)
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		return Compiled{SQL: col + " >= ?", Args: []any{time.Now().Add(-d)}}, nil

	case OpMoreThanAgo:
		d, err := parseDuration(rule.Value)
		if err != nil {
			return Compiled{}, invalid("filter", "%s", err.Error())
		}
		return Compiled{SQL: col + " < ?", Args: []any{time.Now().Add(-d)}}, nil

	case OpIsEmpty:
		return Compiled{SQL: col + " IS NULL"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported date operator %q", rule.Operator)
}

func compileOption(col string, rule Node) (Compiled, error) {
	switch rule.Operator {
	case OpIn:
		list, _ := toList(rule.Value)
		return Compiled{SQL: col + " IN ?", Args: []any{list}}, nil
	case OpNotIn:
		list, _ := toList(rule.Value)
		// NOT IN must also keep NULLs, which SQL's three-valued logic would
		// otherwise drop — a contact with no account is "not in" any list.
		return Compiled{SQL: "(" + col + " IS NULL OR " + col + " NOT IN ?)", Args: []any{list}}, nil
	case OpIsEmpty:
		return Compiled{SQL: "(" + col + " IS NULL OR " + col + " = '')"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported option operator %q", rule.Operator)
}

// compileTags works over the JSONB tag array on contacts.
//
// jsonb_exists_any / jsonb_exists_all rather than the ?| and ?& operators:
// GORM reads ? as a bind placeholder, so the operator form breaks the
// statement before it is ever planned.
func compileTags(col string, rule Node) (Compiled, error) {
	switch rule.Operator {
	case OpContainsAny:
		arr, args := textArray(rule.Value)
		return Compiled{SQL: "jsonb_exists_any(" + col + ", " + arr + ")", Args: args}, nil
	case OpContainsAll:
		arr, args := textArray(rule.Value)
		return Compiled{SQL: "jsonb_exists_all(" + col + ", " + arr + ")", Args: args}, nil
	case OpContainsNone:
		arr, args := textArray(rule.Value)
		return Compiled{SQL: "NOT jsonb_exists_any(" + col + ", " + arr + ")", Args: args}, nil
	case OpIsEmpty:
		return Compiled{SQL: "(" + col + " IS NULL OR jsonb_array_length(" + col + ") = 0)"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported tags operator %q", rule.Operator)
}

// textArray renders a list as ARRAY[?, ?, ...]::text[] with one placeholder per
// value.
//
// Passing a Go slice as a single argument does not work: the driver sends it as
// a record rather than a text[], and jsonb_exists_any has no such overload.
// Writing the placeholders out keeps every value bound while producing the type
// the function actually takes.
func textArray(value any) (string, []any) {
	list, _ := toList(value)
	if len(list) == 0 {
		return "ARRAY[]::text[]", nil
	}

	placeholders := make([]string, 0, len(list))
	args := make([]any, 0, len(list))
	for _, item := range list {
		placeholders = append(placeholders, "?")
		args = append(args, asString(item))
	}
	return "ARRAY[" + strings.Join(placeholders, ", ") + "]::text[]", args
}

func compileBoolean(col string, rule Node) (Compiled, error) {
	switch rule.Operator {
	case OpIsTrue:
		return Compiled{SQL: col + " = true"}, nil
	case OpIsFalse:
		// A NULL boolean is not true, so it belongs with false here.
		return Compiled{SQL: "(" + col + " = false OR " + col + " IS NULL)"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported boolean operator %q", rule.Operator)
}

func compileUser(col string, rule Node, v Viewer) (Compiled, error) {
	switch rule.Operator {
	case OpIs:
		return Compiled{SQL: col + " = ?", Args: []any{rule.Value}}, nil
	case OpIsNot:
		return Compiled{SQL: "(" + col + " IS NULL OR " + col + " <> ?)", Args: []any{rule.Value}}, nil
	case OpIsMe:
		return Compiled{SQL: col + " = ?", Args: []any{v.UserID}}, nil
	case OpIsUnassigned:
		return Compiled{SQL: col + " IS NULL"}, nil
	}
	return Compiled{}, invalid("filter", "unsupported user operator %q", rule.Operator)
}

// escapeLike neutralises LIKE wildcards in a user-supplied search term, so a
// "%" someone typed matches a literal percent sign instead of everything.
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
