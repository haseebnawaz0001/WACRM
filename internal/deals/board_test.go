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

func newDeal(t *testing.T, svc *deals.Service, orgID, contactID uuid.UUID, title string, value float64, owner *uuid.UUID) *models.Deal {
	t.Helper()
	deal, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: orgID, ContactID: contactID, Title: title, Value: value, OwnerID: owner,
	})
	require.NoError(t, err)
	return deal
}

func columnFor(t *testing.T, columns []deals.Column, stageID uuid.UUID) deals.Column {
	t.Helper()
	for _, column := range columns {
		if column.Stage.ID == stageID {
			return column
		}
	}
	t.Fatalf("no column for stage %s", stageID)
	return deals.Column{}
}

// --- Board ordering ---

// New cards land at the bottom. Appearing at the top would reshuffle a board
// somebody is already reading.
func TestCreate_AppendsToTheBottomOfTheColumn(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)

	first := newDeal(t, svc, org.ID, contact.ID, "First", 0, nil)
	second := newDeal(t, svc, org.ID, contact.ID, "Second", 0, nil)

	assert.Less(t, first.BoardPosition, second.BoardPosition)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	require.Len(t, column.Deals, 2)
	assert.Equal(t, "First", column.Deals[0].Title)
	assert.Equal(t, "Second", column.Deals[1].Title)
}

// Dropping a card between two others is what drag-and-drop actually means; the
// stage has not changed, only where it sits.
func TestMove_ReordersWithinAColumn(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)
	stage := stageNamed(t, pipeline, "New")

	a := newDeal(t, svc, org.ID, contact.ID, "A", 0, nil)
	b := newDeal(t, svc, org.ID, contact.ID, "B", 0, nil)
	c := newDeal(t, svc, org.ID, contact.ID, "C", 0, nil)

	// Drag C between A and B.
	moved, err := svc.Move(ctx(), org.ID, c.ID, deals.MoveInput{
		StageID: stage.ID, AfterID: &a.ID, BeforeID: &b.ID,
	}, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.Greater(t, moved.BoardPosition, a.BoardPosition)
	assert.Less(t, moved.BoardPosition, b.BoardPosition)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{})
	require.NoError(t, err)
	column := columnFor(t, columns, stage.ID)
	require.Len(t, column.Deals, 3)
	assert.Equal(t, []string{"A", "C", "B"},
		[]string{column.Deals[0].Title, column.Deals[1].Title, column.Deals[2].Title})
}

// Tidying a column is not progress. A funnel report that counted a drag within
// one stage as a stage change would be fiction.
func TestMove_WithinAColumnWritesNoHistory(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	stage := stageNamed(t, pipeline, "New")

	a := newDeal(t, svc, org.ID, contact.ID, "A", 0, nil)
	b := newDeal(t, svc, org.ID, contact.ID, "B", 0, nil)

	_, err := svc.Move(ctx(), org.ID, b.ID, deals.MoveInput{StageID: stage.ID, BeforeID: &a.ID},
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	var history int64
	require.NoError(t, db.Model(&models.DealStageHistory{}).
		Where("deal_id = ?", b.ID).Count(&history).Error)
	assert.Equal(t, int64(1), history, "only the creation row")
}

// Moving to another stage puts the card at the bottom of its new column unless
// the caller says otherwise, which is where a dropped card without neighbours
// belongs.
func TestMove_AcrossColumnsAppendsToTheBottom(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)
	qualified := stageNamed(t, pipeline, "Qualified")

	existing := newDeal(t, svc, org.ID, contact.ID, "Already there", 0, nil)
	_, err := svc.MoveStage(ctx(), org.ID, existing.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	arriving := newDeal(t, svc, org.ID, contact.ID, "Arriving", 0, nil)
	moved, err := svc.MoveStage(ctx(), org.ID, arriving.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{})
	require.NoError(t, err)
	column := columnFor(t, columns, qualified.ID)
	require.Len(t, column.Deals, 2)
	assert.Equal(t, "Arriving", column.Deals[1].Title)
	assert.Greater(t, moved.BoardPosition, column.Deals[0].BoardPosition)
}

// The board the user dragged on is always slightly behind the database. A
// neighbour that has since moved away must not fail the drop.
func TestMove_IgnoresANeighbourThatHasGone(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)
	stage := stageNamed(t, pipeline, "New")

	deal := newDeal(t, svc, org.ID, contact.ID, "Only one", 0, nil)
	ghost := uuid.New()

	moved, err := svc.Move(ctx(), org.ID, deal.ID, deals.MoveInput{
		StageID: stage.ID, AfterID: &ghost,
	}, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)
	assert.NotEmpty(t, moved.BoardPosition)
}

// --- Board totals and filters ---

// "12 deals · $40k" has to describe the column, not the page of cards the user
// has scrolled to.
func TestBoard_TotalsCoverTheWholeColumnNotThePage(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)

	for i := 0; i < 5; i++ {
		newDeal(t, svc, org.ID, contact.ID, "Deal", 100, nil)
	}

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{Limit: 2})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	assert.Len(t, column.Deals, 2, "only a page of cards is sent")
	assert.True(t, column.HasMore)
	assert.Equal(t, int64(5), column.Count, "the count is the column, not the page")
	assert.Equal(t, 500.0, column.Value)
	assert.Equal(t, 50.0, column.Weighted, "10% probability on the New stage")
}

func TestBoard_FiltersApplyToCardsAndTotalsAlike(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	other := testutil.CreateTestUser(t, db, org.ID)

	newDeal(t, svc, org.ID, contact.ID, "Mine", 100, &user.ID)
	newDeal(t, svc, org.ID, contact.ID, "Theirs", 900, &other.ID)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{OwnerID: &user.ID})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	require.Len(t, column.Deals, 1)
	assert.Equal(t, "Mine", column.Deals[0].Title)
	assert.Equal(t, int64(1), column.Count)
	assert.Equal(t, 100.0, column.Value, "a filtered board must not total deals it is hiding")
}

func TestBoard_SearchMatchesTheTitle(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)

	newDeal(t, svc, org.ID, contact.ID, "Annual renewal", 0, nil)
	newDeal(t, svc, org.ID, contact.ID, "New website", 0, nil)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{Search: "renew"})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	require.Len(t, column.Deals, 1)
	assert.Equal(t, "Annual renewal", column.Deals[0].Title)
}

// The board shows open work by default. A column that quietly included last
// year's losses would make every total meaningless.
func TestBoard_ShowsOpenDealsByDefault(t *testing.T) {
	_, svc, org, contact, user, pipeline := setup(t)

	lost := newDeal(t, svc, org.ID, contact.ID, "Gone", 500, nil)
	_, err := svc.MoveStage(ctx(), org.ID, lost.ID, stageNamed(t, pipeline, "Lost").ID,
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(0), columnFor(t, columns, stageNamed(t, pipeline, "Lost").ID).Count)

	_, columns, err = svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{Status: "all"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), columnFor(t, columns, stageNamed(t, pipeline, "Lost").ID).Count)
}

func TestBoard_FiltersByExpectedCloseDate(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)

	soon := time.Now().UTC().AddDate(0, 0, 3)
	late := time.Now().UTC().AddDate(0, 3, 0)
	_, err := svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "This week", ExpectedCloseDate: &soon,
	})
	require.NoError(t, err)
	_, err = svc.Create(ctx(), deals.CreateInput{
		OrgID: org.ID, ContactID: contact.ID, Title: "Next quarter", ExpectedCloseDate: &late,
	})
	require.NoError(t, err)

	cutoff := time.Now().UTC().AddDate(0, 0, 7)
	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{CloseTo: &cutoff})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	require.Len(t, column.Deals, 1)
	assert.Equal(t, "This week", column.Deals[0].Title)
}

// Cards move while someone is paging, so the next page is keyed on the last
// position rather than an offset that would skip or repeat a card.
func TestStageDeals_PagesFromACursor(t *testing.T) {
	_, svc, org, contact, _, pipeline := setup(t)
	stage := stageNamed(t, pipeline, "New")

	for i := 0; i < 5; i++ {
		newDeal(t, svc, org.ID, contact.ID, "Deal", 0, nil)
	}

	first, more, err := svc.StageDeals(ctx(), org.ID, pipeline.ID, stage.ID, "", deals.BoardFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)
	assert.True(t, more)

	second, more, err := svc.StageDeals(ctx(), org.ID, pipeline.ID, stage.ID,
		first[len(first)-1].BoardPosition, deals.BoardFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.True(t, more)

	for _, card := range second {
		assert.Greater(t, card.BoardPosition, first[len(first)-1].BoardPosition)
	}
}

// --- Visibility ---

// An agent's board is their own work. Seeing the whole company's pipeline is a
// manager's view, and it leaks what every other agent is working on.
func TestVisibleTo_LimitsAnAgentToTheirOwnDeals(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	colleague := testutil.CreateTestUser(t, db, org.ID)
	theirContact := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15557300001"))

	newDeal(t, svc, org.ID, contact.ID, "Mine", 0, &user.ID)
	newDeal(t, svc, org.ID, theirContact.ID, "Theirs", 0, &colleague.ID)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{
		Scope: deals.VisibleTo(user.ID, false),
	})
	require.NoError(t, err)

	column := columnFor(t, columns, stageNamed(t, pipeline, "New").ID)
	require.Len(t, column.Deals, 1)
	assert.Equal(t, "Mine", column.Deals[0].Title)
}

// A deal on a contact the agent looks after is their business even when
// somebody else owns the opportunity.
func TestVisibleTo_IncludesDealsOnTheirContacts(t *testing.T) {
	db, svc, org, _, user, pipeline := setup(t)
	colleague := testutil.CreateTestUser(t, db, org.ID)

	mine := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15557300002"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", mine.ID).
		Update("assigned_user_id", user.ID).Error)

	newDeal(t, svc, org.ID, mine.ID, "On my contact", 0, &colleague.ID)

	_, columns, err := svc.Board(ctx(), org.ID, pipeline.ID, deals.BoardFilter{
		Scope: deals.VisibleTo(user.ID, false),
	})
	require.NoError(t, err)
	assert.Len(t, columnFor(t, columns, stageNamed(t, pipeline, "New").ID).Deals, 1)
}

func TestVisibleTo_IsUnrestrictedForContactReaders(t *testing.T) {
	assert.Nil(t, deals.VisibleTo(uuid.New(), true),
		"someone who can read every contact needs no deal restriction at all")
}
