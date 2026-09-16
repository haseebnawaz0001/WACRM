package handlers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
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
	if req.Options != nil {
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
	if err := a.DB.Save(&def).Error; err != nil {
		a.Log.Error("Failed to update contact field", "error", err, "field_id", id)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to update contact field", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, def.ID, models.AuditActionUpdated, before, contactFieldAuditSnapshot(&def))
	return r.SendEnvelope(map[string]any{"field": toContactFieldResponse(def)})
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
