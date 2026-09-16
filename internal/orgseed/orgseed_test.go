package orgseed_test

import (
	"context"
	"testing"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An organization created through the UI used to get roles but no contact
// fields, no task types and no pipeline, and only became whole the next time a
// migration happened to run.
func TestSeed_GivesANewOrganizationItsCRMDefaults(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	require.NoError(t, orgseed.Seed(db, org.ID))

	fields, err := customfields.New(db).Definitions(context.Background(), org.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.NotEmpty(t, fields, "a contact with nowhere to record anything is not a CRM")

	var taskTypes int64
	require.NoError(t, db.Model(&models.TaskType{}).
		Where("organization_id = ?", org.ID).Count(&taskTypes).Error)
	assert.Equal(t, int64(len(tasks.BuiltInTypes())), taskTypes)

	pipeline, err := deals.New(db).DefaultPipeline(context.Background(), org.ID)
	require.NoError(t, err)
	assert.Len(t, pipeline.Stages, len(deals.DefaultStages()))
}

// Seeding also brings an organization created before a feature shipped up to
// date, so it has to be safe to run again and must not undo what the
// organization has since changed.
func TestSeed_IsSafeToRunAgain(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	require.NoError(t, orgseed.Seed(db, org.ID))

	// Rename something, the way an organization would.
	require.NoError(t, db.Model(&models.TaskType{}).
		Where("organization_id = ? AND key = ?", org.ID, models.TaskTypeCallBack).
		Update("label", "Ring them back").Error)

	require.NoError(t, orgseed.Seed(db, org.ID))

	var renamed models.TaskType
	require.NoError(t, db.Where("organization_id = ? AND key = ?", org.ID, models.TaskTypeCallBack).
		First(&renamed).Error)
	assert.Equal(t, "Ring them back", renamed.Label, "re-seeding must not undo the org's own wording")

	var pipelines int64
	require.NoError(t, db.Model(&models.Pipeline{}).
		Where("organization_id = ?", org.ID).Count(&pipelines).Error)
	assert.Equal(t, int64(1), pipelines, "re-seeding must not give the org a second pipeline")
}

// The list is what a new organization gets; if something is added to the
// product and not here, new organizations quietly miss it.
func TestSteps_NamesEverythingItSeeds(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"contact fields", "task types", "default pipeline"},
		orgseed.Steps())
}
