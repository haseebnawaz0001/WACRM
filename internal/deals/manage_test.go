package deals_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func str(s string) *string   { return &s }
func num(f float64) *float64 { return &f }
func integer(i int) *int     { return &i }
func boolean(b bool) *bool   { return &b }

// --- Editing a deal ---

func TestUpdate_EditsTheDetails(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Draft", 100, nil)

	close := time.Now().UTC().AddDate(0, 1, 0)
	updated, err := svc.Update(ctx(), org.ID, deal.ID, deals.UpdateInput{
		Title:             str("Annual contract"),
		Value:             num(2500),
		ExpectedCloseDate: ptrTime(&close),
	}, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	assert.Equal(t, "Annual contract", updated.Title)
	assert.Equal(t, 2500.0, updated.Value)
	require.NotNil(t, updated.ExpectedCloseDate)
}

// Zero is a real value. If "set the value to nothing" and "leave the value
// alone" looked the same, a deal could never be corrected back down to zero.
func TestUpdate_TellsZeroApartFromAbsent(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Guessed high", 5000, nil)

	cleared, err := svc.Update(ctx(), org.ID, deal.ID, deals.UpdateInput{Value: num(0)},
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Equal(t, 0.0, cleared.Value)

	untouched, err := svc.Update(ctx(), org.ID, deal.ID, deals.UpdateInput{Title: str("Renamed")},
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Equal(t, 0.0, untouched.Value)
	assert.Equal(t, "Renamed", untouched.Title)
}

func TestUpdate_RejectsAnEmptyTitle(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Has a name", 0, nil)

	_, err := svc.Update(ctx(), org.ID, deal.ID, deals.UpdateInput{Title: str("   ")},
		crmevents.UserActor(user.ID, ""))
	require.Error(t, err)
}

func TestUpdate_ClearsTheOwner(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Owned", 0, &user.ID)

	var none *uuid.UUID
	updated, err := svc.Update(ctx(), org.ID, deal.ID, deals.UpdateInput{OwnerID: &none},
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Nil(t, updated.OwnerID)
}

func TestDelete_RemovesTheDeal(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Mistake", 0, nil)

	require.NoError(t, svc.Delete(ctx(), org.ID, deal.ID, crmevents.UserActor(user.ID, "")))

	_, err := svc.Get(ctx(), org.ID, deal.ID)
	assert.ErrorIs(t, err, deals.ErrNotFound)
}

// --- History ---

func TestHistory_NamesTheStagesItMovedBetween(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Moving", 0, nil)

	_, err := svc.MoveStage(ctx(), org.ID, deal.ID, stageNamed(t, pipeline, "Qualified").ID,
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	history, err := svc.History(ctx(), org.ID, deal.ID)
	require.NoError(t, err)
	require.Len(t, history, 2)

	assert.Equal(t, "New", history[0].ToStageName)
	assert.Empty(t, history[0].FromStageName, "creation comes from nowhere")
	assert.Equal(t, "New", history[1].FromStageName)
	assert.Equal(t, "Qualified", history[1].ToStageName)
}

// --- Pipelines ---

func TestUpdatePipeline_RenamesTheObject(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	updated, err := svc.UpdatePipeline(ctx(), org.ID, pipeline.ID, deals.PipelineInput{
		Name:                str("Bookings"),
		ObjectLabelSingular: str("Booking"),
		ObjectLabelPlural:   str("Bookings"),
	})
	require.NoError(t, err)

	assert.Equal(t, "Bookings", updated.Name)
	assert.Equal(t, "Booking", updated.ObjectLabelSingular,
		"a clinic tracks bookings, not deals, and the product should say so")
}

// "The default pipeline" stops meaning anything if two claim it, and new deals
// then land wherever the sort happens to put them.
func TestUpdatePipeline_OnlyOnePipelineIsTheDefault(t *testing.T) {
	_, svc, org, _, user, first := setup(t)

	second, err := svc.CreatePipeline(ctx(), org.ID, "Support", "USD", &user.ID)
	require.NoError(t, err)
	require.False(t, second.IsDefault)

	promoted, err := svc.UpdatePipeline(ctx(), org.ID, second.ID, deals.PipelineInput{
		IsDefault: boolean(true),
	})
	require.NoError(t, err)
	assert.True(t, promoted.IsDefault)

	demoted, err := svc.GetPipeline(ctx(), org.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, demoted.IsDefault)
}

// Deleting a board out from under live opportunities would lose work people
// are in the middle of.
func TestDeletePipeline_RefusesWhileOpenDealsLiveOnIt(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "In play", 0, nil)

	err := svc.DeletePipeline(ctx(), org.ID, pipeline.ID)
	assert.ErrorIs(t, err, deals.ErrPipelineInUse)

	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, stageNamed(t, pipeline, "Lost").ID,
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	assert.NoError(t, svc.DeletePipeline(ctx(), org.ID, pipeline.ID),
		"a pipeline with only closed deals can be retired")
}

func TestArchivePipeline_HidesItWithoutTouchingItsDeals(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Still here", 0, nil)

	archived, err := svc.ArchivePipeline(ctx(), org.ID, pipeline.ID, true)
	require.NoError(t, err)
	require.NotNil(t, archived.ArchivedAt)

	live, err := svc.ListPipelines(ctx(), org.ID, false)
	require.NoError(t, err)
	assert.Empty(t, live)

	all, err := svc.ListPipelines(ctx(), org.ID, true)
	require.NoError(t, err)
	assert.Len(t, all, 1)

	_, err = svc.Get(ctx(), org.ID, deal.ID)
	assert.NoError(t, err, "archiving retires the board, it does not delete the work")
}

// --- Stages ---

func TestCreateStage_LandsBeforeTheClosedColumns(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	stage, err := svc.CreateStage(ctx(), org.ID, pipeline.ID, deals.StageInput{
		Name: str("Negotiation"), Probability: integer(80),
	})
	require.NoError(t, err)

	reloaded, err := svc.GetPipeline(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)

	names := make([]string, 0, len(reloaded.Stages))
	for _, s := range reloaded.Stages {
		names = append(names, s.Name)
	}
	assert.Equal(t, []string{"New", "Qualified", "Proposal", "Negotiation", "Won", "Lost"}, names,
		"a new column belongs in the funnel, not past the end of it")
	assert.Equal(t, 80, stage.Probability)
}

func TestCreateStage_RejectsAnImpossibleProbability(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	_, err := svc.CreateStage(ctx(), org.ID, pipeline.ID, deals.StageInput{
		Name: str("Wishful"), Probability: integer(150),
	})
	require.Error(t, err)
}

func TestUpdateStage_RejectsAnUnknownStageType(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	_, err := svc.UpdateStage(ctx(), org.ID, stageNamed(t, pipeline, "New").ID, deals.StageInput{
		StageType: str("maybe"),
	})
	require.Error(t, err)
}

// Retyping a column has to take the cards with it, or a board full of deals in
// the won column still reports as open.
func TestUpdateStage_BringsExistingDealsWithIt(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Sitting in New", 0, nil)

	_, err := svc.UpdateStage(ctx(), org.ID, stageNamed(t, pipeline, "New").ID, deals.StageInput{
		StageType: str(models.StageWon),
	})
	require.NoError(t, err)

	reloaded, err := svc.Get(ctx(), org.ID, deal.ID)
	require.NoError(t, err)
	assert.Equal(t, models.DealWon, reloaded.Status)
}

func TestReorderStages_WritesTheNewOrder(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	order := []uuid.UUID{
		stageNamed(t, pipeline, "Qualified").ID,
		stageNamed(t, pipeline, "New").ID,
		stageNamed(t, pipeline, "Proposal").ID,
		stageNamed(t, pipeline, "Won").ID,
		stageNamed(t, pipeline, "Lost").ID,
	}
	require.NoError(t, svc.ReorderStages(ctx(), org.ID, pipeline.ID, order))

	reloaded, err := svc.GetPipeline(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)
	assert.Equal(t, "Qualified", reloaded.Stages[0].Name)
	assert.Equal(t, "New", reloaded.Stages[1].Name)
}

func TestReorderStages_RejectsAStageFromAnotherPipeline(t *testing.T) {
	_, svc, org, _, user, pipeline := setup(t)
	other, err := svc.CreatePipeline(ctx(), org.ID, "Support", "USD", &user.ID)
	require.NoError(t, err)

	err = svc.ReorderStages(ctx(), org.ID, pipeline.ID, []uuid.UUID{other.Stages[0].ID})
	require.Error(t, err)
}

// Deleting a column out from under live cards would leave them pointing at a
// stage that no longer exists, so the caller has to say where they go.
func TestDeleteStage_RequiresSomewhereForTheDealsToGo(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)
	deal := newDeal(t, svc, org.ID, contact.ID, "Homeless", 0, nil)

	newStage := stageNamed(t, pipeline, "New")
	err := svc.DeleteStage(ctx(), org.ID, newStage.ID, nil)
	assert.ErrorIs(t, err, deals.ErrStageHasDeals)

	qualified := stageNamed(t, pipeline, "Qualified")
	require.NoError(t, svc.DeleteStage(ctx(), org.ID, newStage.ID, &qualified.ID))

	moved, err := svc.Get(ctx(), org.ID, deal.ID)
	require.NoError(t, err)
	assert.Equal(t, qualified.ID, moved.StageID)
}

func TestDeleteStage_RemovesAnEmptyColumnOutright(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	require.NoError(t, svc.DeleteStage(ctx(), org.ID, stageNamed(t, pipeline, "Proposal").ID, nil))

	reloaded, err := svc.GetPipeline(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)
	assert.Len(t, reloaded.Stages, 4)
}

// A pipeline with nowhere to put a new deal is a pipeline nobody can use.
func TestDeleteStage_KeepsAtLeastOneOpenColumn(t *testing.T) {
	_, svc, org, _, _, pipeline := setup(t)

	require.NoError(t, svc.DeleteStage(ctx(), org.ID, stageNamed(t, pipeline, "Qualified").ID, nil))
	require.NoError(t, svc.DeleteStage(ctx(), org.ID, stageNamed(t, pipeline, "Proposal").ID, nil))

	err := svc.DeleteStage(ctx(), org.ID, stageNamed(t, pipeline, "New").ID, nil)
	require.Error(t, err)
}

// --- Seeding ---

// A first-run organization should be able to create a deal without designing a
// sales process first.
func TestSeedOrganization_GivesANewOrgAWorkingBoard(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	require.NoError(t, deals.SeedOrganization(db, org.ID))

	svc := deals.New(db)
	pipeline, err := svc.DefaultPipeline(ctx(), org.ID)
	require.NoError(t, err)
	assert.Equal(t, deals.DefaultPipelineName, pipeline.Name)
	assert.Len(t, pipeline.Stages, 5)
}

// Re-running a migration must not give an org a second pipeline, nor undo the
// process they have since designed.
func TestSeedOrganization_LeavesAConfiguredOrgAlone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	svc := deals.New(db)

	existing, err := svc.CreatePipeline(ctx(), org.ID, "Bookings", "EUR", nil)
	require.NoError(t, err)

	require.NoError(t, deals.SeedOrganization(db, org.ID))
	require.NoError(t, deals.SeedOrganization(db, org.ID))

	all, err := svc.ListPipelines(ctx(), org.ID, true)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, existing.ID, all[0].ID)
}

func ptrTime(t *time.Time) **time.Time { return &t }
