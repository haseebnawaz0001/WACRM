package migrations_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/migrations"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

func quietLog() logf.Logger {
	return logf.New(logf.Opts{Level: logf.ErrorLevel})
}

// A migration that has already run must not run again, or every restart would
// re-apply backfills over data that has since changed legitimately.
func TestRunPending_AppliesEachMigrationOnce(t *testing.T) {
	db := testutil.SetupTestDB(t)

	// testutil already ran the registered set at startup; running again must
	// be a no-op rather than a second pass.
	before := appliedNames(t, db)
	require.NoError(t, migrations.RunPending(db, quietLog()))
	after := appliedNames(t, db)

	assert.Equal(t, before, after, "a second run must not change what is applied")

	for _, m := range migrations.Registered() {
		assert.Contains(t, after, m.Name, "every registered migration should be recorded")
	}
}

// Every registered migration must be recorded, so RunPending can tell what is
// still outstanding.
func TestRunPending_RecordsEveryRegisteredMigration(t *testing.T) {
	db := testutil.SetupTestDB(t)
	require.NoError(t, migrations.RunPending(db, quietLog()))

	applied := appliedNames(t, db)
	registered := migrations.Registered()
	require.NotEmpty(t, registered, "there should be at least one migration to run")

	for _, m := range registered {
		assert.Contains(t, applied, m.Name)
	}
}

// Names must sort into the order migrations run, since later migrations may
// depend on earlier ones.
func TestRegistered_IsSortedByName(t *testing.T) {
	registered := migrations.Registered()
	for i := 1; i < len(registered); i++ {
		assert.Less(t, registered[i-1].Name, registered[i].Name,
			"migrations run in name order")
	}
}

func TestRegister_RejectsDuplicateNames(t *testing.T) {
	existing := migrations.Registered()
	require.NotEmpty(t, existing)

	assert.Panics(t, func() {
		migrations.Register(migrations.Migration{
			Name: existing[0].Name,
			Run:  func(*gorm.DB) error { return nil },
		})
	}, "a duplicate name would silently skip one of the two migrations")
}

func appliedNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var rows []migrations.SchemaMigration
	require.NoError(t, db.Order("name").Find(&rows).Error)

	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names
}
