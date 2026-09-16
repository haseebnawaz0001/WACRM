package crmevents

import (
	"context"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

// Relay defaults.
const (
	DefaultBatchSize = 500
	DefaultInterval  = time.Second
	DefaultRetention = 7 * 24 * time.Hour
	defaultPruneEvery = time.Hour
)

// Relay drains the outbox and fans each event out to its sinks.
//
// Several replicas may run a relay at once: rows are claimed with
// FOR UPDATE SKIP LOCKED, so a row one relay is working on is invisible to the
// others and no event is delivered twice. A leader lock would reduce the
// wasted polling, and plan 00's F4 scheduler adds one, but it is not needed
// for correctness.
type Relay struct {
	DB    *gorm.DB
	Log   logf.Logger
	Sinks []Sink

	// BatchSize caps how many events one pass claims.
	BatchSize int
	// Interval is how long to wait after an empty pass. A pass that fills
	// its batch continues immediately, so a backlog drains at full speed.
	Interval time.Duration
	// Retention is how long published rows are kept before pruning.
	Retention time.Duration
}

// Run drains the outbox until the context is cancelled.
func (r *Relay) Run(ctx context.Context) {
	r.applyDefaults()

	prune := time.NewTicker(defaultPruneEvery)
	defer prune.Stop()

	// Prune once at startup so a long downtime does not leave the table
	// holding weeks of published rows until the first tick.
	r.Prune(ctx)

	for {
		select {
		case <-ctx.Done():
			r.Log.Info("CRM event relay stopped")
			return
		case <-prune.C:
			r.Prune(ctx)
		default:
		}

		n, err := r.ProcessBatch(ctx)
		if err != nil {
			r.Log.Error("CRM event relay batch failed", "error", err)
		}

		// A full batch means there is probably more waiting; keep going
		// without sleeping. Anything less means the outbox is drained.
		if n >= r.BatchSize && err == nil {
			continue
		}

		select {
		case <-ctx.Done():
			r.Log.Info("CRM event relay stopped")
			return
		case <-time.After(r.Interval):
		}
	}
}

func (r *Relay) applyDefaults() {
	if r.BatchSize <= 0 {
		r.BatchSize = DefaultBatchSize
	}
	if r.Interval <= 0 {
		r.Interval = DefaultInterval
	}
	if r.Retention <= 0 {
		r.Retention = DefaultRetention
	}
}

// ProcessBatch claims one batch of unpublished events, fans them out and marks
// them published. It returns how many events were processed.
//
// The claim, the fan-out and the mark all happen in one transaction so a crash
// mid-batch rolls the claim back and the events are retried by the next pass.
// Sinks must therefore be fast; the webhook sink hands delivery off
// asynchronously rather than awaiting the remote endpoint.
func (r *Relay) ProcessBatch(ctx context.Context) (int, error) {
	r.applyDefaults()

	var claimed []models.CRMEventOutbox
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`
			SELECT * FROM crm_event_outbox
			WHERE published_at IS NULL
			ORDER BY occurred_at
			LIMIT ?
			FOR UPDATE SKIP LOCKED`, r.BatchSize).Scan(&claimed).Error; err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}

		ids := make([]string, 0, len(claimed))
		for i := range claimed {
			row := claimed[i]
			ids = append(ids, row.ID.String())

			event := eventFromRow(row)
			if sinkErr := r.fanOut(ctx, event); sinkErr != "" {
				// One failing sink must not stall the outbox behind a
				// single broken endpoint, so the row is still marked
				// published and the reason is recorded on it.
				if err := tx.Model(&models.CRMEventOutbox{}).
					Where("id = ?", row.ID).
					Updates(map[string]any{
						"attempts":   gorm.Expr("attempts + 1"),
						"last_error": sinkErr,
					}).Error; err != nil {
					return err
				}
			}
		}

		return tx.Model(&models.CRMEventOutbox{}).
			Where("id IN ?", ids).
			Update("published_at", time.Now().UTC()).Error
	})
	if err != nil {
		return 0, err
	}
	return len(claimed), nil
}

// fanOut runs every sink for one event and returns a combined error string,
// empty when all sinks succeeded.
func (r *Relay) fanOut(ctx context.Context, e Event) string {
	var failures string
	for _, sink := range r.Sinks {
		if err := sink.Handle(ctx, e); err != nil {
			r.Log.Error("CRM event sink failed",
				"sink", sink.Name(),
				"event_type", e.Type,
				"event_id", e.ID,
				"error", err,
			)
			if failures != "" {
				failures += "; "
			}
			failures += sink.Name() + ": " + err.Error()
		}
	}
	return failures
}

// Prune deletes published events past the retention window.
func (r *Relay) Prune(ctx context.Context) {
	r.applyDefaults()

	cutoff := time.Now().UTC().Add(-r.Retention)
	res := r.DB.WithContext(ctx).
		Where("published_at IS NOT NULL AND published_at < ?", cutoff).
		Delete(&models.CRMEventOutbox{})
	if res.Error != nil {
		r.Log.Error("CRM event outbox prune failed", "error", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		r.Log.Info("Pruned published CRM events", "count", res.RowsAffected)
	}
}
