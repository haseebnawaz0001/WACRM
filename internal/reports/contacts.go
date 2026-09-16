package reports

import (
	"context"
	"sort"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
)

// LifecycleField and SourceField are the built-in fields the CRM reports read.
// They are the keys seeded by plan 01; an organization that renames the label
// keeps the key, which is exactly why reports key on it.
const (
	LifecycleField = models.FieldKeyLifecycleStage
	SourceField    = models.FieldKeySource

	// CustomerStage is the lifecycle value that means "this became business".
	CustomerStage = "customer"
)

// UnknownBucket is where records with no value for a dimension are counted.
//
// They are shown rather than dropped: "40% of new contacts have no source" is
// itself the finding, and a chart that silently omits them looks healthier
// than the data is.
const UnknownBucket = "unknown"

// SourceRow summarises one contact source over the whole period.
type SourceRow struct {
	Source string `json:"source"`
	// Contacts is how many arrived from this source in the period.
	Contacts int64 `json:"contacts"`
	// Share is this source's percentage of the period's new contacts.
	Share float64 `json:"share"`
	// BecameCustomer is how many of them reached the customer stage while the
	// period was running — the number that says whether a source is worth
	// anything, rather than merely busy.
	BecameCustomer int64 `json:"became_customer"`
}

// ContactsBySource is R1: where new contacts come from, over time.
type ContactsBySource struct {
	Buckets []Bucket    `json:"buckets"`
	Totals  []SourceRow `json:"totals"`
	Total   int64       `json:"total"`
}

// ContactsBySource counts new contacts per interval, split by their source.
func (s *Service) ContactsBySource(ctx context.Context, v Viewer, r Range, splitField string) (*ContactsBySource, error) {
	field := splitField
	if field == "" {
		field = SourceField
	}

	bucketExpr, tz := r.bucket("contacts.created_at")

	// The source lives in a custom field value, so it is joined rather than
	// selected: a contact with no value still has to appear, under "unknown".
	type row struct {
		Period time.Time
		Series string
		Count  int64
	}
	var rows []row

	err := s.DB.WithContext(ctx).Model(&models.Contact{}).
		Select(bucketExpr+" AS period, COALESCE(NULLIF(cfv.value_option, ''), NULLIF(cfv.value_text, ''), ?) AS series, count(*) AS count",
			tz, UnknownBucket).
		Joins(`LEFT JOIN custom_field_definitions d
			ON d.organization_id = contacts.organization_id AND d.entity_type = ? AND d.key = ?`,
			models.FieldEntityContact, field).
		Joins(`LEFT JOIN custom_field_values cfv
			ON cfv.field_id = d.id AND cfv.entity_type = ? AND cfv.entity_id = contacts.id`,
			models.FieldEntityContact).
		Where("contacts.organization_id = ? AND contacts.deleted_at IS NULL", v.OrgID).
		Where("contacts.created_at >= ? AND contacts.created_at <= ?", r.From, r.To).
		Group("period, series").
		Order("period").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := &ContactsBySource{Buckets: make([]Bucket, 0, len(rows))}
	perSource := map[string]int64{}
	for _, item := range rows {
		out.Buckets = append(out.Buckets, Bucket{
			Period: item.Period.In(r.Location),
			Series: item.Series,
			Count:  item.Count,
		})
		perSource[item.Series] += item.Count
		out.Total += item.Count
	}

	converted, err := s.becameCustomerBySource(ctx, v, r, field)
	if err != nil {
		return nil, err
	}

	out.Totals = make([]SourceRow, 0, len(perSource))
	for source, count := range perSource {
		share := 0.0
		if out.Total > 0 {
			share = float64(count) / float64(out.Total) * 100
		}
		out.Totals = append(out.Totals, SourceRow{
			Source:         source,
			Contacts:       count,
			Share:          share,
			BecameCustomer: converted[source],
		})
	}
	// Largest first: a source table is read top-down to find where the volume
	// is, not alphabetically.
	sort.Slice(out.Totals, func(i, j int) bool {
		if out.Totals[i].Contacts != out.Totals[j].Contacts {
			return out.Totals[i].Contacts > out.Totals[j].Contacts
		}
		return out.Totals[i].Source < out.Totals[j].Source
	})
	return out, nil
}

// becameCustomerBySource counts, per source, the contacts that reached the
// customer lifecycle stage during the period.
func (s *Service) becameCustomerBySource(ctx context.Context, v Viewer, r Range, field string) (map[string]int64, error) {
	type row struct {
		Series string
		Count  int64
	}
	var rows []row

	err := s.DB.WithContext(ctx).Table("contact_activities AS a").
		Select("COALESCE(NULLIF(cfv.value_option, ''), NULLIF(cfv.value_text, ''), ?) AS series, count(DISTINCT a.contact_id) AS count",
			UnknownBucket).
		Joins(`LEFT JOIN custom_field_definitions d
			ON d.organization_id = a.organization_id AND d.entity_type = ? AND d.key = ?`,
			models.FieldEntityContact, field).
		Joins(`LEFT JOIN custom_field_values cfv
			ON cfv.field_id = d.id AND cfv.entity_type = ? AND cfv.entity_id = a.contact_id`,
			models.FieldEntityContact).
		Where("a.organization_id = ? AND a.type = ?", v.OrgID, "contact.field_changed").
		Where("a.occurred_at >= ? AND a.occurred_at <= ?", r.From, r.To).
		Where("a.data->>'field' = ? AND lower(a.data->>'to') = ?", LifecycleField, CustomerStage).
		Group("series").Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(rows))
	for _, item := range rows {
		out[item.Series] = item.Count
	}
	return out, nil
}
