// Package segments stores and evaluates saved contact filters (plan 05).
//
// Audiences get described repeatedly — the same "customers in London who have
// not replied in 30 days" for a campaign this month, a report next week, a
// follow-up after that. Saving the filter rather than the resulting list is
// what keeps the audience correct as contacts change; a static list is out of
// date the moment it is written.
package segments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// ErrNotFound is returned when a segment does not exist or is not visible.
var ErrNotFound = errors.New("segments: not found")

// ErrInUse is returned when deleting a segment other segments reference.
var ErrInUse = errors.New("segments: referenced by another segment")

// FieldKey is how one segment references another inside a filter.
const FieldKey = "segment"

// MaxNesting caps how deep segment references may go. A segment referencing a
// segment referencing a segment is already hard to reason about; deeper than
// that is a filter nobody can predict.
const MaxNesting = 3

// Service stores and evaluates segments.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// SaveInput describes a segment to create or update.
type SaveInput struct {
	OrgID       uuid.UUID
	ID          *uuid.UUID
	Name        string
	Description string
	Filter      contactquery.Filter
	Visibility  string
	ActorID     uuid.UUID
}

// Save creates or updates a segment.
//
// The filter is validated against the registry before it is stored, so a
// segment can never hold a filter that fails when someone later runs it —
// which would otherwise surface as a broken campaign rather than a bad save.
func (s *Service) Save(ctx context.Context, r *contactquery.Registry, in SaveInput) (*models.Segment, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("segments: a segment needs a name")
	}
	if err := contactquery.Validate(r, in.Filter); err != nil {
		return nil, err
	}
	if err := s.checkReferences(ctx, in.OrgID, in.ID, in.Filter, 1); err != nil {
		return nil, err
	}

	raw, err := json.Marshal(in.Filter)
	if err != nil {
		return nil, err
	}
	var filter models.JSONB
	if err := json.Unmarshal(raw, &filter); err != nil {
		return nil, err
	}

	visibility := in.Visibility
	if visibility != models.SegmentPrivate {
		visibility = models.SegmentShared
	}

	if in.ID != nil {
		updates := map[string]any{
			"name":          name,
			"description":   in.Description,
			"filter":        filter,
			"visibility":    visibility,
			"updated_by_id": in.ActorID,
			// The cached count describes the old filter, so it is cleared
			// rather than left to mislead.
			"contact_count": nil,
			"counted_at":    nil,
		}
		if err := s.DB.WithContext(ctx).Model(&models.Segment{}).
			Where("id = ? AND organization_id = ?", *in.ID, in.OrgID).
			Updates(updates).Error; err != nil {
			return nil, err
		}
		return s.Get(ctx, in.OrgID, *in.ID)
	}

	segment := &models.Segment{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: in.OrgID,
		Name:           name,
		Description:    in.Description,
		Filter:         filter,
		Visibility:     visibility,
		CreatedByID:    in.ActorID,
	}
	if err := s.DB.WithContext(ctx).Create(segment).Error; err != nil {
		return nil, err
	}
	return segment, nil
}

// checkReferences rejects cycles and over-deep nesting.
//
// A segment that references itself, directly or through a chain, would recurse
// forever when compiled. Catching it on save turns an infinite query into a
// clear error at the moment someone creates it.
func (s *Service) checkReferences(ctx context.Context, orgID uuid.UUID, selfID *uuid.UUID, filter contactquery.Filter, depth int) error {
	if depth > MaxNesting {
		return fmt.Errorf("segments: segments are nested more than %d deep", MaxNesting)
	}

	for _, ref := range referencedIDs(filter) {
		if selfID != nil && ref == *selfID {
			return fmt.Errorf("segments: a segment cannot reference itself")
		}

		var referenced models.Segment
		if err := s.DB.WithContext(ctx).
			Where("id = ? AND organization_id = ?", ref, orgID).
			First(&referenced).Error; err != nil {
			return fmt.Errorf("segments: referenced segment %s does not exist", ref)
		}

		inner, err := parseFilter(referenced.Filter)
		if err != nil {
			return err
		}
		if err := s.checkReferences(ctx, orgID, selfID, inner, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// referencedIDs collects the segment ids a filter refers to.
func referencedIDs(filter contactquery.Filter) []uuid.UUID {
	var out []uuid.UUID

	var walk func(contactquery.Node)
	walk = func(n contactquery.Node) {
		if n.IsGroup() {
			for _, child := range n.Rules {
				walk(child)
			}
			return
		}
		if n.Field != FieldKey {
			return
		}
		if raw, ok := n.Value.(string); ok {
			if id, err := uuid.Parse(raw); err == nil {
				out = append(out, id)
			}
		}
	}
	walk(filter)
	return out
}

func parseFilter(raw models.JSONB) (contactquery.Filter, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return contactquery.Filter{}, err
	}
	return contactquery.ParseFilter(encoded)
}

// Get returns one segment.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (*models.Segment, error) {
	var segment models.Segment
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", id, orgID).
		First(&segment).Error; err != nil {
		return nil, ErrNotFound
	}
	return &segment, nil
}

// List returns the segments a viewer may see.
//
// Private segments belong to their creator: a half-finished audience someone is
// still shaping should not appear in everyone else's list.
func (s *Service) List(ctx context.Context, orgID, viewerID uuid.UUID) ([]models.Segment, error) {
	var out []models.Segment
	err := s.DB.WithContext(ctx).
		Where("organization_id = ?", orgID).
		Where("visibility = ? OR created_by_id = ?", models.SegmentShared, viewerID).
		Order("name").Find(&out).Error
	return out, err
}

// Delete removes a segment, refusing while another references it.
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	dependents, err := s.dependents(ctx, orgID, id)
	if err != nil {
		return err
	}
	if len(dependents) > 0 {
		// Deleting it would leave those segments compiling a filter that
		// points at nothing, so the block names them instead.
		return fmt.Errorf("%w: %s", ErrInUse, strings.Join(dependents, ", "))
	}

	return s.DB.WithContext(ctx).
		Where("id = ? AND organization_id = ?", id, orgID).
		Delete(&models.Segment{}).Error
}

// dependents returns the names of segments referencing this one.
func (s *Service) dependents(ctx context.Context, orgID, id uuid.UUID) ([]string, error) {
	var all []models.Segment
	if err := s.DB.WithContext(ctx).
		Where("organization_id = ? AND id <> ?", orgID, id).Find(&all).Error; err != nil {
		return nil, err
	}

	var names []string
	for _, segment := range all {
		filter, err := parseFilter(segment.Filter)
		if err != nil {
			continue
		}
		for _, ref := range referencedIDs(filter) {
			if ref == id {
				names = append(names, segment.Name)
				break
			}
		}
	}
	return names, nil
}

// Apply adds a segment's filter to a contacts query, expanding any nested
// segment references.
func (s *Service) Apply(ctx context.Context, db *gorm.DB, r *contactquery.Registry, v contactquery.Viewer, segmentID uuid.UUID) (*gorm.DB, error) {
	segment, err := s.Get(ctx, v.OrgID, segmentID)
	if err != nil {
		return nil, err
	}

	filter, err := s.expand(ctx, v.OrgID, segment, 1)
	if err != nil {
		return nil, err
	}
	return contactquery.Apply(db, r, v, filter)
}

// expand replaces segment references with the referenced filters, so the whole
// thing compiles as one query rather than a list of ids fetched in stages.
func (s *Service) expand(ctx context.Context, orgID uuid.UUID, segment *models.Segment, depth int) (contactquery.Filter, error) {
	if depth > MaxNesting {
		return contactquery.Filter{}, fmt.Errorf("segments: nested more than %d deep", MaxNesting)
	}

	filter, err := parseFilter(segment.Filter)
	if err != nil {
		return contactquery.Filter{}, err
	}
	return s.expandNode(ctx, orgID, filter, depth)
}

func (s *Service) expandNode(ctx context.Context, orgID uuid.UUID, node contactquery.Node, depth int) (contactquery.Node, error) {
	if node.IsGroup() {
		rules := make([]contactquery.Node, 0, len(node.Rules))
		for _, child := range node.Rules {
			expanded, err := s.expandNode(ctx, orgID, child, depth)
			if err != nil {
				return contactquery.Node{}, err
			}
			rules = append(rules, expanded)
		}
		node.Rules = rules
		return node, nil
	}

	if node.Field != FieldKey {
		return node, nil
	}

	raw, _ := node.Value.(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return contactquery.Node{}, fmt.Errorf("segments: %q is not a segment id", raw)
	}

	referenced, err := s.Get(ctx, orgID, id)
	if err != nil {
		return contactquery.Node{}, err
	}
	inner, err := s.expand(ctx, orgID, referenced, depth+1)
	if err != nil {
		return contactquery.Node{}, err
	}

	// "not in this segment" is the inverse, which the filter language cannot
	// express directly; wrapping is left for when a NOT group exists.
	if node.Operator == contactquery.OpNotIn {
		return contactquery.Node{}, fmt.Errorf("segments: not_in is not supported yet")
	}
	return inner, nil
}

// Count refreshes a segment's cached contact count.
func (s *Service) Count(ctx context.Context, r *contactquery.Registry, v contactquery.Viewer, segmentID uuid.UUID) (int64, error) {
	query, err := s.Apply(ctx, s.DB.WithContext(ctx).Model(&models.Contact{}), r, v, segmentID)
	if err != nil {
		return 0, err
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	total := int(count)
	if err := s.DB.WithContext(ctx).Model(&models.Segment{}).
		Where("id = ? AND organization_id = ?", segmentID, v.OrgID).
		Updates(map[string]any{"contact_count": total, "counted_at": now}).Error; err != nil {
		return count, err
	}
	return count, nil
}

// MarkUsed records that a segment was used, so unused audiences are visible.
func (s *Service) MarkUsed(ctx context.Context, orgID, segmentID uuid.UUID) {
	s.DB.WithContext(ctx).Model(&models.Segment{}).
		Where("id = ? AND organization_id = ?", segmentID, orgID).
		Update("last_used_at", time.Now().UTC())
}

// RegisterField adds the `segment` field to a query registry (plan 05).
//
// The builder offers it like any other field, but it compiles differently:
// Apply expands a segment reference into the referenced filter before the
// query is compiled, so the Build below should never run. It returns an error
// rather than silently matching everything, which is what a forgotten
// expansion would otherwise look like.
func RegisterField(r *contactquery.Registry, available []models.Segment) {
	options := make([]contactquery.Option, 0, len(available))
	for _, segment := range available {
		options = append(options, contactquery.Option{
			Value: segment.ID.String(),
			Label: segment.Name,
		})
	}

	r.Register(contactquery.Field{
		Key:      FieldKey,
		LabelKey: "segments.segment",
		Type:     contactquery.TypeSegment,
		Options:  options,
		Build: func(contactquery.Field, contactquery.Node, contactquery.Viewer) (string, []any, error) {
			return "", nil, fmt.Errorf("segments: reference was not expanded before compiling")
		},
	})
}
