package handlers

import (
	"context"

	"github.com/shridarpatil/whatomate/internal/crmcontext"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// GetVariableCatalog lists the template variables available in one context
// (plan 10, S6).
//
// Every screen that accepts a template needs a picker, and each one used to
// carry its own hardcoded list. They drifted — the canned-response picker
// offered a spelling nothing else understood, and the custom fields an
// organization had actually defined appeared in no picker at all. Serving the
// list from the same package the renderer uses means the two cannot disagree.
func (a *App) GetVariableCatalog(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceChat, models.ActionRead)
	if err != nil {
		return nil
	}

	contextName := string(r.RequestCtx.QueryArgs().Peek("context"))
	if contextName == "" {
		contextName = crmcontext.ContextCanned
	}

	variables := crmcontext.Catalog(contextName)
	if variables == nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"Unknown variable context", map[string]any{
				"allowed": crmcontext.Contexts(),
			}, "")
	}

	// Expand contact.fields. into this organization's own fields, so the
	// picker offers "Company" rather than asking the author to remember the
	// key. A failure here degrades to the generic entry rather than failing
	// the request: a picker without custom fields is still a working picker.
	expanded := make([]crmcontext.Variable, 0, len(variables)+8)
	for _, v := range variables {
		if !v.Dynamic || v.Path != "contact.fields." {
			expanded = append(expanded, v)
			continue
		}
		defs, defErr := customfields.New(a.DB).Definitions(context.Background(), orgID, models.FieldEntityContact)
		if defErr != nil {
			a.Log.Error("Failed to load contact fields for variable catalog", "error", defErr)
			expanded = append(expanded, v)
			continue
		}
		for _, def := range defs {
			if def.ArchivedAt != nil {
				continue
			}
			expanded = append(expanded, crmcontext.Variable{
				Path:  "contact.fields." + def.Key,
				Label: def.Label,
				Group: "contact",
			})
		}
	}

	return r.SendEnvelope(map[string]any{
		"context":   contextName,
		"variables": expanded,
	})
}
