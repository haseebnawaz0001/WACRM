package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templating"
)

// CRM nodes let a chatbot flow use the shared action library (plan 10, S7).
//
// The library already backed automation. The chatbot could not reach it, so a
// flow that had just collected a customer's company and budget could only store
// them as session variables and end — anything that had to *happen* as a result
// meant writing an automation rule keyed on an event the flow never emitted.
// These two nodes close that gap without the chatbot growing its own second
// implementation of what "add a tag" means.

// crmActionDeps builds the dependencies the action library needs. It is the
// same wiring AutomationEngine uses, including the SSRF-safe HTTP client: a
// call_webhook action run from a flow is exactly as capable of being pointed at
// an internal address as one run from a rule.
func (a *App) crmActionDeps() crmactions.Deps {
	return crmactions.Deps{
		DB:         a.DB,
		HTTPClient: a.HTTPClient,
		Messenger:  automationMessenger{app: a},
		Assigner:   automationAssigner{app: a},
	}
}

// execChatCRMAction runs the node's configured actions in order.
//
// Config:
//
//	{
//	  "actions": [ {"type": "add_tags", "config": {...}}, ... ],
//	  "continue_on_error": false
//	}
//
// Outcome is "default" when every action succeeded, "error" when one failed.
// A flow that has not drawn an "error" edge simply falls through its default
// edge, so adding error handling is opt-in rather than a new way to get stuck.
func (a *App) execChatCRMAction(node *ChatNode, ctx *chatNodeCtx) (nodeOutcome, error) {
	specs := crmActionSpecs(node.Config)
	if len(specs) == 0 {
		a.Log.Warn("crm_action node has no actions", "node", node.ID, "session", ctx.session.ID)
		return nodeOutcome{outcome: "default"}, nil
	}

	continueOnError := boolFromConfig(node.Config, "continue_on_error")
	rc := a.chatRunContext(ctx)
	deps := a.crmActionDeps()

	var failed bool
	for i, spec := range specs {
		if _, ok := crmactions.Lookup(spec.Type); !ok {
			a.Log.Error("crm_action node references an unknown action",
				"node", node.ID, "action", spec.Type)
			failed = true
			if !continueOnError {
				break
			}
			continue
		}

		if _, err := crmactions.Execute(context.Background(), deps, rc, spec.Type, spec.Config); err != nil {
			a.Log.Error("crm_action failed", "node", node.ID, "action", spec.Type,
				"index", i, "session", ctx.session.ID, "error", err)
			failed = true
			if !continueOnError {
				break
			}
		}
	}

	if failed {
		return nodeOutcome{outcome: "error"}, nil
	}
	return nodeOutcome{outcome: "default"}, nil
}

// execChatCRMCondition branches on what is true of the contact.
//
// Config is either a filter or a segment:
//
//	{"filter": {"op": "and", "rules": [...]}}
//	{"segment_id": "<uuid>"}
//
// Outcome is "true" or "false". A condition that cannot be evaluated — a
// deleted segment, a filter naming a field that no longer exists — takes the
// "false" edge and logs, because a flow stopping dead mid-conversation is worse
// for the customer than taking the negative branch.
func (a *App) execChatCRMCondition(node *ChatNode, ctx *chatNodeCtx) (nodeOutcome, error) {
	matched, err := a.contactMatchesCRMCondition(node, ctx)
	if err != nil {
		a.Log.Error("crm_condition could not be evaluated; taking the false edge",
			"node", node.ID, "session", ctx.session.ID, "error", err)
		return nodeOutcome{outcome: "false"}, nil
	}
	if matched {
		return nodeOutcome{outcome: "true"}, nil
	}
	return nodeOutcome{outcome: "false"}, nil
}

// contactMatchesCRMCondition asks the database whether this one contact
// matches the node's condition.
//
// It reuses contactquery rather than evaluating the filter in Go: the segment a
// manager built in the contacts list and the branch a flow takes have to agree
// about what "VIP customers in London" means, and two implementations of that
// would eventually disagree. This is the same path automation's condition
// check takes, scoped to one contact.
func (a *App) contactMatchesCRMCondition(node *ChatNode, ctx *chatNodeCtx) (bool, error) {
	orgID := ctx.account.OrganizationID

	filter, ok, err := a.crmConditionFilter(node, orgID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("crm_condition: no filter or segment configured")
	}

	registry, err := a.contactRegistry(orgID)
	if err != nil {
		return false, fmt.Errorf("crm_condition: build registry: %w", err)
	}

	viewer := contactquery.Viewer{
		OrgID: orgID,
		// A flow is not a person: it sees whatever its condition names, or the
		// branch taken would depend on who happened to author the flow.
		CanSeeAllContacts: true,
		Location:          a.OrgLocation(orgID),
	}

	query, err := contactquery.Apply(a.DB.Model(&models.Contact{}), registry, viewer, filter)
	if err != nil {
		return false, fmt.Errorf("crm_condition: compile filter: %w", err)
	}

	var count int64
	if err := query.Where("contacts.id = ?", ctx.contact.ID).Count(&count).Error; err != nil {
		return false, fmt.Errorf("crm_condition: evaluate: %w", err)
	}
	return count > 0, nil
}

// crmConditionFilter resolves the node's condition into a filter, loading the
// saved segment when the node names one. ok is false when neither is set.
func (a *App) crmConditionFilter(node *ChatNode, orgID uuid.UUID) (contactquery.Filter, bool, error) {
	if raw := stringFromConfig(node.Config, "segment_id"); raw != "" {
		segmentID, err := uuid.Parse(raw)
		if err != nil {
			return contactquery.Filter{}, false, fmt.Errorf("crm_condition: bad segment_id %q", raw)
		}
		var segment models.Segment
		if err := a.DB.Where("organization_id = ? AND id = ?", orgID, segmentID).
			First(&segment).Error; err != nil {
			return contactquery.Filter{}, false, fmt.Errorf("crm_condition: load segment: %w", err)
		}
		encoded, err := json.Marshal(segment.Filter)
		if err != nil {
			return contactquery.Filter{}, false, fmt.Errorf("crm_condition: encode segment filter: %w", err)
		}
		filter, err := contactquery.ParseFilter(encoded)
		if err != nil {
			return contactquery.Filter{}, false, fmt.Errorf("crm_condition: parse segment filter: %w", err)
		}
		return filter, true, nil
	}

	raw, isMap := node.Config["filter"].(map[string]any)
	if !isMap || len(raw) == 0 {
		return contactquery.Filter{}, false, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return contactquery.Filter{}, false, fmt.Errorf("crm_condition: encode filter: %w", err)
	}
	filter, err := contactquery.ParseFilter(encoded)
	if err != nil {
		return contactquery.Filter{}, false, fmt.Errorf("crm_condition: parse filter: %w", err)
	}
	return filter, true, nil
}

// chatRunContext describes a flow-driven action run to the action library.
//
// The actor is the bot, not a user and not an automation: a contact's history
// should say the chatbot added the tag, because that is what a person reading
// the timeline needs to know to find the flow that did it.
func (a *App) chatRunContext(ctx *chatNodeCtx) crmactions.RunContext {
	vars := make(map[string]any, len(ctx.session.SessionData)+1)
	for k, v := range ctx.session.SessionData {
		vars[k] = v
	}
	vars["contact"] = map[string]any{
		"id":           ctx.contact.ID.String(),
		"profile_name": ctx.contact.ProfileName,
		"phone_number": ctx.contact.PhoneNumber,
	}

	return crmactions.RunContext{
		OrgID:     ctx.account.OrganizationID,
		ContactID: ctx.contact.ID,
		Actor:     crmevents.Actor{Type: crmevents.ActorSystem, Name: "Chatbot"},
		Vars:      vars,
		Location:  a.OrgLocation(ctx.account.OrganizationID),
	}
}

// crmActionSpec is one entry in a crm_action node's action list.
type crmActionSpec struct {
	Type   string
	Config crmactions.Config
}

// crmActionSpecs reads the action list out of a node config.
func crmActionSpecs(cfg map[string]any) []crmActionSpec {
	raw, _ := cfg["actions"].([]any)
	out := make([]crmActionSpec, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		actionType, _ := m["type"].(string)
		if actionType == "" {
			continue
		}
		config, _ := m["config"].(map[string]any)
		if config == nil {
			config = map[string]any{}
		}
		out = append(out, crmActionSpec{Type: actionType, Config: crmactions.Config(config)})
	}
	return out
}

// boolFromConfig reads a boolean out of a node config.
func boolFromConfig(cfg map[string]any, key string) bool {
	b, _ := cfg[key].(bool)
	return b
}

// keywordRuleActions reads a keyword rule's optional CRM action list.
//
// Stored as {"list": [...]} for the same reason automation_rules.actions is:
// the column is a JSON object, and a bespoke array type for one column would
// be worse than one wrapper key.
func keywordRuleActions(rule models.KeywordRule) []crmActionSpec {
	if len(rule.Actions) == 0 {
		return nil
	}
	list, _ := rule.Actions["list"].([]any)
	if len(list) == 0 {
		return nil
	}
	return crmActionSpecs(map[string]any{"actions": list})
}

// runKeywordActions performs a keyword rule's actions.
//
// A keyword rule is the cheapest automation in the product — "when someone
// says REFUND" — and until now it could only answer. Failures are logged, not
// surfaced: the customer has already had their reply, and there is nothing
// useful to tell them about a tag that did not stick.
func (a *App) runKeywordActions(account *models.WhatsAppAccount, contact *models.Contact,
	session *models.ChatbotSession, specs []crmActionSpec) {

	if len(specs) == 0 {
		return
	}
	rc := a.chatRunContext(&chatNodeCtx{account: account, contact: contact, session: session})
	deps := a.crmActionDeps()

	for _, spec := range specs {
		if _, ok := crmactions.Lookup(spec.Type); !ok {
			a.Log.Error("keyword rule references an unknown action", "action", spec.Type)
			continue
		}
		if _, err := crmactions.Execute(context.Background(), deps, rc, spec.Type, spec.Config); err != nil {
			a.Log.Error("keyword rule action failed", "action", spec.Type,
				"contact", contact.ID, "error", err)
		}
	}
}

// validateKeywordActions rejects an action list at save time.
func validateKeywordActions(actions []map[string]any) error {
	for i, raw := range actions {
		actionType, _ := raw["type"].(string)
		if actionType == "" {
			return fmt.Errorf("action %d has no type", i+1)
		}
		cfg, _ := raw["config"].(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
		}
		if err := crmactions.Validate(actionType, crmactions.Config(cfg)); err != nil {
			return fmt.Errorf("action %d (%s): %w", i+1, actionType, err)
		}
	}
	return nil
}

// keywordActionsJSONB wraps an action list for storage.
func keywordActionsJSONB(actions []map[string]any) models.JSONB {
	if len(actions) == 0 {
		return models.JSONB{}
	}
	list := make([]any, 0, len(actions))
	for _, a := range actions {
		list = append(list, a)
	}
	return models.JSONB{"list": list}
}

// applyAPIFieldMapping writes an api_call response onto the contact's custom
// fields (plan 10, S7).
//
// Config: {"field_mapping": {"<field_key>": "<json.path>"}}
//
// Separate from response_mapping because the destinations differ. A mapping
// into SessionData is scratch state that dies with the conversation; a field is
// the customer's record, which is what a later segment, report or campaign
// reads. Putting a field key inside response_mapping would store it flat under
// that literal name and never reach the contact.
func (a *App) applyAPIFieldMapping(node *ChatNode, ctx *chatNodeCtx, respBody []byte) {
	raw, ok := node.Config["field_mapping"].(map[string]any)
	if !ok || len(raw) == 0 {
		return
	}

	var decoded map[string]any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		a.Log.Warn("field_mapping: response is not JSON", "node", node.ID)
		return
	}

	paths := make(map[string]string, len(raw))
	for fieldKey, path := range raw {
		if p, ok := path.(string); ok && p != "" {
			paths[fieldKey] = p
		}
	}
	extracted := templating.ExtractResponseMapping(decoded, paths)
	if len(extracted) == 0 {
		return
	}

	// A path that resolved to nothing must not blank a field the customer
	// already has: an API that omits a key is not saying "delete this".
	values := make(map[string]any, len(extracted))
	for key, value := range extracted {
		if value == nil || value == "" {
			continue
		}
		values[key] = value
	}
	if len(values) == 0 {
		return
	}

	tx := a.DB.WithContext(context.Background())
	if _, err := customfields.New(a.DB).SetValues(tx, ctx.account.OrganizationID,
		ctx.contact.ID, models.FieldEntityContact, values, nil); err != nil {
		a.Log.Error("field_mapping: failed to write contact fields",
			"node", node.ID, "contact", ctx.contact.ID, "error", err)
	}
}

// syncPanelFieldsToContact copies session variables the flow's panel config
// marks with save_to_field onto the contact record (plan 10, S7).
//
// A panel field is session data by default: it describes one conversation and
// goes away with it. Some of those values are facts about the customer — their
// company, their account number — and an author should be able to say so on the
// field itself rather than adding a CRM-action node whose only job is copying a
// variable across.
//
// Run at flow completion, when the variables have their final values.
func (a *App) syncPanelFieldsToContact(flow *models.ChatbotFlow, ctx *chatNodeCtx) {
	if flow == nil || len(flow.PanelConfig) == 0 {
		return
	}

	sections, ok := flow.PanelConfig["sections"].([]any)
	if !ok || len(sections) == 0 {
		return
	}

	values := map[string]any{}
	for _, rawSection := range sections {
		section, ok := rawSection.(map[string]any)
		if !ok {
			continue
		}
		fields, ok := section["fields"].([]any)
		if !ok {
			continue
		}
		for _, rawField := range fields {
			field, ok := rawField.(map[string]any)
			if !ok {
				continue
			}
			target, _ := field["save_to_field"].(string)
			key, _ := field["key"].(string)
			if target == "" || key == "" {
				continue
			}
			value, present := ctx.session.SessionData[key]
			if !present || value == nil || value == "" {
				continue
			}
			values[target] = value
		}
	}
	if len(values) == 0 {
		return
	}

	tx := a.DB.WithContext(context.Background())
	if _, err := customfields.New(a.DB).SetValues(tx, ctx.account.OrganizationID,
		ctx.contact.ID, models.FieldEntityContact, values, nil); err != nil {
		a.Log.Error("panel save_to_field failed", "flow", flow.ID,
			"contact", ctx.contact.ID, "error", err)
	}
}
