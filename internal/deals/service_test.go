package deals_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *deals.Service, *models.Organization, *models.Contact, *models.User, *models.Pipeline) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	svc := deals.New(db)
	pipeline, err := svc.CreatePipeline(ctx(), org.ID, "Sales", "USD", &user.ID)
	require.NoError(t, err)

	return db, svc, org, contact, user, pipeline
}

func stageNamed(t *testing.T, pipeline *models.Pipeline, name string) models.PipelineStage {
	t.Helper()
	for _, stage := range pipeline.Stages {
		if stage.Name == name {
			return stage
		}
	}
	t.Fatalf("stage %q not found", name)
	return models.PipelineStage{}
}

// --- Pipelines ---

// A board with no columns is unusable, and asking someone to invent a sales
// process before their first deal is a poor first run.
func TestCreatePipeline_SeedsDefaultStages(t *testing.T) {
	_, _, _, _, _, pipeline := setup(t)

	require.Len(t, pipeline.Stages, 5)
	assert.Equal(t, "New", pipeline.Stages[0].Name, "stages come back in board order")
	assert.Equal(t, models.StageWon, stageNamed(t, pipeline, "Won").StageType)
	assert.Equal(t, models.StageLost, stageNamed(t, pipeline, "Lost").StageType)
	assert.True(t, pipeline.IsDefault, "the first pipeline is the default")
}

func TestCreatePipeline_OnlyTheFirstIsDefault(t *testing.T) {
	_, svc, org, _, user, _ := setup(t)

	second, err := svc.CreatePipeline(ctx(), org.ID, "Support", "USD", &user.ID)
	require.NoError(t, err)
	assert.False(t, second.IsDefault)
}

// --- Creating deals ---

func TestCreate_StartsInTheFirstOpenStage(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "New opportunity",
		Value: 1000, CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	assert.Equal(t, models.DealOpen, deal.Status)
	assert.Equal(t, stageNamed(t, pipeline, "New").ID, deal.StageID)
	assert.Equal(t, "USD", deal.Currency, "the pipeline's currency is inherited")
	assert.False(t, deal.StageEnteredAt.IsZero())
}

// Whoever manages the relationship is the obvious person to own the
// opportunity.
func TestCreate_DefaultsOwnerToTheContactOwner(t *testing.T) {
	db, svc, org, contact, user, _ := setup(t)
	owner := testutil.CreateTestUser(t, db, org.ID)

	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Update("assigned_user_id", owner.ID).Error)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Owned", CreatedBy: &user.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, deal.OwnerID)
	assert.Equal(t, owner.ID, *deal.OwnerID)
}

// A board whose first column happened to be "Lost" would otherwise create every
// deal already closed.
func TestCreate_SkipsNonOpenStagesWhenChoosingTheStart(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)

	// Move "Lost" to the front of the board.
	lost := stageNamed(t, pipeline, "Lost")
	require.NoError(t, db.Model(&models.PipelineStage{}).Where("id = ?", lost.ID).
		Update("position", 1).Error)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Should be open", CreatedBy: &user.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, models.DealOpen, deal.Status)
	assert.NotEqual(t, lost.ID, deal.StageID)
}

func TestCreate_RejectsMissingTitleAndContact(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)

	_, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "", CreatedBy: &user.ID,
	})
	assert.ErrorContains(t, err, "needs a title")

	_, err = svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: uuid.New(), Title: "Orphan", CreatedBy: &user.ID,
	})
	assert.ErrorContains(t, err, "contact not found")
}

// --- Moving ---

// A deal sitting in a "Won" column that still says open is the kind of
// disagreement that makes a board untrustworthy.
func TestMoveStage_StatusFollowsTheStage(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Closing", Value: 500, CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	won := stageNamed(t, pipeline, "Won")
	moved, err := svc.MoveStage(ctx(), org.ID, deal.ID, won.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	assert.Equal(t, models.DealWon, moved.Status)
	require.NotNil(t, moved.ClosedAt)
}

// Reopening must clear the close, or a reopened deal still reports as closed.
func TestMoveStage_ReopeningClearsTheClose(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Back in play", CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	lost := stageNamed(t, pipeline, "Lost")
	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, lost.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	qualified := stageNamed(t, pipeline, "Qualified")
	reopened, err := svc.MoveStage(ctx(), org.ID, deal.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	assert.Equal(t, models.DealOpen, reopened.Status)
	assert.Nil(t, reopened.ClosedAt)
}

// Funnel reporting needs how long deals actually spent in each stage, which
// cannot be recovered from where they are now.
func TestMoveStage_RecordsHowLongTheDealSatInTheStage(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Timed", CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	// Pretend it entered the stage two hours ago.
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).
		Update("stage_entered_at", time.Now().UTC().Add(-2*time.Hour)).Error)

	qualified := stageNamed(t, pipeline, "Qualified")
	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	var history []models.DealStageHistory
	require.NoError(t, db.Where("deal_id = ?", deal.ID).Order("created_at").Find(&history).Error)
	require.Len(t, history, 2, "creation and the move are both recorded")

	move := history[1]
	require.NotNil(t, move.FromStageID)
	assert.Equal(t, qualified.ID, move.ToStageID)
	assert.Greater(t, move.DurationSeconds, int64(3000), "roughly two hours in the previous stage")
}

func TestMoveStage_RejectsAStageFromAnotherPipeline(t *testing.T) {
	_, svc, org, contact, user, _ := setup(t)

	other, err := svc.CreatePipeline(ctx(), org.ID, "Other", "USD", &user.ID)
	require.NoError(t, err)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Confused", CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, other.Stages[0].ID, crmevents.SystemActor())
	assert.ErrorContains(t, err, "not part of this deal's pipeline")
}

// --- Totals ---

// A forecast that is simply the sum of everything anyone ever hoped for is not
// a forecast.
func TestTotals_WeightsByStageProbability(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	qualified := stageNamed(t, pipeline, "Qualified") // 30%
	_, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Weighted",
		Value: 1000, StageID: &qualified.ID, CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	totals, err := svc.Totals(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)
	require.Len(t, totals, 1)

	assert.Equal(t, 1, totals[0].Count)
	assert.InDelta(t, 1000, totals[0].Value, 0.01)
	assert.InDelta(t, 300, totals[0].Weighted, 0.01, "1000 at 30% is 300")
}

// Closed deals are history, not forecast.
func TestTotals_CountOnlyOpenDeals(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Closing", Value: 5000, CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	won := stageNamed(t, pipeline, "Won")
	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, won.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	totals, err := svc.Totals(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)
	assert.Empty(t, totals, "a won deal is no longer pipeline")
}

// --- Rotting ---

func TestIsRotting(t *testing.T) {
	now := time.Now().UTC()
	stage := models.PipelineStage{RottingDays: 7}

	fresh := models.Deal{Status: models.DealOpen, StageEnteredAt: now.Add(-24 * time.Hour)}
	assert.False(t, fresh.IsRotting(stage, now))

	stale := models.Deal{Status: models.DealOpen, StageEnteredAt: now.Add(-10 * 24 * time.Hour)}
	assert.True(t, stale.IsRotting(stage, now))

	// A closed deal is not neglected, it is finished.
	closed := models.Deal{Status: models.DealWon, StageEnteredAt: now.Add(-100 * 24 * time.Hour)}
	assert.False(t, closed.IsRotting(stage, now))

	// Rotting off means never flagged.
	assert.False(t, stale.IsRotting(models.PipelineStage{RottingDays: 0}, now))
}

// --- Events ---

func TestDealLifecycle_RecordsEvents(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	require.NoError(t, db.Exec("DELETE FROM crm_event_outbox WHERE organization_id = ?", org.ID).Error)

	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Tracked", CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	won := stageNamed(t, pipeline, "Won")
	_, err = svc.MoveStage(ctx(), org.ID, deal.ID, won.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	var types []string
	require.NoError(t, db.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND contact_id = ?", org.ID, contact.ID).
		Order("occurred_at").Pluck("type", &types).Error)

	assert.Contains(t, types, "deal.created")
	assert.Contains(t, types, "deal.won")
}

func TestList_FiltersByContactAndStatus(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	other := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559100001"))

	mine, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Mine", CreatedBy: &user.ID,
	})
	require.NoError(t, err)
	_, err = svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: other.ID, Title: "Theirs", CreatedBy: &user.ID,
	})
	require.NoError(t, err)

	rows, err := svc.List(ctx(), org.ID, deals.ListOpts{ContactID: &contact.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, mine.ID, rows[0].ID)

	won := stageNamed(t, pipeline, "Won")
	_, err = svc.MoveStage(ctx(), org.ID, mine.ID, won.ID, crmevents.SystemActor())
	require.NoError(t, err)

	open, err := svc.List(ctx(), org.ID, deals.ListOpts{ContactID: &contact.ID})
	require.NoError(t, err)
	assert.Empty(t, open, "the default view is open deals")

	all, err := svc.List(ctx(), org.ID, deals.ListOpts{ContactID: &contact.ID, Status: "all"})
	require.NoError(t, err)
	assert.Len(t, all, 1)
}
