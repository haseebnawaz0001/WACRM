package handlers

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// ReorderContactFields sets the order fields appear in, in one request.
//
// Position was editable one field at a time, which means a list of twelve
// fields takes twelve requests to reorder and lands in a half-applied state if
// one of them fails. The order arrives whole and is written in a transaction.
func (a *App) ReorderContactFields(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContactFields, models.ActionWrite)
	if err != nil {
		return err
	}

	var req struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}
	if len(req.IDs) == 0 {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "ids is required", nil, "")
	}

	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		for i, id := range req.IDs {
			// Scoped to the organization, so an id from elsewhere reorders
			// nothing rather than rearranging a stranger's settings.
			if err := tx.Model(&models.CustomFieldDefinition{}).
				Where("id = ? AND organization_id = ? AND entity_type = ?",
					id, orgID, models.FieldEntityContact).
				Updates(map[string]any{"position": (i + 1) * 10, "updated_by_id": userID}).
				Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		a.Log.Error("Failed to reorder contact fields", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to reorder fields", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, uuid.Nil,
		models.AuditActionUpdated, nil, map[string]any{"order": req.IDs})
	return r.SendEnvelope(map[string]any{"message": "Fields reordered"})
}

// PromoteMetadataRequest names a metadata key and the field to make from it.
type PromoteMetadataRequest struct {
	MetadataKey string `json:"metadata_key"`
	Field       struct {
		Key        string `json:"key"`
		Label      string `json:"label"`
		Type       string `json:"type"`
		GroupLabel string `json:"group_label"`
		ShowInList bool   `json:"show_in_list"`
	} `json:"field"`
}

// PromoteMetadata turns a contact metadata key into a real custom field
// (plan 01).
//
// Before fields existed, integrations wrote whatever they knew into the
// contact's metadata blob: no type, no validation, nothing to filter or report
// on. Those keys are real data an organization has been collecting, sometimes
// for years, and the only way to make them usable was to create a field and
// re-enter every value by hand — so in practice nobody did, and the blob kept
// growing.
//
// This creates the field and copies the values across in one transaction. The
// metadata is left alone: an integration is probably still writing to it, and
// deleting what it wrote would break the thing that produced the data.
func (a *App) PromoteMetadata(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceContactFields, models.ActionWrite)
	if err != nil {
		return err
	}

	var req PromoteMetadataRequest
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	req.MetadataKey = strings.TrimSpace(req.MetadataKey)
	if req.MetadataKey == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "metadata_key is required", nil, "")
	}
	if req.Field.Key == "" {
		req.Field.Key = slugifyKey(req.MetadataKey, 64)
	}
	if req.Field.Label == "" {
		req.Field.Label = req.MetadataKey
	}
	if req.Field.Type == "" {
		// Text holds anything the blob was holding. Narrowing the type is a
		// later edit the org can make once it can see the values in a column.
		req.Field.Type = models.FieldTypeText
	}

	def := &models.CustomFieldDefinition{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		EntityType:      models.FieldEntityContact,
		Key:             req.Field.Key,
		Label:           req.Field.Label,
		Type:            req.Field.Type,
		GroupLabel:      req.Field.GroupLabel,
		ShowInList:      req.Field.ShowInList,
		ShowInChatPanel: true,
		Options:         models.JSONBArray{},
		Validation:      models.JSONB{},
		CreatedByID:     &userID,
		UpdatedByID:     &userID,
	}
	if err := customfields.ValidateDefinition(def); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}

	var existing int64
	if err := a.DB.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			orgID, models.FieldEntityContact, def.Key).
		Count(&existing).Error; err == nil && existing > 0 {
		return r.SendErrorEnvelope(fasthttp.StatusConflict, "A field with this key already exists", nil, "")
	}

	// Which contacts hold the key, and what they hold. Read before the write
	// so a value that will not coerce is reported rather than silently
	// dropped: "promoted 400 of 900" needs to say which 500 and why.
	var rows []struct {
		ID    uuid.UUID
		Value string
	}
	// Presence is tested by extracting the value rather than with jsonb's ?
	// operator, which collides with the driver's own placeholder.
	if err := a.DB.Model(&models.Contact{}).
		Select("id, metadata ->> ? AS value", req.MetadataKey).
		Where("organization_id = ? AND deleted_at IS NULL", orgID).
		Where("metadata ->> ? IS NOT NULL", req.MetadataKey).
		Scan(&rows).Error; err != nil {
		a.Log.Error("Failed to read metadata values", "error", err, "key", req.MetadataKey)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to read metadata", nil, "")
	}

	promoted, skipped := 0, 0
	if err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(def).Error; err != nil {
			return err
		}
		svc := customfields.New(tx)
		for _, row := range rows {
			if strings.TrimSpace(row.Value) == "" {
				skipped++
				continue
			}
			if _, err := svc.SetValues(tx, orgID, row.ID, models.FieldEntityContact,
				map[string]any{def.Key: row.Value}, &userID); err != nil {
				// One unparseable value must not lose the whole promotion.
				skipped++
				continue
			}
			promoted++
		}
		return nil
	}); err != nil {
		a.Log.Error("Failed to promote metadata", "error", err, "key", req.MetadataKey)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to promote metadata", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceContactFields, def.ID, models.AuditActionCreated, nil,
		map[string]any{"promoted_from": req.MetadataKey, "promoted": promoted, "skipped": skipped})

	return r.SendEnvelope(map[string]any{
		"field":    toContactFieldResponse(*def),
		"promoted": promoted,
		"skipped":  skipped,
	})
}

// contactMetadataKeys is used by the settings UI to offer what can be promoted.
func (a *App) ListContactMetadataKeys(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceContactFields, models.ActionRead)
	if err != nil {
		return err
	}

	var keys []string
	// Sampled rather than scanned: the point is to offer the keys an
	// organization actually uses, and a full scan of every contact's blob to
	// populate a dropdown is not worth it.
	if err := a.DB.Raw(`
		SELECT DISTINCT key FROM (
			SELECT jsonb_object_keys(metadata) AS key
			FROM contacts
			WHERE organization_id = ? AND deleted_at IS NULL
			  AND metadata IS NOT NULL AND metadata <> '{}'::jsonb
			LIMIT 5000
		) AS sampled
		ORDER BY key`, orgID).Scan(&keys).Error; err != nil {
		a.Log.Error("Failed to list metadata keys", "error", err, "org_id", orgID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to load metadata keys", nil, "")
	}

	return r.SendEnvelope(map[string]any{"keys": keys})
}
