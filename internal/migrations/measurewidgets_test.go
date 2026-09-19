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

func measureWidgetSeed(t *testing.T) func(*gorm.DB) error {
	t.Helper()
	const name = "2026_09_29_seed_measure_widgets"
	for _, m := range migrations.Registered() {
		if m.Name == name {
			return m.Run
		}
	}
	t.Fatalf("migration %s is not registered", name)
	return nil
}

// An existing dashboard gains the measure widgets below what it already has,
// and keeps its own widgets exactly where they were.
func TestSeedMeasureWidgets_AddsBelowTheExistingDashboard(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	own := models.Widget{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID, UserID: &user.ID,
		Name: "Total Messages", DataSource: "messages", Metric: "count", DisplayType: "number",
		GridX: 0, GridY: 4, GridW: 3, GridH: 3,
	}
	require.NoError(t, db.Create(&own).Error)

	require.NoError(t, measureWidgetSeed(t)(db))

	var widgets []models.Widget
	require.NoError(t, db.Where("organization_id = ?", org.ID).Find(&widgets).Error)
	byName := map[string]models.Widget{}
	for _, w := range widgets {
		byName[w.Name] = w
	}

	assert.Equal(t, 4, byName["Total Messages"].GridY, "the organization's own widget has not moved")
	for _, spec := range models.MeasureDefaultWidgets() {
		w, ok := byName[spec.Name]
		require.True(t, ok, "%s was not added", spec.Name)
		assert.GreaterOrEqual(t, w.GridY, 7, "%s sits below the existing widgets", spec.Name)
		assert.Equal(t, "measure", w.DataSource)
		assert.Equal(t, spec.Measure, w.Config["measure"])
		assert.True(t, w.IsShared)
	}
	assert.Equal(t, 7, byName["Waiting for a reply"].GridY, "the new block starts at the dashboard's bottom edge")

	// Running again adds nothing.
	require.NoError(t, measureWidgetSeed(t)(db))
	var count int64
	db.Model(&models.Widget{}).Where("organization_id = ?", org.ID).Count(&count)
	assert.Equal(t, int64(len(widgets)), count)
}

// A widget somebody deleted stays deleted.
func TestSeedMeasureWidgets_DoesNotBringBackADeletedWidget(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	gone := models.Widget{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID, UserID: &user.ID,
		Name: "Overdue follow-ups", DataSource: "tasks", Metric: "count", DisplayType: "number",
	}
	require.NoError(t, db.Create(&gone).Error)
	require.NoError(t, db.Delete(&gone).Error)

	require.NoError(t, measureWidgetSeed(t)(db))

	var count int64
	db.Model(&models.Widget{}).Where("organization_id = ? AND name = ?", org.ID, "Overdue follow-ups").Count(&count)
	assert.Zero(t, count)
}

// The seeded CRM widgets move onto measures while they are still the seeded
// widget; one an administrator rebuilt on another source is left alone.
func TestSeedMeasureWidgets_MovesSeededCRMWidgetsOntoMeasures(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)

	seeded := models.Widget{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID, UserID: &user.ID,
		Name: "Pipeline value", DataSource: "deals", Metric: "sum", Field: "value", DisplayType: "number",
		IsDefault: true, GridX: 9, GridY: 11, GridW: 3, GridH: 3,
		Filters: models.JSONBArray{map[string]any{"field": "status", "operator": "equals", "value": "open"}},
	}
	rebuilt := models.Widget{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID, UserID: &user.ID,
		Name: "Deals by stage", DataSource: "messages", Metric: "count", DisplayType: "number", IsDefault: true,
	}
	require.NoError(t, db.Create(&seeded).Error)
	require.NoError(t, db.Create(&rebuilt).Error)

	require.NoError(t, measureWidgetSeed(t)(db))

	var moved models.Widget
	require.NoError(t, db.First(&moved, "id = ?", seeded.ID).Error)
	assert.Equal(t, "measure", moved.DataSource)
	assert.Equal(t, "deals_open_value", moved.Config["measure"])
	assert.Empty(t, moved.Filters)
	assert.False(t, moved.ShowChange, "a count of right now has no previous period")
	assert.Equal(t, 11, moved.GridY, "it keeps its place")

	var untouched models.Widget
	require.NoError(t, db.First(&untouched, "id = ?", rebuilt.ID).Error)
	assert.Equal(t, "messages", untouched.DataSource)
}
