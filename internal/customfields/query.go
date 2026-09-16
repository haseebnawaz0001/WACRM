package customfields

import (
	"fmt"

	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
)

// FieldKeyPrefix namespaces custom fields in the filter language, so a field
// called "source" cannot collide with the built-in contacts.source column.
const FieldKeyPrefix = "field."

// RegisterFields adds an organization's custom fields to a query registry
// (plan 01 + plan 00 F6).
//
// Definitions are per-organization, so the registry is built per request rather
// than once at startup: two organizations legitimately have different fields
// under different keys, and a shared registry would leak one org's schema into
// another's filter builder.
func RegisterFields(r *contactquery.Registry, defs []models.CustomFieldDefinition) {
	for _, def := range defs {
		if def.IsArchived() {
			// Archived fields keep their values readable but are not offered
			// as filters, so a builder cannot construct a query on something
			// the org has retired.
			continue
		}
		r.Register(fieldFor(def))
	}
}

// fieldFor maps a definition onto a query field.
func fieldFor(def models.CustomFieldDefinition) contactquery.Field {
	field := contactquery.Field{
		Key:      FieldKeyPrefix + def.Key,
		LabelKey: def.Label,
		Type:     queryType(def.Type),
		Build:    buildFor(def),
		Sortable: def.Type != models.FieldTypeDropdown,
	}

	if def.Type == models.FieldTypeDropdown {
		for _, raw := range def.Options {
			opt, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			value, _ := opt["value"].(string)
			label, _ := opt["label"].(string)
			field.Options = append(field.Options, contactquery.Option{Value: value, Label: label})
		}
	}
	return field
}

// queryType maps a field type onto the filter language's type.
//
// Email and phone are text as far as filtering goes: the difference between
// them is validation on the way in, not how they are compared.
func queryType(fieldType string) contactquery.FieldType {
	switch fieldType {
	case models.FieldTypeNumber:
		return contactquery.TypeNumber
	case models.FieldTypeDate:
		return contactquery.TypeDate
	case models.FieldTypeDropdown:
		return contactquery.TypeOption
	default:
		return contactquery.TypeText
	}
}

// buildFor returns a compiler for one custom field.
//
// Values live in their own table, so a rule becomes an EXISTS subquery rather
// than a column comparison. EXISTS rather than a join because a join would
// multiply contact rows once several field rules appear in the same filter.
func buildFor(def models.CustomFieldDefinition) contactquery.BuildFunc {
	column := valueColumn(def.Type)

	return func(f contactquery.Field, rule contactquery.Node, v contactquery.Viewer) (string, []any, error) {
		// "Is empty" asks about the absence of a value, which is the absence
		// of a row — the inner condition would never match, so it has to be
		// negated at the EXISTS level instead.
		if rule.Operator == contactquery.OpIsEmpty {
			return fmt.Sprintf(`NOT EXISTS (
				SELECT 1 FROM custom_field_values cfv
				WHERE cfv.entity_type = 'contact'
				  AND cfv.entity_id = contacts.id
				  AND cfv.field_id = ?
				  AND cfv.%s IS NOT NULL)`, column), []any{def.ID}, nil
		}

		inner, err := innerCondition(f.Type, column, rule, v)
		if err != nil {
			return "", nil, err
		}

		sql := fmt.Sprintf(`EXISTS (
			SELECT 1 FROM custom_field_values cfv
			WHERE cfv.entity_type = 'contact'
			  AND cfv.entity_id = contacts.id
			  AND cfv.field_id = ?
			  AND %s)`, inner.SQL)

		args := append([]any{def.ID}, inner.Args...)
		return sql, args, nil
	}
}

// innerCondition compiles the rule against the value column, reusing the same
// operator semantics the core fields use.
func innerCondition(t contactquery.FieldType, column string, rule contactquery.Node, v contactquery.Viewer) (contactquery.Compiled, error) {
	// A synthetic field whose "column" is the value column of the subquery.
	proxy := contactquery.Field{Key: "cfv", Type: t, Column: "cfv." + column}
	return contactquery.CompileLeaf(proxy, rule, v)
}

// valueColumn is the typed column a field's values live in.
func valueColumn(fieldType string) string {
	switch fieldType {
	case models.FieldTypeNumber:
		return "value_number"
	case models.FieldTypeDate:
		return "value_date"
	case models.FieldTypeDropdown:
		return "value_option"
	default:
		return "value_text"
	}
}
