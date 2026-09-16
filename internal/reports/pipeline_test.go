package reports_test

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
	"gorm.io/gorm"
)

func pipelineFor(t *testing.T, db *gorm.DB, orgID uuid.UUID) (*deals.Service, *models.Pipeline) {
	t.Helper()
	svc := deals.New(db)
	pipeline, err := svc.CreatePipeline(ctx(), orgID, "Sales", "USD", nil)
	require.NoError(t, err)
	return svc, pipeline
}

func stage(t *testing.T, pipeline *models.Pipeline, name string) models.PipelineStage {
	t.Helper()
	for _, s := range pipeline.Stages {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("stage %q not found", name)
	return models.PipelineStage{}
}

// Counting deals that are still in play as losses makes every pipeline look
// terrible, and every quarter look better than the last as old deals close.
func TestPipelineFunnel_WinRateCountsOnlyClosedDeals(t *testing.T) {
	db, svc, org, viewer := setup(t)
	dealSvc, pipeline := pipelineFor(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)

	won := stage(t, pipeline, "Won")
	lost := stage(t, pipeline, "Lost")

	for i, target := range []models.PipelineStage{won, won, won, lost} {
		deal, err := dealSvc.Create(ctx(), deals.CreateInput{
			OrgID: org.ID, ContactID: contact.ID, Title: "Deal", Value: float64(100 * (i + 1)),
		})
		require.NoError(t, err)
		_, err = dealSvc.MoveStage(ctx(), org.ID, deal.ID, target.ID, crmevents.SystemActor())
		require.NoError(t, err)
	}

	// One still open, which must not count against the win rate.
	_, err := dealSvc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Still in play",
	})
	require.NoError(t, err)

	funnel, err := svc.PipelineFunnel(ctx(), viewer, lastMonth(), pipeline.ID)
	require.NoError(t, err)

	assert.Equal(t, int64(3), funnel.Won)
	assert.Equal(t, int64(1), funnel.Lost)
	assert.InDelta(t, 75.0, funnel.WinRate, 0.01)
}

func TestPipelineFunnel_ReportsTimeInStage(t *testing.T) {
	db, svc, org, viewer := setup(t)
	dealSvc, pipeline := pipelineFor(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)

	deal, err := dealSvc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Timed",
	})
	require.NoError(t, err)

	// Pretend it entered its first stage two days ago.
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).
		Update("stage_entered_at", time.Now().UTC().Add(-48*time.Hour)).Error)

	qualified := stage(t, pipeline, "Qualified")
	_, err = dealSvc.MoveStage(ctx(), org.ID, deal.ID, qualified.ID, crmevents.SystemActor())
	require.NoError(t, err)

	funnel, err := svc.PipelineFunnel(ctx(), viewer, lastMonth(), pipeline.ID)
	require.NoError(t, err)

	for _, step := range funnel.Steps {
		if step.Key == qualified.ID.String() {
			require.NotNil(t, step.MedianDaysFromPrevious)
			assert.InDelta(t, 2.0, *step.MedianDaysFromPrevious, 0.1)
			return
		}
	}
	t.Fatal("the Qualified step was not in the funnel")
}

// "Why do we lose?" is the question a pipeline report exists to answer.
func TestPipelineFunnel_BreaksDownLostReasons(t *testing.T) {
	db, svc, org, viewer := setup(t)
	dealSvc, pipeline := pipelineFor(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	lost := stage(t, pipeline, "Lost")

	for _, reason := range []string{"Price", "Price", "Timing"} {
		deal, err := dealSvc.Create(ctx(), deals.CreateInput{
			OrgID: org.ID, ContactID: contact.ID, Title: "Deal", Value: 100,
		})
		require.NoError(t, err)
		_, err = dealSvc.Move(ctx(), org.ID, deal.ID,
			deals.MoveInput{StageID: lost.ID, LostReason: reason}, crmevents.SystemActor())
		require.NoError(t, err)
	}

	funnel, err := svc.PipelineFunnel(ctx(), viewer, lastMonth(), pipeline.ID)
	require.NoError(t, err)
	require.NotEmpty(t, funnel.LostReasons)

	assert.Equal(t, "Price", funnel.LostReasons[0].Reason)
	assert.Equal(t, int64(2), funnel.LostReasons[0].Count)
	assert.Equal(t, 200.0, funnel.LostReasons[0].Value)
}

// A forecast is not simply the sum of everything anyone ever hoped for.
func TestPipelineForecast_WeightsByStageProbability(t *testing.T) {
	db, svc, org, viewer := setup(t)
	dealSvc, pipeline := pipelineFor(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)

	close := time.Now().UTC().AddDate(0, 0, 20)
	_, err := dealSvc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Next month",
		Value: 1000, ExpectedCloseDate: &close,
	})
	require.NoError(t, err)

	forecast, err := svc.PipelineForecast(ctx(), viewer, pipeline.ID, 6, time.UTC)
	require.NoError(t, err)
	require.NotEmpty(t, forecast.Months)

	total := 0.0
	weighted := 0.0
	for _, month := range forecast.Months {
		total += month.Value
		weighted += month.Weighted
	}
	assert.Equal(t, 1000.0, total)
	// The first open stage is 10% probability.
	assert.InDelta(t, 100.0, weighted, 0.01)
}

// A close date in the past is the part of a forecast that is quietly fiction.
func TestPipelineForecast_SeparatesOverdueAndUndatedDeals(t *testing.T) {
	db, svc, org, viewer := setup(t)
	dealSvc, pipeline := pipelineFor(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)

	past := time.Now().UTC().AddDate(0, 0, -10)
	_, err := dealSvc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Slipped",
		Value: 500, ExpectedCloseDate: &past,
	})
	require.NoError(t, err)

	_, err = dealSvc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "No date", Value: 300,
	})
	require.NoError(t, err)

	forecast, err := svc.PipelineForecast(ctx(), viewer, pipeline.ID, 6, time.UTC)
	require.NoError(t, err)

	assert.Equal(t, int64(1), forecast.Overdue)
	assert.Equal(t, 500.0, forecast.OverdueValue)
	assert.Equal(t, int64(1), forecast.Undated)
	assert.Equal(t, 300.0, forecast.UndatedValue)
	assert.Empty(t, forecast.Months, "neither belongs in a month's forecast")
}
