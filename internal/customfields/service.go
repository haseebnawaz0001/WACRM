package customfields

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service reads definitions and reads/writes values.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Definitions returns an organization's field definitions in display order.
//
// Archived fields are included: their values still have to render on records
// that carry them, and hiding the definition would leave the value unlabelled.
func (s *Service) Definitions(ctx context.Context, orgID uuid.UUID, entityType string) ([]models.CustomFieldDefinition, error) {
	var out []models.CustomFieldDefinition
	err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND entity_type = ?", orgID, entityTypeOrContact(entityType)).
		Order("position, label").
		Find(&out).Error
	return out, err
}

// DefinitionsByKey indexes an organization's definitions by key.
func (s *Service) DefinitionsByKey(ctx context.Context, orgID uuid.UUID, entityType string) (map[string]models.CustomFieldDefinition, error) {
	defs, err := s.Definitions(ctx, orgID, entityType)
	if err != nil {
		return nil, err
	}
	out := make(map[string]models.CustomFieldDefinition, len(defs))
	for _, d := range defs {
		out[d.Key] = d
	}
	return out, nil
}

// Values returns one entity's field values keyed by field key.
func (s *Service) Values(ctx context.Context, orgID, entityID uuid.UUID, entityType string) (map[string]any, error) {
	byEntity, err := s.ValuesFor(ctx, orgID, []uuid.UUID{entityID}, entityType)
	if err != nil {
		return nil, err
	}
	if values, ok := byEntity[entityID]; ok {
		return values, nil
	}
	return map[string]any{}, nil
}

// ValuesFor returns field values for several entities at once.
//
// The list and chat panel both need values for a whole page of contacts.
// Fetching them per row is the N+1 that made the contacts list slow, so this
// takes the whole page in one query.
func (s *Service) ValuesFor(ctx context.Context, orgID uuid.UUID, entityIDs []uuid.UUID, entityType string) (map[uuid.UUID]map[string]any, error) {
	out := make(map[uuid.UUID]map[string]any, len(entityIDs))
	if len(entityIDs) == 0 {
		return out, nil
	}

	defs, err := s.Definitions(ctx, orgID, entityType)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]models.CustomFieldDefinition, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}

	var rows []models.CustomFieldValue
	if err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND entity_type = ? AND entity_id IN ?",
			orgID, entityTypeOrContact(entityType), entityIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		def, ok := byID[row.FieldID]
		if !ok {
			// A value whose definition is gone has nothing to render it.
			continue
		}
		if out[row.EntityID] == nil {
			out[row.EntityID] = map[string]any{}
		}
		out[row.EntityID][def.Key] = exportValue(def, row)
	}
	return out, nil
}

// exportValue renders a stored value in the shape the API returns.
func exportValue(def models.CustomFieldDefinition, row models.CustomFieldValue) any {
	switch def.Type {
	case models.FieldTypeNumber:
		if row.ValueNumber != nil {
			return *row.ValueNumber
		}
	case models.FieldTypeDate:
		if row.ValueDate != nil {
			return row.ValueDate.Format("2006-01-02")
		}
	case models.FieldTypeDropdown:
		if row.ValueOption != nil {
			return *row.ValueOption
		}
	default:
		if row.ValueText != nil {
			return *row.ValueText
		}
	}
	return nil
}

// SetValues validates and stores a set of field values for one entity.
//
// Values are written in the caller's transaction so they commit with whatever
// change prompted them. Unknown keys are rejected rather than ignored: silently
// dropping a field the caller believed they set is the kind of failure nobody
// notices until the data is missing.
//
// It returns the keys whose stored value actually changed, so the caller can
// record precise field-level activity rather than a blanket "contact updated".
func (s *Service) SetValues(tx *gorm.DB, orgID, entityID uuid.UUID, entityType string, values map[string]any, actorID *uuid.UUID) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	defs, err := s.DefinitionsByKey(tx.Statement.Context, orgID, entityType)
	if err != nil {
		return nil, err
	}

	var existing []models.CustomFieldValue
	if err := tx.Where("organization_id = ? AND entity_type = ? AND entity_id = ?",
		orgID, entityTypeOrContact(entityType), entityID).Find(&existing).Error; err != nil {
		return nil, err
	}
	current := make(map[uuid.UUID]models.CustomFieldValue, len(existing))
	for _, row := range existing {
		current[row.FieldID] = row
	}

	changed := make([]string, 0, len(values))
	for key, raw := range values {
		def, ok := defs[key]
		if !ok {
			return nil, reject(key, "no such field")
		}
		if def.IsArchived() {
			return nil, reject(key, "field is archived and cannot be edited")
		}

		coerced, err := Coerce(def, raw)
		if err != nil {
			return nil, err
		}

		prev, hadValue := current[def.ID]
		if hadValue && sameValue(def, prev, coerced) {
			continue
		}

		if coerced.IsEmpty() {
			if !hadValue {
				continue
			}
			// Clearing removes the row rather than storing four NULLs, so
			// "has a value" stays a simple existence check.
			if err := tx.Where("id = ?", prev.ID).Delete(&models.CustomFieldValue{}).Error; err != nil {
				return nil, err
			}
			changed = append(changed, key)
			continue
		}

		row := models.CustomFieldValue{
			ID:             uuid.New(),
			OrganizationID: orgID,
			EntityType:     entityTypeOrContact(entityType),
			EntityID:       entityID,
			FieldID:        def.ID,
			UpdatedByID:    actorID,
			UpdatedAt:      time.Now().UTC(),
		}
		coerced.Apply(&row)

		// One row per (entity, field): the upsert is what makes concurrent
		// edits from the chat panel and the profile page converge instead of
		// creating a second value.
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "entity_type"}, {Name: "entity_id"}, {Name: "field_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"value_text", "value_number", "value_date", "value_option",
				"updated_by_id", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			return nil, err
		}
		changed = append(changed, key)
	}

	return changed, nil
}

// sameValue reports whether a stored row already holds the coerced value.
func sameValue(def models.CustomFieldDefinition, row models.CustomFieldValue, v Value) bool {
	switch def.Type {
	case models.FieldTypeNumber:
		return equalFloat(row.ValueNumber, v.Number)
	case models.FieldTypeDate:
		return equalDate(row.ValueDate, v.Date)
	case models.FieldTypeDropdown:
		return equalString(row.ValueOption, v.Option)
	default:
		return equalString(row.ValueText, v.Text)
	}
}

func equalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Format("2006-01-02") == b.Format("2006-01-02")
}

func entityTypeOrContact(entityType string) string {
	if entityType == "" {
		return models.FieldEntityContact
	}
	return entityType
}
