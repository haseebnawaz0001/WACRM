// Package migrations runs versioned, run-once data migrations (plan 00, F1).
//
// Schema changes are handled by AutoMigrate over GetMigrationModels plus the
// idempotent SQL in getIndexes. That covers columns and indexes but not data:
// seeds, backfills and permission grants have to run exactly once, in order,
// and be safe to re-run on a database that already has them.
//
// Before this package the only tools were hand-written idempotent functions
// invoked from startup, which meant every backfill had to re-derive whether it
// had already run. A named row in schema_migrations answers that once.
package migrations

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

// Migration is one run-once data change.
type Migration struct {
	// Name identifies the migration and is stored once it has run. Use a
	// sortable, unique prefix: "2026_10_01_backfill_sender_type".
	Name string

	// Run performs the change. It receives a transaction; returning an error
	// rolls the whole migration back, including its schema_migrations row,
	// so a failed migration is retried on the next start.
	Run func(tx *gorm.DB) error
}

// SchemaMigration records that a named migration has been applied.
type SchemaMigration struct {
	Name      string    `gorm:"primaryKey;size:255" json:"name"`
	AppliedAt time.Time `gorm:"not null" json:"applied_at"`
}

func (SchemaMigration) TableName() string {
	return "schema_migrations"
}

var (
	mu         sync.Mutex
	registered []Migration
)

// Register adds a migration to the set run by RunPending. Registering the same
// name twice panics: it means two changes would silently share one applied
// marker, and only the first would ever run.
func Register(m Migration) {
	mu.Lock()
	defer mu.Unlock()

	if m.Name == "" {
		panic("migrations: migration has no name")
	}
	if m.Run == nil {
		panic("migrations: migration " + m.Name + " has no Run function")
	}
	for _, existing := range registered {
		if existing.Name == m.Name {
			panic("migrations: duplicate migration name " + m.Name)
		}
	}
	registered = append(registered, m)
}

// Registered returns the registered migrations in the order they will run.
func Registered() []Migration {
	mu.Lock()
	defer mu.Unlock()

	out := make([]Migration, len(registered))
	copy(out, registered)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunPending applies every registered migration that has not run yet, in name
// order, each in its own transaction.
func RunPending(db *gorm.DB, log logf.Logger) error {
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("migrations: create schema_migrations: %w", err)
	}

	var appliedRows []SchemaMigration
	if err := db.Find(&appliedRows).Error; err != nil {
		return fmt.Errorf("migrations: read schema_migrations: %w", err)
	}
	applied := make(map[string]bool, len(appliedRows))
	for _, row := range appliedRows {
		applied[row.Name] = true
	}

	for _, m := range Registered() {
		if applied[m.Name] {
			continue
		}

		log.Info("Running data migration", "name", m.Name)
		start := time.Now()

		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.Run(tx); err != nil {
				return err
			}
			// Written in the same transaction as the change, so a crash
			// can never mark a migration applied that did not complete.
			return tx.Create(&SchemaMigration{
				Name:      m.Name,
				AppliedAt: time.Now().UTC(),
			}).Error
		}); err != nil {
			return fmt.Errorf("migrations: %s: %w", m.Name, err)
		}

		log.Info("Data migration applied", "name", m.Name, "took", time.Since(start).String())
	}

	return nil
}
