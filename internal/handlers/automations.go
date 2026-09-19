package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Automations returns the rule service, wired to this organization's filter
// fields so a condition on a custom field is recognised rather than rejected.
func (a *App) Automations() *automation.Service {
	svc := automation.New(a.DB)
	svc.BuildRegistry = func(_ context.Context, orgID uuid.UUID) (*contactquery.Registry, error) {
		return a.contactRegistry(orgID)
	}
	return svc
}

// AutomationEngine builds the engine used for dry runs and, in the server, for
// the stream consumer.
func (a *App) AutomationEngine() *automation.Engine {
	return &automation.Engine{
		Rules: a.Automations(),
		Deps: crmactions.Deps{
			DB: a.DB,
			// The SSRF-safe client: a webhook action takes a URL from a rule
			// somebody wrote, so the plain client would happily fetch the
			// cloud metadata endpoint on their behalf.
			HTTPClient: a.HTTPClient,
			Messenger:  automationMessenger{app: a},
			Assigner:   automationAssigner{app: a},
		},
		Redis:    a.Redis,
		Log:      a.Log,
		Location: a.OrgLocation,
	}
}

// AutomationResponse is the API shape of a rule.
type AutomationResponse struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Description   string                   `json:"description"`
	Enabled       bool                     `json:"enabled"`
	TriggerType   string                   `json:"trigger_type"`
	TriggerConfig map[string]any           `json:"trigger_config"`
	ContactFilter *contactquery.Filter     `json:"contact_filter,omitempty"`
	Actions       []automation.ActionSpec  `json:"actions"`
	RunPolicy     automation.RunPolicy     `json:"run_policy"`
	LastRunAt     *time.Time               `json:"last_run_at,omitempty"`
	RunCount      int64                    `json:"run_count"`
	ErrorCount    int64                    `json:"error_count"`
	CreatedAt     time.Time                `json:"created_at"`
	Stats         *automationStatsResponse `json:"stats,omitempty"`
	// Problems is what stops the rule from running, pinned to steps. A draft
	// may have some; a rule that is on has none.
	Problems []automation.StepProblem `json:"problems"`
	// Waiting counts the contacts parked at each wait step right now.
	Waiting map[string]int64 `json:"waiting,omitempty"`
}

type automationStatsResponse struct {
	Runs24h     int64 `json:"runs_24h"`
	Failures24h int64 `json:"failures_24h"`
}

func toAutomationResponse(rule models.AutomationRule) AutomationResponse {
	out := AutomationResponse{
		ID:            rule.ID.String(),
		Name:          rule.Name,
		Description:   rule.Description,
		Enabled:       rule.Enabled,
		TriggerType:   rule.TriggerType,
		TriggerConfig: map[string]any(rule.TriggerConfig),
		Actions:       automation.RuleActions(&rule),
		RunPolicy:     automation.RulePolicy(&rule),
		LastRunAt:     rule.LastRunAt,
		RunCount:      rule.RunCount,
		ErrorCount:    rule.ErrorCount,
		CreatedAt:     rule.CreatedAt,
	}
	if filter, ok := automation.RuleFilter(&rule); ok {
		out.ContactFilter = &filter
	}
	if out.Actions == nil {
		out.Actions = []automation.ActionSpec{}
	}
	out.Problems = []automation.StepProblem{}
	return out
}

// automationDetail is the response for one rule: what it is, what still stops
// it from running, and who is waiting inside it.
func (a *App) automationDetail(rule *models.AutomationRule) AutomationResponse {
	out := toAutomationResponse(*rule)
	out.Problems = a.Automations().ProblemsFor(context.Background(), rule)
	waiting, err := a.AutomationEngine().WaitingCounts(context.Background(), rule.ID)
	if err != nil {
		a.Log.Error("Failed to count waiting contacts", "error", err, "rule_id", rule.ID)
	}
	out.Waiting = waiting
	return out
}

// sendAutomationError answers a failed save or switch-on. A problem pinned to
// a step comes back with its id, so the builder can mark the card it belongs
// to instead of showing a sentence nobody can place.
func sendAutomationError(r *fastglue.Request, err error) error {
	var stepErr *automation.StepError
	if errors.As(err, &stepErr) {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, stepErr.Message,
			map[string]any{"step_id": stepErr.StepID}, "")
	}
	return r.SendErrorEnvelope(fasthttp.StatusBadRequest, strings.TrimPrefix(err.Error(), "automation: "), nil, "")
}

type automationRequest struct {
	Name          string                  `json:"name"`
	Description   string                  `json:"description"`
	Enabled       *bool                   `json:"enabled"`
	TriggerType   string                  `json:"trigger_type"`
	TriggerConfig map[string]any          `json:"trigger_config"`
	ContactFilter *contactquery.Filter    `json:"contact_filter"`
	Actions       []automation.ActionSpec `json:"actions"`
	RunPolicy     *automation.RunPolicy   `json:"run_policy"`
}

func (req automationRequest) input(actorID uuid.UUID) automation.Input {
	return automation.Input{
		Name:          req.Name,
		Description:   req.Description,
		Enabled:       req.Enabled,
		TriggerType:   req.TriggerType,
		TriggerConfig: req.TriggerConfig,
		ContactFilter: req.ContactFilter,
		Actions:       req.Actions,
		RunPolicy:     req.RunPolicy,
		ActorID:       &actorID,
	}
}

// ListAutomations returns the organization's rules with their recent activity.
func (a *App) ListAutomations(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAutomations, models.ActionRead)
	if err != nil {
		return err
	}

	rules, err := a.Automations().List(context.Background(), orgID)
	if err != nil {
		a.Log.Error("Failed to list automations", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load automations", nil, "")
	}

	stats, err := a.automationStats(orgID)
	if err != nil {
		a.Log.Error("Failed to load automation stats", "error", err, "org_id", orgID)
	}

	svc := a.Automations()
	items := make([]AutomationResponse, 0, len(rules))
	for i, rule := range rules {
		item := toAutomationResponse(rule)
		// The list says which rules are unfinished, so a draft left half-built
		// is visible from the list rather than discovered by opening it.
		item.Problems = svc.ProblemsFor(context.Background(), &rules[i])
		if stat, ok := stats[rule.ID]; ok {
			item.Stats = stat
		}
		items = append(items, item)
	}
	return r.SendEnvelope(map[string]any{"automations": items})
}

// automationStats counts the last day's runs per rule in one query, so a list
// of fifty rules is not fifty round trips.
func (a *App) automationStats(orgID uuid.UUID) (map[uuid.UUID]*automationStatsResponse, error) {
	type row struct {
		RuleID   uuid.UUID
		Runs     int64
		Failures int64
	}
	var rows []row

	err := a.DB.Model(&models.AutomationRun{}).
		Select(`rule_id,
			count(*) AS runs,
			count(*) FILTER (WHERE status IN ('failed', 'partially_failed')) AS failures`).
		Where("organization_id = ? AND dry_run = false AND started_at > ?",
			orgID, time.Now().UTC().Add(-24*time.Hour)).
		Group("rule_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]*automationStatsResponse, len(rows))
	for _, r := range rows {
		out[r.RuleID] = &automationStatsResponse{Runs24h: r.Runs, Failures24h: r.Failures}
	}
	return out, nil
}

// CreateAutomation stores a new rule.
func (a *App) CreateAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionWrite)
	if err != nil {
		return err
	}

	var req automationRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	rule, err := a.Automations().Create(context.Background(), orgID, req.input(userID))
	if err != nil {
		return sendAutomationError(r, err)
	}

	a.logAudit(orgID, userID, models.ResourceAutomations, rule.ID, models.AuditActionCreated, nil,
		map[string]any{"name": rule.Name, "trigger": rule.TriggerType})
	return r.SendEnvelope(map[string]any{"automation": a.automationDetail(rule)})
}

// GetAutomation returns one rule.
func (a *App) GetAutomation(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAutomations, models.ActionRead)
	if err != nil {
		return err
	}
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	rule, err := a.Automations().Get(context.Background(), orgID, ruleID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Automation not found", nil, "")
	}
	return r.SendEnvelope(map[string]any{"automation": a.automationDetail(rule)})
}

// UpdateAutomation edits a rule.
func (a *App) UpdateAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionWrite)
	if err != nil {
		return err
	}
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	var req automationRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	rule, err := a.Automations().Update(context.Background(), orgID, ruleID, req.input(userID))
	if err != nil {
		if errors.Is(err, automation.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Automation not found", nil, "")
		}
		return sendAutomationError(r, err)
	}

	a.logAudit(orgID, userID, models.ResourceAutomations, rule.ID, models.AuditActionUpdated, nil,
		map[string]any{"name": rule.Name, "trigger": rule.TriggerType})
	return r.SendEnvelope(map[string]any{"automation": a.automationDetail(rule)})
}

// DeleteAutomation removes a rule.
func (a *App) DeleteAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionDelete)
	if err != nil {
		return err
	}
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	if err := a.Automations().Delete(context.Background(), orgID, ruleID); err != nil {
		if errors.Is(err, automation.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Automation not found", nil, "")
		}
		a.Log.Error("Failed to delete automation", "error", err, "rule_id", ruleID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete automation", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceAutomations, ruleID, models.AuditActionDeleted, nil, nil)
	return r.SendEnvelope(map[string]any{"deleted": true})
}

// EnableAutomation turns a rule on.
func (a *App) EnableAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionWrite)
	if err != nil {
		return err
	}
	return a.setAutomationEnabled(r, orgID, userID, true)
}

// DisableAutomation turns a rule off.
func (a *App) DisableAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionWrite)
	if err != nil {
		return err
	}
	return a.setAutomationEnabled(r, orgID, userID, false)
}

func (a *App) setAutomationEnabled(r *fastglue.Request, orgID, userID uuid.UUID, enabled bool) error {
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	rule, err := a.Automations().SetEnabled(context.Background(), orgID, ruleID, enabled)
	if err != nil {
		// Every failure here used to come back as "Automation not found",
		// which is only one of the things that can go wrong.
		if errors.Is(err, automation.ErrNotFound) {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Automation not found", nil, "")
		}
		return sendAutomationError(r, err)
	}

	a.logAudit(orgID, userID, models.ResourceAutomations, ruleID, models.AuditActionUpdated, nil,
		map[string]any{"enabled": enabled})
	return r.SendEnvelope(map[string]any{"automation": a.automationDetail(rule)})
}

// TestAutomation dry-runs a rule against a real contact.
//
// This is what makes a rule safe to write: the author sees which conditions
// passed and what each action would do, without a customer finding out first.
func (a *App) TestAutomation(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAutomations, models.ActionWrite)
	if err != nil {
		return err
	}
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	var req struct {
		ContactID string         `json:"contact_id"`
		EventData map[string]any `json:"event_data"`
		// Rule, when given, is tested instead of the saved version. A rule
		// that is on holds its edits until they are saved, and "does my
		// change do what I think?" is exactly the question to answer before
		// saving it.
		Rule *automationRequest `json:"rule"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	contactID, err := uuid.Parse(req.ContactID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	rule, err := a.Automations().Get(context.Background(), orgID, ruleID)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Automation not found", nil, "")
	}
	// A contact the tester cannot open is not one they may run a rule
	// against: the result would describe that contact to them.
	if !a.canSeeContact(orgID, userID, contactID) {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Contact not found", nil, "")
	}
	if req.Rule != nil {
		draft := req.Rule.input(userID)
		if _, err := a.Automations().Problems(context.Background(), orgID, draft); err != nil {
			return sendAutomationError(r, err)
		}
		// Tested in memory under the saved rule's id, so the dry run lands
		// in this rule's history; nothing about the rule itself is written.
		rule = automation.DraftRule(rule, draft)
	}

	data := req.EventData
	if data == nil {
		data = map[string]any{}
	}
	sample := crmevents.New(orgID, rule.TriggerType, crmActorForUser(userID), data).
		ForContact(contactID)

	run, err := a.AutomationEngine().DryRun(context.Background(), rule, sample)
	if err != nil {
		a.Log.Error("Automation dry run failed", "error", err, "rule_id", ruleID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "The test could not be run", nil, "")
	}
	return r.SendEnvelope(map[string]any{"run": run})
}

// AutomationRuns returns a rule's run history, newest first.
func (a *App) AutomationRuns(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAutomations, models.ActionRead)
	if err != nil {
		return err
	}
	ruleID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid automation id", nil, "")
	}

	args := r.RequestCtx.QueryArgs()
	query := a.DB.Where("organization_id = ? AND rule_id = ?", orgID, ruleID)
	if status := strings.ToLower(string(args.Peek("status"))); status != "" {
		query = query.Where("status = ?", status)
	}
	if id, ok := optionalUUIDArg(args, "contact_id"); ok {
		query = query.Where("contact_id = ?", *id)
	}

	limit := 50
	if n, ok := optionalIntArg(args, "limit"); ok && n > 0 && n <= 200 {
		limit = n
	}

	var runs []models.AutomationRun
	if err := query.Order("started_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		a.Log.Error("Failed to load automation runs", "error", err, "rule_id", ruleID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load runs", nil, "")
	}
	return r.SendEnvelope(map[string]any{"runs": a.namedRuns(orgID, runs)})
}

// automationRunResponse is a run with the name of the contact it was for.
// History is read as "what happened to Amara", and a list of contact ids
// answers nobody.
type automationRunResponse struct {
	models.AutomationRun
	ContactName string `json:"contact_name,omitempty"`
}

// namedRuns attaches contact names in one query, masked like every other
// place a phone number stands in for a name.
func (a *App) namedRuns(orgID uuid.UUID, runs []models.AutomationRun) []automationRunResponse {
	ids := make([]uuid.UUID, 0, len(runs))
	for _, run := range runs {
		if run.ContactID != nil {
			ids = append(ids, *run.ContactID)
		}
	}
	names := map[uuid.UUID]string{}
	if len(ids) > 0 {
		var contacts []models.Contact
		if err := a.DB.Unscoped().Select("id", "profile_name", "phone_number").
			Where("organization_id = ? AND id IN ?", orgID, ids).Find(&contacts).Error; err == nil {
			for _, c := range contacts {
				name, phone := a.MaskContactFields(orgID, c.ProfileName, c.PhoneNumber)
				if name == "" {
					name = phone
				}
				names[c.ID] = name
			}
		}
	}
	out := make([]automationRunResponse, 0, len(runs))
	for _, run := range runs {
		item := automationRunResponse{AutomationRun: run}
		if run.ContactID != nil {
			item.ContactName = names[*run.ContactID]
		}
		out = append(out, item)
	}
	return out
}

// ContactAutomationRuns returns the runs that touched one contact, which is
// the answer to "why did this customer get that?".
func (a *App) ContactAutomationRuns(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAutomations, models.ActionRead)
	if err != nil {
		return err
	}
	contactID, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid contact id", nil, "")
	}

	var runs []models.AutomationRun
	if err := a.DB.Where("organization_id = ? AND contact_id = ?", orgID, contactID).
		Order("started_at DESC").Limit(50).Find(&runs).Error; err != nil {
		a.Log.Error("Failed to load contact automation runs", "error", err, "contact_id", contactID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load runs", nil, "")
	}
	return r.SendEnvelope(map[string]any{"runs": runs})
}

// AutomationCatalog describes the triggers and actions the builder may offer.
//
// It is served rather than hard-coded in the UI so a build of the frontend
// cannot offer a trigger this backend does not have.
func (a *App) AutomationCatalog(r *fastglue.Request) error {
	_, _, err := a.requireAuth(r, models.ResourceAutomations, models.ActionRead)
	if err != nil {
		return err
	}
	return r.SendEnvelope(map[string]any{
		"triggers": automation.Triggers(),
		"actions":  automation.Actions(),
		// Flow control: a question that splits the path, and a pause.
		"steps": automation.FlowSteps(),
		"limits": map[string]any{
			"max_steps_per_rule": automation.MaxStepsPerRule,
			"max_nesting":        automation.MaxNesting,
			"max_wait_days":      int(automation.MaxWait.Hours() / 24),
			"max_rules_per_org":  automation.MaxRulesPerOrg,
			"max_depth":          automation.MaxDepth,
		},
	})
}
