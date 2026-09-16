package deals_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// matches runs a filter and returns the contacts it selects.
func matches(t *testing.T, db *gorm.DB, r *contactquery.Registry, v contactquery.Viewer, f contactquery.Filter) []models.Contact {
	t.Helper()
	q, err := contactquery.Apply(db.Model(&models.Contact{}), r, v, f)
	require.NoError(t, err)

	var out []models.Contact
	require.NoError(t, q.Find(&out).Error)
	return out
}

// The whole reason deals appear in the contact filter language: a segment like
// "everyone with an open deal in Qualified" is a campaign audience.
func TestRegisterFields_SegmentsOnAnOpenDealInAStage(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	bystander := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15557400001"))

	deal := newDeal(t, svc, org.ID, contact.ID, "Live one", 0, nil)
	newDeal(t, svc, org.ID, bystander.ID, "Elsewhere", 0, nil)

	qualified := stageNamed(t, pipeline, "Qualified")
	_, err := svc.MoveStage(ctx(), org.ID, deal.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	r := contactquery.NewRegistry()
	reloaded, err := svc.GetPipeline(ctx(), org.ID, pipeline.ID)
	require.NoError(t, err)
	deals.RegisterFields(r, reloaded.Stages, []models.Pipeline{*reloaded})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: user.ID, CanSeeAllContacts: true}
	found := matches(t, db, r, viewer, contactquery.Filter{
		Field: "deal.stage_id", Operator: contactquery.OpIn,
		Value: []any{qualified.ID.String()},
	})

	require.Len(t, found, 1)
	assert.Equal(t, contact.ID, found[0].ID)
}

// A contact with four deals is still one contact. A join would list them four
// times and a campaign would message them four times.
func TestRegisterFields_AContactWithSeveralDealsIsListedOnce(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)

	for i := 0; i < 3; i++ {
		newDeal(t, svc, org.ID, contact.ID, "One of several", 0, nil)
	}

	r := contactquery.NewRegistry()
	deals.RegisterFields(r, pipeline.Stages, []models.Pipeline{*pipeline})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: user.ID, CanSeeAllContacts: true}
	found := matches(t, db, r, viewer, contactquery.Filter{
		Field: "deal.has_open", Operator: contactquery.OpIsTrue,
	})
	assert.Len(t, found, 1)
}

// "No open deal" has to mean nothing in play, not "has some other deal".
func TestRegisterFields_HasOpenIsFalseFindsContactsWithNothingInPlay(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)
	quiet := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15557400002"))

	deal := newDeal(t, svc, org.ID, contact.ID, "Won last week", 0, nil)
	_, err := svc.MoveStage(ctx(), org.ID, deal.ID, stageNamed(t, pipeline, "Won").ID,
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	r := contactquery.NewRegistry()
	deals.RegisterFields(r, pipeline.Stages, []models.Pipeline{*pipeline})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: user.ID, CanSeeAllContacts: true}
	found := matches(t, db, r, viewer, contactquery.Filter{
		Field: "deal.has_open", Operator: contactquery.OpIsFalse,
	})

	ids := map[string]bool{}
	for _, c := range found {
		ids[c.ID.String()] = true
	}
	assert.True(t, ids[quiet.ID.String()], "a contact with no deals at all has no open deal")
	assert.True(t, ids[contact.ID.String()], "a contact whose only deal is won has no open deal")
}

// "Not in stage X" must mean "has no deal in stage X". Negating inside the
// subquery would match anyone who also happens to have a second deal.
func TestRegisterFields_NotInExcludesTheContactEntirely(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)

	qualified := stageNamed(t, pipeline, "Qualified")
	inStage := newDeal(t, svc, org.ID, contact.ID, "Qualified one", 0, nil)
	_, err := svc.MoveStage(ctx(), org.ID, inStage.ID, qualified.ID, crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	// A second deal elsewhere, which is exactly the trap.
	newDeal(t, svc, org.ID, contact.ID, "Somewhere else", 0, nil)

	r := contactquery.NewRegistry()
	deals.RegisterFields(r, pipeline.Stages, []models.Pipeline{*pipeline})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: user.ID, CanSeeAllContacts: true}
	found := matches(t, db, r, viewer, contactquery.Filter{
		Field: "deal.stage_id", Operator: contactquery.OpNotIn,
		Value: []any{qualified.ID.String()},
	})

	for _, c := range found {
		assert.NotEqual(t, contact.ID, c.ID)
	}
}

func TestRegisterFields_SegmentsOnDealStatus(t *testing.T) {
	db, svc, org, contact, user, pipeline := setup(t)

	deal := newDeal(t, svc, org.ID, contact.ID, "Lost one", 0, nil)
	_, err := svc.MoveStage(ctx(), org.ID, deal.ID, stageNamed(t, pipeline, "Lost").ID,
		crmevents.UserActor(user.ID, ""))
	require.NoError(t, err)

	r := contactquery.NewRegistry()
	deals.RegisterFields(r, pipeline.Stages, []models.Pipeline{*pipeline})

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: user.ID, CanSeeAllContacts: true}
	found := matches(t, db, r, viewer, contactquery.Filter{
		Field: "deal.status", Operator: contactquery.OpIn,
		Value: []any{models.DealLost},
	})
	require.Len(t, found, 1)
	assert.Equal(t, contact.ID, found[0].ID)
}

// The stage list is what the filter builder offers, so it has to be the org's
// real stages rather than a hard-coded set.
func TestRegisterFields_OffersTheOrgsOwnStagesAsOptions(t *testing.T) {
	_, _, _, _, _, pipeline := setup(t)

	r := contactquery.NewRegistry()
	deals.RegisterFields(r, pipeline.Stages, []models.Pipeline{*pipeline})

	field, ok := r.Lookup("deal.stage_id")
	require.True(t, ok)
	require.Len(t, field.Options, 5)
	assert.Equal(t, "New", field.Options[0].Label)
}
