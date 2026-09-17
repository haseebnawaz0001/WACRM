package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// ContactFieldResponse is the API shape of a field definition.
type ContactFieldResponse struct {
	ID              string         `json:"id"`
	Key             string         `json:"key"`
	Label           string         `json:"label"`
	Description     string         `json:"description"`
	Type            string         `json:"type"`
	Options         []any          `json:"options"`
	Validation      map[string]any `json:"validation"`
	IsSystem        bool           `json:"is_system"`
	IsRequired      bool           `json:"is_required"`
	ShowInList      bool           `json:"show_in_list"`
	ShowInChatPanel bool           `json:"show_in_chat_panel"`
	GroupLabel      string         `json:"group_label"`
	Position        int            `json:"position"`
	ArchivedAt      *time.Time     `json:"archived_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func toContactFieldResponse(d models.CustomFieldDefinition) ContactFieldResponse {
	return ContactFieldResponse{
		ID:              d.ID.String(),
		Key:             d.Key,
		Label:           d.Label,
		Description:     d.Description,
		Type:            d.Type,
		Options:         d.Options,
		Validation:      d.Validation,
		IsSystem:        d.IsSystem,
		IsRequired:      d.IsRequired,
		ShowInList:      d.ShowInList,
		ShowInChatPanel: d.ShowInChatPanel,
		GroupLabel:      d.GroupLabel,
		Position:        d.Position,
		ArchivedAt:      d.ArchivedAt,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}
}

// contactFieldRequest is the create/update body.
type contactFieldRequest struct {
	Key             string         `json:"key"`
	Label           string         `json:"label"`
	Description     string         `json:"description"`
	Type            string         `json:"type"`
	Options         []any          `json:"options"`
	Validation      map[string]any `json:"validation"`
	IsRequired      *bool          `json:"is_required"`
	ShowInList      *bool          `json:"show_in_list"`
	ShowInChatPanel *bool          `json:"show_in_chat_panel"`
	GroupLabel      string         `json:"group_label"`
	Position        *int           `json:"position"`
	Archived        *bool          `json:"archived"`
	// OptionRenames says that an option kept its meaning under a new name.
	// Without it a rename is indistinguishable from removing one option and
	// adding another, and everything already set to the old value would be
	// orphaned rather than moved.
	OptionRenames []optionRename `json:"option_renames"`
}

// optionRename is one dropdown option keeping its meaning under a new name.
type optionRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ListContactFields returns the organization's contact field definitions.
func (a *App) ListContactFields(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceContactFields, models.ActionRead)
	if err != nil {
		return err
	}

	defs, err := customfields.New(a.DB).Definitions(context.Background(), orgID, models.FieldEntityContact)
	if err != nil {
		a.Log.Error("Failed to list contact fields", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load contact fields", nil, "")
	}

	out := make([]ContactFieldResponse, 0, len(defs))
	for _, d := range defs {
		out = append(out, toContactFieldResponse(d))
	}
	return r.SendEnvelope(map[string]any{"fields": out})
}

// CreateContactField defines a new field for the organization.
func (a *App) CreateContactField(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContactFields, models.ActionWrite)
	if err != nil {
		return err
	}

	var req contactFieldRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	def := &models.CustomFieldDefinition{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		EntityType:      models.FieldEntityContact,
		Key:             req.Key,
		Label:           req.Label,
		Description:     req.Description,
		Type:            req.Type,
		Options:         models.JSONBArray(req.Options),
		Validation:      models.JSONB(req.Validation),
		GroupLabel:      req.GroupLabel,
		ShowInChatPanel: true,
		CreatedByID:     &userID,
		UpdatedByID:     &userID,
	}
	applyFieldFlags(def, req)
	if def.Options == nil {
		def.Options = models.JSONBArray{}
	}
	if def.Validation == nil {
		def.Validation = models.JSONB{}
	}

	if err := customfields.ValidateDefinition(def); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	// The unique index is the real guard against a duplicate key; checking
	// first only produces a friendlier message for the common case.
	var existing int64
	if err := a.DB.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			orgID, models.FieldEntityContact, def.Key).
		Count(&existing).Error; err == nil && existing > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "A field with this key already exists", nil, "")
	}

	if err := a.DB.Create(def).Error; err != nil {
		a.Log.Error("Failed to create contact field", "error", err, "key", def.Key)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to create contact field", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, def.ID, models.AuditActionCreated, nil, contactFieldAuditSnapshot(def))
	return r.SendEnvelope(map[string]any{"field": toContactFieldResponse(*def)})
}

// UpdateContactField edits a field definition.
//
// The key and the type are immutable. A key rename would silently break every
// segment, automation and message template that refers to it, and a type change
// would leave existing values in the wrong column with no safe conversion.
func (a *App) UpdateContactField(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContactFields, models.ActionWrite)
	if err != nil {
		return err
	}

	id, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid field id", nil, "")
	}

	var def models.CustomFieldDefinition
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&def).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact field not found", nil, "")
	}

	before := contactFieldAuditSnapshot(&def)

	var req contactFieldRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if req.Key != "" && req.Key != def.Key {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"A field key cannot be changed; filters and templates refer to it", nil, "")
	}
	if req.Type != "" && req.Type != def.Type {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"A field type cannot be changed; existing values could not be converted safely", nil, "")
	}

	if req.Label != "" {
		def.Label = req.Label
	}
	def.Description = req.Description
	def.GroupLabel = req.GroupLabel
	// A dropdown option is stored by value in segment filters and automation
	// configs (plan 10, S8). Removing one leaves those matching nothing and
	// saying nothing, so the removals are reported rather than silent, and a
	// caller that means it passes ?force=1.
	renames := claimedRenames(req.OptionRenames, def.Options, req.Options)
	if req.Options != nil {
		removed := renamedAside(removedOptions(def.Options, req.Options), renames)
		if len(removed) > 0 && !boolParam(r, "force") {
			if blocked, msg := a.optionsStillInUse(orgID, removed); blocked {
				return r.SendErrorEnvelope(fasthttp.StatusConflict, msg,
					map[string]any{"removed_options": removed}, "")
			}
		}
		def.Options = models.JSONBArray(req.Options)
	}
	if req.Validation != nil {
		def.Validation = models.JSONB(req.Validation)
	}
	applyFieldFlags(&def, req)
	def.UpdatedByID = &userID

	if err := customfields.ValidateDefinition(&def); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}
	// The definition and everything set to the old option move together: a
	// half-applied rename leaves contacts holding a value the field no longer
	// offers, which reads as "empty" everywhere but is not.
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&def).Error; err != nil {
			return err
		}
		return applyOptionRenames(tx, orgID, def, renames)
	}); err != nil {
		a.Log.Error("Failed to update contact field", "error", err, "field_id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update contact field", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, def.ID, models.AuditActionUpdated, before, contactFieldAuditSnapshot(&def))
	return r.SendEnvelope(map[string]any{
		"field":           toContactFieldResponse(def),
		"options_renamed": len(renames),
	})
}

// applyFieldFlags copies the optional booleans and position from a request.
func applyFieldFlags(def *models.CustomFieldDefinition, req contactFieldRequest) {
	if req.IsRequired != nil {
		def.IsRequired = *req.IsRequired
	}
	if req.ShowInList != nil {
		def.ShowInList = *req.ShowInList
	}
	if req.ShowInChatPanel != nil {
		def.ShowInChatPanel = *req.ShowInChatPanel
	}
	if req.Position != nil {
		def.Position = *req.Position
	}
	if req.Archived != nil {
		if *req.Archived {
			now := time.Now().UTC()
			def.ArchivedAt = &now
		} else {
			def.ArchivedAt = nil
		}
	}
}

// DeleteContactField removes a field definition and its values.
//
// System fields cannot be deleted: product features refer to them by key, so
// removing one breaks those rather than just tidying a form. Archiving is
// offered instead, which hides the field while keeping historical values
// readable.
func (a *App) DeleteContactField(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContactFields, models.ActionDelete)
	if err != nil {
		return err
	}

	id, err := uuid.Parse(r.RequestCtx.UserValue("id").(string))
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid field id", nil, "")
	}

	var def models.CustomFieldDefinition
	if err := a.DB.Where("id = ? AND organization_id = ?", id, orgID).First(&def).Error; err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Contact field not found", nil, "")
	}
	if def.IsSystem {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"Built-in fields cannot be deleted. Archive the field instead to hide it while keeping its values.", nil, "")
	}

	// Values go with the definition, in one transaction: leaving them behind
	// would be rows nothing can interpret.
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("organization_id = ? AND field_id = ?", orgID, def.ID).
			Delete(&models.CustomFieldValue{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.CustomFieldDefinition{}, "id = ?", def.ID).Error
	}); err != nil {
		a.Log.Error("Failed to delete contact field", "error", err, "field_id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to delete contact field", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, def.ID, models.AuditActionDeleted, contactFieldAuditSnapshot(&def), nil)
	return r.SendEnvelope(map[string]any{"message": "Contact field deleted"})
}

// contactFieldAuditSnapshot captures the parts of a definition worth diffing in
// the audit log. Values are not included: they belong to contacts, not to the
// definition, and would make every audit entry unbounded.
func contactFieldAuditSnapshot(d *models.CustomFieldDefinition) map[string]any {
	return map[string]any{
		"key":                d.Key,
		"label":              d.Label,
		"type":               d.Type,
		"options":            d.Options,
		"validation":         d.Validation,
		"is_required":        d.IsRequired,
		"show_in_list":       d.ShowInList,
		"show_in_chat_panel": d.ShowInChatPanel,
		"group_label":        d.GroupLabel,
		"position":           d.Position,
		"archived":           d.IsArchived(),
	}
}

// optionValue reads the stored value out of one dropdown option.
//
// An option is an object — {"value": "bronze", "label": "Bronze"} — because the
// text on screen has to be changeable without rewriting every record set to it.
// Reading one as a bare string finds nothing, silently: the guard below would
// never fire and a removal would look safe every time.
func optionValue(raw any) (string, bool) {
	opt, ok := raw.(map[string]any)
	if !ok {
		// Tolerated so a definition stored before options became objects is
		// still readable.
		value, isString := raw.(string)
		return value, isString
	}
	value, isString := opt["value"].(string)
	return value, isString
}

// optionValues lists the values in an option list.
func optionValues(options []any) map[string]bool {
	out := make(map[string]bool, len(options))
	for _, raw := range options {
		if value, ok := optionValue(raw); ok {
			out[value] = true
		}
	}
	return out
}

// removedOptions lists the option values present before an edit and absent
// after it.
func removedOptions(before models.JSONBArray, after []any) []string {
	remaining := optionValues(after)

	var removed []string
	for _, raw := range before {
		value, ok := optionValue(raw)
		if ok && !remaining[value] {
			removed = append(removed, value)
		}
	}
	return removed
}

// optionsStillInUse reports whether any removed option is named by a saved
// filter or rule, and what to tell the person.
func (a *App) optionsStillInUse(orgID uuid.UUID, removed []string) (bool, string) {
	for _, option := range removed {
		dependents, err := entityrefs.FindDependents(a.DB, orgID, option)
		if err != nil {
			a.Log.Error("Failed to check option references", "error", err, "option", option)
			// A failed check must not block an edit: the administrator is in
			// front of the screen and the alternative is an error they cannot
			// act on.
			return false, ""
		}
		if len(dependents) > 0 {
			return true, "The option \"" + option + "\" is still used by " +
				entityrefs.DescribeDependents(dependents)
		}
	}
	return false, ""
}

// claimedRenames keeps the renames that the edit actually supports: the old
// value has to be an option today and the new one has to be an option after the
// edit. A rename naming something that was never there would rewrite stored
// values to a value the field does not offer.
func claimedRenames(claimed []optionRename, before models.JSONBArray, after []any) []optionRename {
	if len(claimed) == 0 {
		return nil
	}

	existed := optionValues(before)
	// after is nil when the edit does not touch the options at all, in which
	// case the current list is what the rename has to land in.
	offered := existed
	if after != nil {
		offered = optionValues(after)
	}

	out := make([]optionRename, 0, len(claimed))
	for _, rename := range claimed {
		if rename.From == rename.To || rename.From == "" || rename.To == "" {
			continue
		}
		if existed[rename.From] && offered[rename.To] {
			out = append(out, rename)
		}
	}
	return out
}

// renamedAside drops the values that left the option list only because they
// were renamed. They are not removals and must not be reported as breaking
// anything — the rewrite is about to follow them.
func renamedAside(removed []string, renames []optionRename) []string {
	if len(renames) == 0 {
		return removed
	}
	moved := make(map[string]bool, len(renames))
	for _, rename := range renames {
		moved[rename.From] = true
	}

	out := make([]string, 0, len(removed))
	for _, value := range removed {
		if !moved[value] {
			out = append(out, value)
		}
	}
	return out
}

// applyOptionRenames moves the stored values and the saved filters and rules
// that name them.
//
// The config rewrite is confined to rows that also mention this field's key
// (plan 10, S8): an option value is a bare word, and rewriting every rule that
// happens to contain it would corrupt configs that have nothing to do with this
// field.
func applyOptionRenames(tx *gorm.DB, orgID uuid.UUID, def models.CustomFieldDefinition, renames []optionRename) error {
	for _, rename := range renames {
		if err := tx.Model(&models.CustomFieldValue{}).
			Where("organization_id = ? AND field_id = ? AND value_option = ?", orgID, def.ID, rename.From).
			Update("value_option", rename.To).Error; err != nil {
			return fmt.Errorf("rename option values: %w", err)
		}
		if err := entityrefs.RewriteConfigValueWithin(
			tx, orgID, rename.From, rename.To, "field."+def.Key); err != nil {
			return err
		}
	}
	return nil
}
