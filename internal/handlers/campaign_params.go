package handlers

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/messaging"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templating"
)

// Campaign template variable mapping (plan 05, corrected by plan 10 S6).
//
// A segment-targeted campaign had no way to fill its template's variables. The
// list flow gets them from the CSV, column by column; a segment has no CSV, so
// `{{customer_name}}` went out as the literal text, or the campaign could not
// be sent at all.
//
// The mapping says where each parameter's value comes from, using the same
// namespace every other template surface uses: `contact.name`,
// `contact.phone_number`, `contact.fields.<key>`, or a static string.
//
// Keys are the template's own parameter names (`ExtParamNames`) — "1", "2" for
// a positional template, words for a named one — because that is how the
// recipient rows and the worker's lookup already key them. Plan 05 originally
// specified numeric-only keys, which would not have matched a named template.

// ParamMapping says where one template parameter's value comes from.
type ParamMapping struct {
	// Source is a namespace path, or "static".
	Source string `json:"source"`
	// Value is the literal text when Source is "static".
	Value string `json:"value"`
	// Fallback is used when the source resolves to nothing.
	//
	// Meta rejects a send with an empty parameter, so a contact missing a
	// company name would fail rather than send — a fallback turns "this one
	// recipient has a gap" into a message that still goes out.
	Fallback string `json:"fallback"`
}

// ParamSources are the sources a mapping may name.
//
// Deliberately a closed list rather than an arbitrary path: a campaign renders
// for thousands of contacts at once, and a typo that silently produced empty
// values would be found by the recipients.
const (
	ParamSourceStatic = "static"
	ParamSourceName   = "contact.name"
	ParamSourcePhone  = "contact.phone_number"
	// ParamSourceFieldPrefix + a field key, e.g. "contact.fields.company".
	ParamSourceFieldPrefix = "contact.fields."
)

// ValidParamSource reports whether a mapping names something resolvable.
func ValidParamSource(source string) bool {
	switch source {
	case ParamSourceStatic, ParamSourceName, ParamSourcePhone:
		return true
	}
	return strings.HasPrefix(source, ParamSourceFieldPrefix) &&
		strings.TrimPrefix(source, ParamSourceFieldPrefix) != ""
}

// ValidateParamMappings checks a campaign's mappings against its template.
//
// Returns the parameters the template needs and the mapping does not cover, so
// the campaign screen can say which ones rather than refusing with "invalid".
func ValidateParamMappings(template *models.Template, mappings map[string]ParamMapping) (missing []string, badSources []string) {
	for name, mapping := range mappings {
		if !ValidParamSource(mapping.Source) {
			badSources = append(badSources, name+": "+mapping.Source)
		}
	}
	for _, name := range messaging.BodyParamNames(template) {
		if _, ok := mappings[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing, badSources
}

// resolveParamsForContacts renders every mapped parameter for a page of
// contacts, in one pass.
//
// Custom field values are fetched for the whole page rather than per contact:
// a campaign to forty thousand people would otherwise be forty thousand
// queries, which is the shape of the N+1 the contacts list already had.
func (a *App) resolveParamsForContacts(orgID uuid.UUID, contacts []models.Contact, mappings map[string]ParamMapping) (map[uuid.UUID]map[string]string, error) {
	out := make(map[uuid.UUID]map[string]string, len(contacts))
	if len(mappings) == 0 || len(contacts) == 0 {
		return out, nil
	}

	var fields map[uuid.UUID]map[string]any
	if mappingsUseFields(mappings) {
		ids := make([]uuid.UUID, 0, len(contacts))
		for _, contact := range contacts {
			ids = append(ids, contact.ID)
		}
		var err error
		fields, err = customfields.New(a.DB).ValuesFor(
			context.Background(), orgID, ids, models.FieldEntityContact)
		if err != nil {
			return nil, err
		}
	}

	for _, contact := range contacts {
		params := make(map[string]string, len(mappings))
		for name, mapping := range mappings {
			params[name] = resolveParam(mapping, contact, fields[contact.ID])
		}
		out[contact.ID] = params
	}
	return out, nil
}

func mappingsUseFields(mappings map[string]ParamMapping) bool {
	for _, mapping := range mappings {
		if strings.HasPrefix(mapping.Source, ParamSourceFieldPrefix) {
			return true
		}
	}
	return false
}

// resolveParam renders one mapping for one contact.
func resolveParam(mapping ParamMapping, contact models.Contact, fields map[string]any) string {
	var value string

	switch {
	case mapping.Source == ParamSourceStatic:
		value = mapping.Value
	case mapping.Source == ParamSourceName:
		value = contact.ProfileName
	case mapping.Source == ParamSourcePhone:
		value = contact.PhoneNumber
	case strings.HasPrefix(mapping.Source, ParamSourceFieldPrefix):
		key := strings.TrimPrefix(mapping.Source, ParamSourceFieldPrefix)
		if raw, ok := fields[key]; ok && raw != nil {
			value = formatFieldValue(raw)
		}
	}

	if strings.TrimSpace(value) == "" {
		return mapping.Fallback
	}
	return value
}

// formatFieldValue renders a typed field value as template text.
func formatFieldValue(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		// Numbers, dates and booleans all arrive as their JSON forms; the
		// shared formatter is what every other template surface uses, so a
		// number renders the same way here as it does in a canned response.
		return templating.FormatValue(v)
	}
}
