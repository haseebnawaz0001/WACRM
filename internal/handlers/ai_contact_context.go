package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
)

// The contact-profile AI context (plan 10, 4.3).
//
// The chatbot could answer from a page of static text and from an external API,
// but not from the record of the person it was talking to — so it asked
// customers for things the CRM already knew, and could not answer "what did I
// order" without an integration written for the purpose.
//
// Two rules shape this:
//
//   - It is opt-in. An organization adds a "Contact profile" context
//     deliberately; nothing starts sending customer records to a language model
//     because the feature shipped.
//
//   - Custom fields are allowlisted, one by one. A CRM holds things nobody
//     would send to a third party — a national id, a medical note, an internal
//     credit score — and a default of "all fields" would send them the first
//     time somebody enabled this. `fields: ["*"]` is accepted for an
//     organization that means it, and has to be typed.
//
// Everything rendered is a fact the customer already knows about themselves,
// which is the test applied to each section below.

// contactProfileConfig is what an organization chose to expose.
type contactProfileConfig struct {
	// Fields are custom field keys, or ["*"] for every live field.
	Fields []string
	// Sections that can be switched off. All default to on, because a
	// contact-profile context that shows nothing is a context nobody wanted.
	IncludeTags         bool
	IncludeLifecycle    bool
	IncludeOpenTasks    bool
	IncludeOpenDeals    bool
	IncludeConversation bool
}

// readContactProfileConfig reads the context's api_config, which is where the
// existing shape already keeps per-context settings.
func readContactProfileConfig(raw models.JSONB) contactProfileConfig {
	cfg := contactProfileConfig{
		IncludeTags:         true,
		IncludeLifecycle:    true,
		IncludeOpenTasks:    true,
		IncludeOpenDeals:    true,
		IncludeConversation: true,
	}
	if raw == nil {
		return cfg
	}

	if list, ok := raw["fields"].([]any); ok {
		for _, entry := range list {
			if key, ok := entry.(string); ok && strings.TrimSpace(key) != "" {
				cfg.Fields = append(cfg.Fields, strings.TrimSpace(key))
			}
		}
	}

	// Absent means on; only an explicit false turns a section off.
	for key, target := range map[string]*bool{
		"include_tags":         &cfg.IncludeTags,
		"include_lifecycle":    &cfg.IncludeLifecycle,
		"include_open_tasks":   &cfg.IncludeOpenTasks,
		"include_open_deals":   &cfg.IncludeOpenDeals,
		"include_conversation": &cfg.IncludeConversation,
	} {
		if value, ok := raw[key].(bool); ok {
			*target = value
		}
	}
	return cfg
}

// allowsEveryField reports the explicit opt-out of the allowlist.
func (c contactProfileConfig) allowsEveryField() bool {
	for _, key := range c.Fields {
		if key == "*" {
			return true
		}
	}
	return false
}

// buildContactProfileContext renders what the CRM knows about one contact.
//
// Returns empty when there is nothing to say; the caller drops empty sections
// rather than sending the model a heading with nothing under it.
func (a *App) buildContactProfileContext(orgID uuid.UUID, session *models.ChatbotSession,
	raw models.JSONB) string {

	if session == nil || session.ContactID == uuid.Nil {
		return ""
	}
	cfg := readContactProfileConfig(raw)

	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", session.ContactID, orgID).
		First(&contact).Error; err != nil {
		return ""
	}

	var lines []string
	if name := strings.TrimSpace(contact.ProfileName); name != "" {
		lines = append(lines, "Name: "+name)
	}

	if cfg.IncludeTags {
		if tags := contactTagList(contact.Tags); len(tags) > 0 {
			lines = append(lines, "Tags: "+strings.Join(tags, ", "))
		}
	}

	lines = append(lines, a.contactProfileFields(orgID, contact.ID, cfg)...)
	if cfg.IncludeOpenTasks {
		lines = append(lines, a.contactProfileTasks(orgID, contact.ID)...)
	}
	if cfg.IncludeOpenDeals {
		lines = append(lines, a.contactProfileDeals(orgID, contact.ID)...)
	}
	if cfg.IncludeConversation {
		lines = append(lines, a.contactProfileConversation(orgID, contact.ID)...)
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// contactTagList reads the JSONB tag array.
func contactTagList(raw models.JSONBArray) []string {
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		if tag, ok := entry.(string); ok && tag != "" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// contactProfileFields renders the allowlisted custom fields.
//
// The lifecycle stage is a custom field like any other, so it is included or
// excluded by the same switch rather than by a special case that could disagree
// with the allowlist.
func (a *App) contactProfileFields(orgID, contactID uuid.UUID, cfg contactProfileConfig) []string {
	if len(cfg.Fields) == 0 {
		return nil
	}

	svc := customfields.New(a.DB)
	defs, err := svc.DefinitionsByKey(context.Background(), orgID, models.FieldEntityContact)
	if err != nil {
		return nil
	}
	values, err := svc.Values(context.Background(), orgID, contactID, models.FieldEntityContact)
	if err != nil {
		return nil
	}

	allowed := map[string]bool{}
	for _, key := range cfg.Fields {
		allowed[key] = true
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []string
	for _, key := range keys {
		if !cfg.allowsEveryField() && !allowed[key] {
			continue
		}
		if key == models.FieldKeyLifecycleStage && !cfg.IncludeLifecycle {
			continue
		}
		def, known := defs[key]
		if !known || def.IsArchived() {
			continue
		}
		text := fmt.Sprint(values[key])
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, def.Label+": "+text)
	}
	return out
}

// contactProfileTasks lists what we owe this contact.
func (a *App) contactProfileTasks(orgID, contactID uuid.UUID) []string {
	var rows []models.Task
	if err := a.DB.Where("organization_id = ? AND contact_id = ? AND status = ?",
		orgID, contactID, models.TaskOpen).
		Order("due_at").Limit(5).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil
	}

	out := []string{"Open follow-ups:"}
	for _, task := range rows {
		out = append(out, fmt.Sprintf("- %s (due %s)", task.Title, task.DueAt.Format("2 January 2006")))
	}
	return out
}

// contactProfileDeals lists the open deals.
func (a *App) contactProfileDeals(orgID, contactID uuid.UUID) []string {
	var rows []models.Deal
	if err := a.DB.Where("organization_id = ? AND contact_id = ? AND status = ?",
		orgID, contactID, models.DealOpen).
		Order("created_at DESC").Limit(5).Find(&rows).Error; err != nil || len(rows) == 0 {
		return nil
	}

	out := []string{"Open deals:"}
	for _, deal := range rows {
		out = append(out, "- "+deal.Title)
	}
	return out
}

// contactProfileConversation says how long this has been going on, which is
// what tells the model whether it is mid-thread or opening one.
func (a *App) contactProfileConversation(orgID, contactID uuid.UUID) []string {
	var conv models.Conversation
	if err := a.DB.Where("organization_id = ? AND contact_id = ? AND status <> ?",
		orgID, contactID, models.ConversationResolved).
		Order("opened_at DESC").First(&conv).Error; err != nil {
		return nil
	}

	line := "Conversation: " + string(conv.Status)
	if !conv.OpenedAt.IsZero() {
		line += fmt.Sprintf(", open since %s", conv.OpenedAt.Format("2 January 2006"))
		if days := int(time.Since(conv.OpenedAt).Hours() / 24); days >= 1 {
			line += fmt.Sprintf(" (%d days)", days)
		}
	}
	return []string{line}
}

// BuildContactProfileContextForTest exposes the builder to the handler tests,
// which live in package handlers_test.
func (a *App) BuildContactProfileContextForTest(orgID uuid.UUID, session *models.ChatbotSession,
	raw models.JSONB) string {
	return a.buildContactProfileContext(orgID, session, raw)
}
