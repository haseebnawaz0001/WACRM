package crmactions

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	register(addTags{})
	register(removeTags{})
	register(setField{})
	register(setContactOwner{})
}

// --- Tags ---

type addTags struct{}

func (addTags) Type() string { return TypeAddTags }

func (addTags) Validate(cfg Config) error {
	if len(cfg.Strings("tags")) == 0 {
		return fmt.Errorf("crmactions: add_tags needs at least one tag")
	}
	return nil
}

func (addTags) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	contact, err := loadContact(ctx, d, rc)
	if err != nil {
		return nil, err
	}

	current := tagStrings(contact.Tags)
	added := missingTags(current, cfg.Strings("tags"))
	if len(added) == 0 {
		// Already tagged is a success, not a failure: a rule that runs twice
		// should not start reporting errors on the second pass.
		return map[string]any{"added": []string{}}, nil
	}
	if rc.DryRun {
		return map[string]any{"added": added}, nil
	}

	err = d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An unknown tag is created rather than refused: the alternative is a
		// rule that fails at 3am because nobody had used that word yet.
		if err := ensureTags(tx, rc.OrgID, added); err != nil {
			return err
		}

		if err := tx.Model(&models.Contact{}).Where("id = ?", contact.ID).
			Update("tags", tagArray(append(append([]string{}, current...), added...))).Error; err != nil {
			return err
		}

		for _, tag := range added {
			event := crmevents.New(rc.OrgID, "contact.tag_added", rc.Actor, map[string]any{"tag": tag}).
				ForContact(contact.ID)
			event.Origin = rc.Origin
			if err := crmevents.PublishTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, Retryable{err}
	}
	return map[string]any{"added": added}, nil
}

type removeTags struct{}

func (removeTags) Type() string { return TypeRemoveTags }

func (removeTags) Validate(cfg Config) error {
	if len(cfg.Strings("tags")) == 0 {
		return fmt.Errorf("crmactions: remove_tags needs at least one tag")
	}
	return nil
}

func (removeTags) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	contact, err := loadContact(ctx, d, rc)
	if err != nil {
		return nil, err
	}

	drop := map[string]bool{}
	for _, tag := range cfg.Strings("tags") {
		drop[strings.ToLower(tag)] = true
	}

	current := tagStrings(contact.Tags)
	kept := make([]string, 0, len(current))
	removed := make([]string, 0, len(current))
	for _, tag := range current {
		if drop[strings.ToLower(tag)] {
			removed = append(removed, tag)
			continue
		}
		kept = append(kept, tag)
	}
	if len(removed) == 0 || rc.DryRun {
		return map[string]any{"removed": removed}, nil
	}

	err = d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Contact{}).Where("id = ?", contact.ID).
			Update("tags", tagArray(kept)).Error; err != nil {
			return err
		}
		for _, tag := range removed {
			event := crmevents.New(rc.OrgID, "contact.tag_removed", rc.Actor, map[string]any{"tag": tag}).
				ForContact(contact.ID)
			event.Origin = rc.Origin
			if err := crmevents.PublishTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, Retryable{err}
	}
	return map[string]any{"removed": removed}, nil
}

// tagStrings reads a contact's tag column, which is stored as a loose JSON
// array and so arrives as []any.
func tagStrings(raw models.JSONBArray) []string {
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// tagArray converts back for storage.
func tagArray(tags []string) models.JSONBArray {
	out := make(models.JSONBArray, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tag)
	}
	return out
}

// missingTags returns the wanted tags the contact does not already carry,
// compared case-insensitively so "vip" does not become a second "VIP".
func missingTags(current []string, wanted []string) []string {
	have := make(map[string]bool, len(current))
	for _, tag := range current {
		have[strings.ToLower(tag)] = true
	}

	out := make([]string, 0, len(wanted))
	for _, tag := range wanted {
		key := strings.ToLower(tag)
		if have[key] {
			continue
		}
		have[key] = true
		out = append(out, tag)
	}
	return out
}

// ensureTags creates any tag the organization has not seen before.
func ensureTags(tx *gorm.DB, orgID uuid.UUID, names []string) error {
	rows := make([]models.Tag, 0, len(names))
	for _, name := range names {
		rows = append(rows, models.Tag{OrganizationID: orgID, Name: name, Color: "gray"})
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

// --- Fields ---

type setField struct{}

func (setField) Type() string { return TypeSetField }

func (setField) Validate(cfg Config) error {
	if cfg.Str("field") == "" {
		return fmt.Errorf("crmactions: set_field needs a field key")
	}
	if _, present := cfg["value"]; !present {
		return fmt.Errorf("crmactions: set_field needs a value")
	}
	return nil
}

func (setField) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	key := cfg.Str("field")

	value := cfg["value"]
	if text, isText := value.(string); isText {
		value = render(text, rc.Vars)
	}

	if rc.DryRun {
		return map[string]any{"field": key, "value": value}, nil
	}

	var changed []string
	err := d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var setErr error
		changed, setErr = customfields.New(d.DB).SetValues(tx, rc.OrgID, rc.ContactID,
			models.FieldEntityContact, map[string]any{key: value}, rc.Actor.ID)
		if setErr != nil {
			return setErr
		}
		for _, field := range changed {
			event := crmevents.New(rc.OrgID, "contact.field_changed", rc.Actor, map[string]any{
				"field": field, "to": value,
			}).ForContact(rc.ContactID)
			event.Origin = rc.Origin
			if err := crmevents.PublishTx(tx, event); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// A value the field rejects is the rule author's mistake, and no
		// amount of retrying will make "banana" a valid date.
		return nil, Permanent{err}
	}
	return map[string]any{"field": key, "value": value, "changed": changed}, nil
}

// --- Owner ---

type setContactOwner struct{}

func (setContactOwner) Type() string { return TypeSetContactOwner }

func (setContactOwner) Validate(cfg Config) error {
	switch cfg.Str("mode") {
	case "user":
		if _, ok := cfg.UUID("user_id"); !ok {
			return fmt.Errorf("crmactions: set_contact_owner in user mode needs a user_id")
		}
	case "conversation_assignee", "unassign":
	default:
		return fmt.Errorf("crmactions: set_contact_owner needs mode user, conversation_assignee or unassign")
	}
	return nil
}

func (setContactOwner) Execute(ctx context.Context, d Deps, rc RunContext, cfg Config) (map[string]any, error) {
	var owner *uuid.UUID

	switch cfg.Str("mode") {
	case "user":
		id, _ := cfg.UUID("user_id")
		owner = &id
	case "conversation_assignee":
		var conversation models.Conversation
		err := d.DB.WithContext(ctx).
			Where("organization_id = ? AND contact_id = ? AND status <> ?",
				rc.OrgID, rc.ContactID, models.ConversationResolved).
			Order("created_at DESC").First(&conversation).Error
		if err != nil || conversation.AssigneeID == nil {
			return nil, Permanent{fmt.Errorf("crmactions: no conversation assignee to copy")}
		}
		owner = conversation.AssigneeID
	}

	if owner != nil {
		// Assigning to somebody who has left is the same as assigning to
		// nobody, except harder to notice.
		var active int64
		if err := d.DB.WithContext(ctx).Model(&models.User{}).
			Where("id = ? AND organization_id = ? AND is_active = true", *owner, rc.OrgID).
			Count(&active).Error; err != nil {
			return nil, Retryable{err}
		}
		if active == 0 {
			return nil, Permanent{fmt.Errorf("crmactions: that owner is not an active user")}
		}
	}

	if rc.DryRun {
		return map[string]any{"owner_id": owner}, nil
	}

	err := d.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Contact{}).Where("id = ? AND organization_id = ?", rc.ContactID, rc.OrgID).
			Update("assigned_user_id", owner).Error; err != nil {
			return err
		}
		data := map[string]any{}
		if owner != nil {
			data["to"] = owner.String()
		}
		event := crmevents.New(rc.OrgID, "contact.assigned", rc.Actor, data).ForContact(rc.ContactID)
		event.Origin = rc.Origin
		return crmevents.PublishTx(tx, event)
	})
	if err != nil {
		return nil, Retryable{err}
	}
	return map[string]any{"owner_id": owner}, nil
}
