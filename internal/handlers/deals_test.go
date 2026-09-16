package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// dealOrg creates an organization with its default pipeline seeded.
func dealOrg(t *testing.T, app *handlers.App) (*models.Organization, *models.Pipeline) {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, deals.SeedOrganization(app.DB, org.ID))

	pipeline, err := deals.New(app.DB).DefaultPipeline(t.Context(), org.ID)
	require.NoError(t, err)
	return org, pipeline
}

func stageID(t *testing.T, pipeline *models.Pipeline, name string) uuid.UUID {
	t.Helper()
	for _, stage := range pipeline.Stages {
		if stage.Name == name {
			return stage.ID
		}
	}
	t.Fatalf("stage %q not found", name)
	return uuid.Nil
}

func decodeDeal(t *testing.T, body []byte) handlers.DealResponse {
	t.Helper()
	var result struct {
		Data struct {
			Deal handlers.DealResponse `json:"deal"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Deal
}

func decodeBoard(t *testing.T, body []byte) []handlers.BoardColumnResponse {
	t.Helper()
	var result struct {
		Data struct {
			Columns []handlers.BoardColumnResponse `json:"columns"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &result))
	return result.Data.Columns
}

func createDealVia(t *testing.T, app *handlers.App, org *models.Organization, user *models.User, body map[string]any) handlers.DealResponse {
	t.Helper()
	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, org.ID, user.ID)
	require.NoError(t, app.CreateDeal(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	return decodeDeal(t, testutil.GetResponseBody(req))
}

func TestCreateDeal_LandsInTheFirstOpenStage(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500001"))

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(),
		"title":      "Annual licence",
		"value":      4800,
	})

	assert.Equal(t, "Annual licence", deal.Title)
	assert.Equal(t, models.DealOpen, deal.Status)
	assert.Equal(t, stageID(t, pipeline, "New").String(), deal.StageID)
	assert.Equal(t, "USD", deal.Currency, "the pipeline's currency is the default")
	assert.NotEmpty(t, deal.BoardPosition)
}

func TestCreateDeal_RejectsAnUnknownContact(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": uuid.New().String(),
		"title":      "Ghost",
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.CreateDeal(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// A board for another organization must not be reachable by id alone.
func TestBoard_IsScopedToTheCallersOrganization(t *testing.T) {
	app := newTestApp(t)
	_, theirPipeline := dealOrg(t, app)
	mine, _ := dealOrg(t, app)
	admin := adminFor(t, app, mine)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, mine.ID, admin.ID)
	testutil.SetPathParam(req, "id", theirPipeline.ID.String())

	require.NoError(t, app.Board(req))
	assert.Equal(t, fasthttp.StatusNotFound, testutil.GetResponseStatusCode(req))
}

func TestBoard_ReturnsColumnsWithCardsAndTotals(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500002"))

	createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "One", "value": 1000,
	})
	createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Two", "value": 500,
	})

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", pipeline.ID.String())

	require.NoError(t, app.Board(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	columns := decodeBoard(t, testutil.GetResponseBody(req))
	require.Len(t, columns, 5)

	first := columns[0]
	assert.Equal(t, "New", first.Stage.Name)
	assert.Equal(t, int64(2), first.Count)
	assert.Equal(t, 1500.0, first.Value)
	require.Len(t, first.Deals, 2)
	assert.Equal(t, contact.ProfileName, first.Deals[0].ContactName,
		"a card has to name the person it is about")
}

func TestMoveDeal_ChangesTheStageAndTheStatus(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500003"))

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Closing", "value": 900,
	})

	req := testutil.NewJSONRequest(t, map[string]any{
		"stage_id": stageID(t, pipeline, "Won").String(),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", deal.ID)

	require.NoError(t, app.MoveDeal(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	moved := decodeDeal(t, testutil.GetResponseBody(req))
	assert.Equal(t, models.DealWon, moved.Status)
	assert.NotNil(t, moved.ClosedAt)
}

// Dropping on Lost asks for a reason, and the reason has to survive the move
// or the board can never answer why anything was lost.
func TestMoveDeal_KeepsTheLostReason(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500004"))

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Gone", "value": 200,
	})

	req := testutil.NewJSONRequest(t, map[string]any{
		"stage_id":    stageID(t, pipeline, "Lost").String(),
		"lost_reason": "Went with a competitor",
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", deal.ID)

	require.NoError(t, app.MoveDeal(req))
	moved := decodeDeal(t, testutil.GetResponseBody(req))

	assert.Equal(t, models.DealLost, moved.Status)
	assert.Equal(t, "Went with a competitor", moved.LostReason)
}

func TestMoveDeal_RejectsAStageFromAnotherPipeline(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500005"))

	other, err := deals.New(app.DB).CreatePipeline(t.Context(), org.ID, "Support", "USD", &admin.ID)
	require.NoError(t, err)

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Confused", "value": 0,
	})

	req := testutil.NewJSONRequest(t, map[string]any{"stage_id": other.Stages[0].ID.String()})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", deal.ID)

	require.NoError(t, app.MoveDeal(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestUpdateDeal_ClearsTheCloseDateWhenAsked(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500006"))

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id":          contact.ID.String(),
		"title":               "Dated",
		"expected_close_date": "2026-12-01T00:00:00Z",
	})
	require.NotNil(t, deal.ExpectedCloseDate)

	req := testutil.NewJSONRequest(t, map[string]any{"clear_close_date": true})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", deal.ID)

	require.NoError(t, app.UpdateDeal(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))
	assert.Nil(t, decodeDeal(t, testutil.GetResponseBody(req)).ExpectedCloseDate)
}

func TestDealHistory_ListsEveryMove(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500007"))

	deal := createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Tracked", "value": 0,
	})

	move := testutil.NewJSONRequest(t, map[string]any{
		"stage_id": stageID(t, pipeline, "Qualified").String(),
	})
	testutil.SetAuthContext(move, org.ID, admin.ID)
	testutil.SetPathParam(move, "id", deal.ID)
	require.NoError(t, app.MoveDeal(move))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", deal.ID)

	require.NoError(t, app.DealHistory(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			History []struct {
				ToStageName string `json:"to_stage_name"`
			} `json:"history"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.History, 2)
	assert.Equal(t, "Qualified", result.Data.History[1].ToStageName)
}

func TestContactDeals_ReturnsTheProfilePanel(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500008"))
	other := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500009"))

	createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "Theirs", "value": 100,
	})
	createDealVia(t, app, org, admin, map[string]any{
		"contact_id": other.ID.String(), "title": "Somebody else's", "value": 100,
	})

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", contact.ID.String())

	require.NoError(t, app.ContactDeals(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Deals []handlers.DealResponse `json:"deals"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.Deals, 1)
	assert.Equal(t, "Theirs", result.Data.Deals[0].Title)
}

// Reshaping the board is a manager's job. An agent who could retype a column
// would silently close everyone's deals.
func TestCreateStage_RequiresPipelinePermission(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)

	role := testutil.CreateAgentRole(t, app.DB, org.ID)
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	req := testutil.NewJSONRequest(t, map[string]any{"name": "Sneaky"})
	testutil.SetAuthContext(req, org.ID, agent.ID)
	testutil.SetPathParam(req, "id", pipeline.ID.String())

	// requireAuth writes the envelope and returns errEnvelopeSent, so the
	// error here is the refusal rather than a failure to answer.
	require.Error(t, app.CreateStage(req))
	assert.Equal(t, fasthttp.StatusForbidden, testutil.GetResponseStatusCode(req))
}

// Deleting a column with cards in it has to say where they go, and the UI
// needs a distinguishable answer to turn that into a prompt.
func TestDeleteStage_ConflictsWhenDealsHaveNowhereToGo(t *testing.T) {
	app := newTestApp(t)
	org, pipeline := dealOrg(t, app)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15554500010"))

	createDealVia(t, app, org, admin, map[string]any{
		"contact_id": contact.ID.String(), "title": "In the way", "value": 0,
	})

	req := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", stageID(t, pipeline, "New").String())

	require.NoError(t, app.DeleteStage(req))
	assert.Equal(t, fasthttp.StatusConflict, testutil.GetResponseStatusCode(req))

	target := stageID(t, pipeline, "Qualified")
	retry := testutil.NewDELETERequest(t)
	testutil.SetAuthContext(retry, org.ID, admin.ID)
	testutil.SetPathParam(retry, "id", stageID(t, pipeline, "New").String())
	testutil.SetQueryParam(retry, "move_deals_to", target.String())

	require.NoError(t, app.DeleteStage(retry))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(retry))
}

func TestListPipelines_ReturnsTheSeededBoard(t *testing.T) {
	app := newTestApp(t)
	org, _ := dealOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ListPipelines(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Pipelines []models.Pipeline `json:"pipelines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	require.Len(t, result.Data.Pipelines, 1)
	assert.Equal(t, deals.DefaultPipelineName, result.Data.Pipelines[0].Name)
	assert.Len(t, result.Data.Pipelines[0].Stages, 5)
}
