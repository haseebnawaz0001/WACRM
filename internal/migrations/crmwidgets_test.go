package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/migrations"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// crmWidgetSeed returns the registered migration by name, so the test exercises
// exactly what ships.
func crmWidgetSeed(t *testing.T) func(*gorm.DB) error {
	t.Helper()
	const name = "2026_09_26_seed_crm_default_widgets"
	for _, m := range migrations.Registered() {
		if m.Name == name {
			return m.Run
		}
	}
	t.Fatalf("migration %s is not registered", name)
	return nil
}

func widgetNames(t *testing.T, db *gorm.DB, orgID uuid.UUID) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Model(&models.Widget{}).
		Where("organization_id = ?", orgID).
		Order("name").Pluck("name", &names).Error)
	return names
}

// The original seed skips any organization that already holds a widget, which
// is every organization that existed before the CRM shipped. Without this the
// new widgets would have reached only organizations created afterwards.
func TestSeedCRMWidgets_ReachesOrganizationsThatAlreadyHaveWidgets(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, db.Create(&models.Widget{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		UserID: &user.ID, Name: "Total Messages", DataSource: "messages",
		Metric: "count", DisplayType: "number",
	}).Error)

	require.NoError(t, crmWidgetSeed(t)(db))

	names := widgetNames(t, db, org.ID)
	assert.Contains(t, names, "Total Messages", "the organization's own widgets are left alone")
	assert.Contains(t, names, "Open conversations")
	assert.Contains(t, names, "My open follow-ups")
	assert.Contains(t, names, "Pipeline value")
}

// Migrations re-run. A second pass must not double the dashboard.
func TestSeedCRMWidgets_IsIdempotent(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	testutil.CreateTestUser(t, db, org.ID)

	run := crmWidgetSeed(t)
	require.NoError(t, run(db))
	first := widgetNames(t, db, org.ID)

	require.NoError(t, run(db))
	assert.Equal(t, first, widgetNames(t, db, org.ID))
}

// An administrator who deletes a seeded widget has said what they want.
func TestSeedCRMWidgets_DoesNotResurrectADeletedWidget(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	testutil.CreateTestUser(t, db, org.ID)

	run := crmWidgetSeed(t)
	require.NoError(t, run(db))

	require.NoError(t, db.Where("organization_id = ? AND name = ?", org.ID, "Pipeline value").
		Delete(&models.Widget{}).Error)

	require.NoError(t, run(db))
	assert.NotContains(t, widgetNames(t, db, org.ID), "Pipeline value")
}

// "My open follow-ups" has to mean the person looking at it. Storing the
// seeding admin's id would put their tasks on everybody's dashboard.
func TestSeedCRMWidgets_PersonalWidgetsFilterOnTheViewer(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, crmWidgetSeed(t)(db))

	var widget models.Widget
	require.NoError(t, db.Where("organization_id = ? AND name = ?", org.ID, "My open follow-ups").
		First(&widget).Error)

	var sawMe bool
	for _, raw := range widget.Filters {
		filter, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if filter["field"] == "owner_id" && filter["value"] == "me" {
			sawMe = true
		}
	}
	assert.True(t, sawMe, "the owner filter must be resolved per viewer, not baked in")
}

// An organization with nobody in it has no one to attribute a widget to, and
// must not stop the migration for everyone else.
func TestSeedCRMWidgets_SkipsAnEmptyOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	empty := testutil.CreateTestOrganization(t, db)
	staffed := testutil.CreateTestOrganization(t, db)
	testutil.CreateTestUser(t, db, staffed.ID)

	require.NoError(t, crmWidgetSeed(t)(db))

	assert.Empty(t, widgetNames(t, db, empty.ID))
	assert.Contains(t, widgetNames(t, db, staffed.ID), "Open conversations")
}
