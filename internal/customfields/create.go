package customfields

import (
	"sort"

	"github.com/shridarpatil/whatomate/internal/models"
)

// ApplyDefaults fills in the fields a manual create did not mention (plan 01).
//
// A default that only appears in the editor is not a default: the first
// contact created through the API, an import or a bulk action would miss it,
// and the organization would find the field empty on exactly the records it
// never opened. Defaults are applied here, once, for every path that creates a
// record by hand.
//
// An explicit null stays null. Somebody clearing a field means it, and
// re-filling it from the default would make the field impossible to empty.
func ApplyDefaults(defs map[string]models.CustomFieldDefinition, values map[string]any) map[string]any {
	out := values
	if out == nil {
		out = map[string]any{}
	}
	for key, def := range defs {
		if def.IsArchived() || def.DefaultValue == nil {
			continue
		}
		if _, given := out[key]; given {
			continue
		}
		if fallback, ok := def.DefaultValue["value"]; ok && fallback != nil {
			out[key] = fallback
		}
	}
	return out
}

// MissingRequired lists the labels of required fields a manual create or edit
// left empty.
//
// It is deliberately not applied to inbound paths. A message from a customer
// must never be refused because an organization made "Company" mandatory —
// the contact has to exist before anyone can fill that in.
func MissingRequired(defs map[string]models.CustomFieldDefinition, values map[string]any) []string {
	var missing []string
	for key, def := range defs {
		if def.IsArchived() || !def.IsRequired {
			continue
		}
		raw, given := values[key]
		if !given || raw == nil || raw == "" {
			missing = append(missing, def.Label)
		}
	}
	sort.Strings(missing)
	return missing
}
