package crmactions

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"gorm.io/gorm"
)

func init() {
	register(setConversationStatus{})
	register(assignConversation{})
	register(createTask{})
	register(addNote{})
	register(notifyUsers{})
	register(createDeal{})
	register(moveDealStage{})
}

// --- Conversation ---

type setConversationStatus struct{}

func (setConversationStatus) Type() string { return TypeSetConversationStatus }

func (setConversationStatus) Validate(cfg Config) error {
	switch cfg.Str("status") {
	case string(models.ConversationResolved), string(models.ConversationOpen):
		return nil
	case string(models.ConversationSnoozed):
		if _, ok := cfg.Duration("snooze_for"); !ok {
			return fmt.Errorf("crmactions: snoozing needs a duration")
		}
		return nil
	}
	return fmt.Errorf("crmactions: %q is not a status an automation can set", cfg.Str("status"))
}

func (setConversationStatus) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	status := cfg.Str("status")
	if rc.DryRun {
		return map[string]any{"status": status}, nil
	}

	svc := conversation.New(d.DB)
	var (
		result *models.Conversation
		err    error
	)

	switch status {
	case string(models.ConversationResolved):
		result, err = svc.Resolve(ctx, rc.OrgID, rc.ContactID, models.ResolutionAutomation, rc.Actor)
	case string(models.ConversationSnoozed):
		wait, _ := cfg.Duration("snooze_for")
		result, err = svc.Snooze(ctx, rc.OrgID, rc.ContactID, time.Now().UTC().Add(wait), rc.Actor)
	default:
		return nil, Permanent{fmt.Errorf("crmactions: %q is not a status an automation can set", status)}
	}

	if err != nil {
		// No active conversation is a fact about this contact, not a transient
		// failure; retrying every minute would never find one.
		return nil, Permanent{err}
	}
	return map[string]any{"status": string(result.Status), "conversation_id": result.ID.String()}, nil
}

type assignConversation struct{}

func (assignConversation) Type() string { return TypeAssignConversation }

func (assignConversation) Validate(cfg Config) error {
	switch cfg.Str("mode") {
	case "user":
		if _, ok := cfg.UUID("user_id"); !ok {
			return fmt.Errorf("crmactions: assign_conversation in user mode needs a user_id")
		}
	case "team":
		if _, ok := cfg.UUID("team_id"); !ok {
			return fmt.Errorf("crmactions: assign_conversation in team mode needs a team_id")
		}
	case "unassign":
	default:
		return fmt.Errorf("crmactions: assign_conversation needs mode user, team or unassign")
	}
	return nil
}

func (assignConversation) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	mode := cfg.Str("mode")

	if mode == "team" {
		teamID, _ := cfg.UUID("team_id")
		if rc.DryRun {
			return map[string]any{"mode": mode, "team_id": teamID.String()}, nil
		}
		if d.Assigner == nil {
			return nil, Permanent{ErrUnavailable}
		}
		// A team assignment is a queue entry, not a column write: it has to go
		// through the transfer path so the strategy and business hours apply.
		outcome, err := d.Assigner.AssignToTeam(ctx, rc.OrgID, rc.ContactID, teamID)
		if err != nil {
			return nil, Retryable{err}
		}
		return map[string]any{"mode": mode, "team_id": teamID.String(), "outcome": outcome}, nil
	}

	var assignee *uuid.UUID
	if mode == "user" {
		id, _ := cfg.UUID("user_id")
		assignee = &id
	}
	if rc.DryRun {
		return map[string]any{"mode": mode, "assignee_id": assignee}, nil
	}

	result, err := conversation.New(d.DB).Assign(ctx, rc.OrgID, rc.ContactID, assignee, nil, rc.Actor)
	if err != nil {
		return nil, Permanent{err}
	}
	return map[string]any{"mode": mode, "conversation_id": result.ID.String()}, nil
}

// --- Tasks ---

type createTask struct{}

func (createTask) Type() string { return TypeCreateTask }

func (createTask) Validate(cfg Config) error {
	if err := requireText(cfg, "title"); err != nil {
		return err
	}
	switch cfg.Object("owner").Str("mode") {
	case "contact_owner", "conversation_assignee", "rule_creator", "":
	case "user":
		if _, ok := cfg.Object("owner").UUID("user_id"); !ok {
			return fmt.Errorf("crmactions: create_task owner in user mode needs a user_id")
		}
	default:
		return fmt.Errorf("crmactions: unknown task owner mode")
	}
	return nil
}

func (createTask) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	title := render(cfg.Str("title"), rc.Vars)
	description := render(cfg.Str("description"), rc.Vars)

	in := tasks.CreateInput{
		OrgID:       rc.OrgID,
		ContactID:   rc.ContactID,
		TypeKey:     cfg.Str("type_key"),
		Title:       title,
		Description: description,
		Priority:    cfg.Str("priority"),
		AllDay:      cfg.Boolean("all_day"),
		Source:      models.TaskSourceAutomation,
		Location:    rc.Location,
	}
	if rc.Actor.Type == crmevents.ActorAutomation {
		in.AutomationRuleID = rc.Actor.ID
	} else {
		// A person triggering the action is the creator, which is the service's
		// last-resort owner when the contact has none.
		in.CreatedBy = rc.Actor.ID
	}
	if due, ok := cfg.Duration("due_in"); ok {
		at := time.Now().UTC().Add(due)
		in.DueAt = &at
	}

	owner := cfg.Object("owner")
	switch owner.Str("mode") {
	case "user":
		id, _ := owner.UUID("user_id")
		in.OwnerID = &id
	case "conversation_assignee":
		var row models.Conversation
		if err := d.DB.WithContext(ctx).
			Where("organization_id = ? AND contact_id = ? AND status <> ?",
				rc.OrgID, rc.ContactID, models.ConversationResolved).
			Order("created_at DESC").First(&row).Error; err == nil {
			in.OwnerID = row.AssigneeID
		}
	case "rule_creator":
		if creator, ok := cfg.Object("owner").UUID("fallback_user_id"); ok {
			in.OwnerID = &creator
		}
	}
	// An automation has no person to fall back to, so the rule's author is the
	// last resort. Without it, a rule acting on a contact with no owner would
	// fail every time.
	if in.CreatedBy == nil {
		if fallback, ok := owner.UUID("fallback_user_id"); ok {
			in.CreatedBy = &fallback
		} else {
			in.CreatedBy = rc.CreatorID
		}
	}
	// The service already falls back to the contact's owner and then the
	// creator, so "contact_owner" needs nothing set here.

	if rc.DryRun {
		return map[string]any{"title": title, "due_at": in.DueAt, "owner_id": in.OwnerID}, nil
	}

	task, err := tasks.New(d.DB).Create(ctx, in)
	if err != nil {
		// "No active owner available" is a configuration problem: nobody is
		// going to become active because we tried again.
		return nil, Permanent{err}
	}
	return map[string]any{"task_id": task.ID.String(), "title": task.Title, "due_at": task.DueAt}, nil
}

// --- Notes ---

type addNote struct{}

func (addNote) Type() string { return TypeAddNote }

func (addNote) Validate(cfg Config) error { return requireText(cfg, "content") }

func (addNote) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	content := render(cfg.Str("content"), rc.Vars)
	if rc.DryRun {
		return map[string]any{"content": content}, nil
	}

	// conversation_notes requires an author, and an automation is not a user.
	// Attributing the note to the rule's creator is honest — they wrote the
	// rule — and the rule name in the body says who typed it in practice.
	authorID, err := uuid.Parse(cfg.Object("owner").Str("fallback_user_id"))
	if err != nil {
		switch {
		case rc.CreatorID != nil:
			authorID = *rc.CreatorID
		case rc.Actor.Type == crmevents.ActorUser && rc.Actor.ID != nil:
			authorID = *rc.Actor.ID
		default:
			return nil, Permanent{fmt.Errorf("crmactions: a note needs an author")}
		}
	}

	body := content
	if rc.Actor.Type == crmevents.ActorAutomation && rc.Actor.Name != "" {
		body = fmt.Sprintf("Automation: %s\n%s", rc.Actor.Name, content)
	}

	note := models.ConversationNote{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: rc.OrgID,
		ContactID:      rc.ContactID,
		CreatedByID:    authorID,
		Content:        body,
	}
	if err := d.DB.WithContext(ctx).Create(&note).Error; err != nil {
		return nil, Retryable{err}
	}
	return map[string]any{"note_id": note.ID.String()}, nil
}

// --- Notifications ---

type notifyUsers struct{}

func (notifyUsers) Type() string { return TypeNotifyUsers }

func (notifyUsers) Validate(cfg Config) error {
	if err := requireText(cfg, "title"); err != nil {
		return err
	}
	recipients := cfg.Object("recipients")
	if len(recipients.Strings("user_ids")) == 0 &&
		len(recipients.Strings("roles")) == 0 &&
		!recipients.Boolean("contact_owner") &&
		!recipients.Boolean("conversation_assignee") {
		return fmt.Errorf("crmactions: notify_users needs at least one recipient")
	}
	return nil
}

func (a notifyUsers) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	recipients, err := a.resolve(ctx, d, rc, cfg.Object("recipients"))
	if err != nil {
		return nil, err
	}
	title := render(cfg.Str("title"), rc.Vars)
	body := render(cfg.Str("body"), rc.Vars)

	if rc.DryRun || len(recipients) == 0 {
		return map[string]any{"recipients": recipients, "title": title}, nil
	}

	contactID := rc.ContactID
	err = notify.New(d.DB).Send(ctx, notify.Input{
		OrgID:   rc.OrgID,
		UserIDs: recipients,
		Type:    models.NotificationAutomation,
		Title:   title,
		Body:    body,
		Entity:  notify.Entity{Type: "contact", ID: &contactID},
	})
	if err != nil {
		return nil, Retryable{err}
	}
	return map[string]any{"recipients": recipients, "title": title}, nil
}

func (notifyUsers) resolve(ctx context.Context, d Deps, rc RunContext, cfg Config) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(id *uuid.UUID) {
		if id == nil || seen[*id] {
			return
		}
		seen[*id] = true
		out = append(out, *id)
	}

	for _, raw := range cfg.Strings("user_ids") {
		if parsed, err := uuid.Parse(raw); err == nil {
			add(&parsed)
		}
	}

	if roles := cfg.Strings("roles"); len(roles) > 0 {
		var ids []uuid.UUID
		if err := d.DB.WithContext(ctx).Model(&models.User{}).
			Joins("JOIN custom_roles ON custom_roles.id = users.custom_role_id").
			Where("users.organization_id = ? AND users.is_active = true AND custom_roles.name IN ?",
				rc.OrgID, roles).
			Pluck("users.id", &ids).Error; err != nil {
			return nil, Retryable{err}
		}
		for i := range ids {
			add(&ids[i])
		}
	}

	if cfg.Boolean("contact_owner") {
		var contact models.Contact
		if err := d.DB.WithContext(ctx).Select("assigned_user_id").
			Where("id = ?", rc.ContactID).First(&contact).Error; err == nil {
			add(contact.AssignedUserID)
		}
	}

	if cfg.Boolean("conversation_assignee") {
		var row models.Conversation
		if err := d.DB.WithContext(ctx).
			Where("organization_id = ? AND contact_id = ? AND status <> ?",
				rc.OrgID, rc.ContactID, models.ConversationResolved).
			Order("created_at DESC").First(&row).Error; err == nil {
			add(row.AssigneeID)
		}
	}

	return out, nil
}

// --- Deals ---

type createDeal struct{}

func (createDeal) Type() string { return TypeCreateDeal }

func (createDeal) Validate(cfg Config) error { return requireText(cfg, "title") }

func (createDeal) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	title := render(cfg.Str("title"), rc.Vars)

	in := deals.CreateInput{
		OrgID:     rc.OrgID,
		ContactID: rc.ContactID,
		Title:     title,
		CreatedBy: rc.Actor.ID,
	}
	if value, ok := cfg.Number("value"); ok {
		in.Value = value
	}
	if id, ok := cfg.UUID("pipeline_id"); ok {
		in.PipelineID = &id
	}
	if id, ok := cfg.UUID("stage_id"); ok {
		in.StageID = &id
	}

	if rc.DryRun {
		return map[string]any{"title": title, "value": in.Value}, nil
	}

	deal, err := deals.New(d.DB).Create(ctx, in)
	if err != nil {
		return nil, Permanent{err}
	}
	return map[string]any{"deal_id": deal.ID.String(), "title": deal.Title}, nil
}

type moveDealStage struct{}

func (moveDealStage) Type() string { return TypeMoveDealStage }

func (moveDealStage) Validate(cfg Config) error {
	if _, ok := cfg.UUID("stage_id"); !ok {
		return fmt.Errorf("crmactions: move_deal_stage needs a stage_id")
	}
	return nil
}

func (moveDealStage) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	stageID, _ := cfg.UUID("stage_id")

	// The deal the triggering event was about, or the contact's most recent
	// open one: a rule on "tag added" has no deal of its own to move.
	dealID, err := resolveDeal(ctx, d, rc, cfg)
	if err != nil {
		return nil, err
	}

	if rc.DryRun {
		return map[string]any{"deal_id": dealID.String(), "stage_id": stageID.String()}, nil
	}

	moved, err := deals.New(d.DB).MoveStage(ctx, rc.OrgID, dealID, stageID, rc.Actor)
	if err != nil {
		return nil, Permanent{err}
	}
	return map[string]any{"deal_id": moved.ID.String(), "status": moved.Status}, nil
}

func resolveDeal(ctx context.Context, d Deps, rc RunContext, cfg Config) (uuid.UUID, error) {
	if id, ok := cfg.UUID("deal_id"); ok {
		return id, nil
	}
	if rc.Event.Subject.Type == crmevents.SubjectDeal && rc.Event.Subject.ID != nil {
		return *rc.Event.Subject.ID, nil
	}

	var deal models.Deal
	err := d.DB.WithContext(ctx).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			rc.OrgID, rc.ContactID, models.DealOpen).
		Order("stage_entered_at DESC").First(&deal).Error
	if err == gorm.ErrRecordNotFound {
		return uuid.Nil, Permanent{fmt.Errorf("crmactions: this contact has no open deal to move")}
	}
	if err != nil {
		return uuid.Nil, Retryable{err}
	}
	return deal.ID, nil
}
