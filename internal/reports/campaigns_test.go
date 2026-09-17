package reports_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedCampaign writes a finished campaign with its counters.
func seedCampaign(t *testing.T, db *gorm.DB, orgID uuid.UUID, name string,
	recipients, delivered, replied int, startedAt time.Time) {

	t.Helper()
	// The campaign points at a real template and a real user: the columns are
	// foreign keys, and a fixture that invents ids is a fixture that only
	// works until somebody adds a constraint.
	template := testutil.CreateTestTemplate(t, db, orgID, "acct")
	creator := testutil.CreateTestUser(t, db, orgID)

	require.NoError(t, db.Exec(`
		INSERT INTO bulk_message_campaigns
			(id, organization_id, whats_app_account, name, template_id, status,
			 total_recipients, delivered_count, replied_count, started_at,
			 created_by, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, 'acct', ?, ?, 'completed',
			?, ?, ?, ?, ?, now(), now())`,
		orgID, name, template.ID, recipients, delivered, replied, startedAt, creator.ID).Error)
}

// "Delivered" and "read" are Meta's numbers, and a campaign can score well on
// both while achieving nothing. A reply is the first thing a customer does
// that the business asked for.
func TestCampaignReplies_ReportsTheRatePerCampaign(t *testing.T) {
	db, svc, org, viewer := setup(t)
	started := time.Now().UTC().Add(-48 * time.Hour)

	seedCampaign(t, db, org.ID, "Autumn sale", 200, 180, 45, started)

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)

	row := result.Rows[0]
	assert.Equal(t, "Autumn sale", row.Name)
	assert.EqualValues(t, 180, row.Delivered)
	assert.EqualValues(t, 45, row.Replied)
	assert.InDelta(t, 25.0, row.ReplyRate, 0.01)
}

// The rate is replies over delivered, not over recipients: a message that
// never arrived cannot be replied to, and counting it blames the copy for a
// delivery problem.
func TestCampaignReplies_RateIsOverDeliveredNotSent(t *testing.T) {
	db, svc, org, viewer := setup(t)
	started := time.Now().UTC().Add(-24 * time.Hour)

	// Half the list never arrived.
	seedCampaign(t, db, org.ID, "Half delivered", 200, 100, 50, started)

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)

	assert.InDelta(t, 50.0, result.Rows[0].ReplyRate, 0.01,
		"50 replies from 100 delivered is 50%%, not 25%%")
}

// A campaign that delivered nothing has no reply rate. Reporting 100% or NaN
// would both be worse than reporting none.
func TestCampaignReplies_NothingDeliveredHasNoRate(t *testing.T) {
	db, svc, org, viewer := setup(t)
	seedCampaign(t, db, org.ID, "Went nowhere", 50, 0, 0, time.Now().UTC().Add(-time.Hour))

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	assert.Zero(t, result.Rows[0].ReplyRate)
}

// A campaign that has not started has no outcome to report.
func TestCampaignReplies_IgnoresCampaignsThatNeverStarted(t *testing.T) {
	db, svc, org, viewer := setup(t)
	template := testutil.CreateTestTemplate(t, db, org.ID, "acct")
	creator := testutil.CreateTestUser(t, db, org.ID)
	require.NoError(t, db.Exec(`
		INSERT INTO bulk_message_campaigns
			(id, organization_id, whats_app_account, name, template_id, status,
			 total_recipients, created_by, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, 'acct', 'Draft', ?, 'draft',
			10, ?, now(), now())`, org.ID, template.ID, creator.ID).Error)

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	assert.Empty(t, result.Rows)
}

// Another organization's campaigns are not ours to report on.
func TestCampaignReplies_IsScopedToTheOrganization(t *testing.T) {
	db, svc, org, viewer := setup(t)
	other := testutil.CreateTestOrganization(t, db).ID
	seedCampaign(t, db, other, "Theirs", 100, 100, 100, time.Now().UTC().Add(-time.Hour))

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	assert.Empty(t, result.Rows)
	_ = org
}

// The totals are computed the same way as a row, so a reader cannot get a
// different answer by adding the column up themselves.
func TestCampaignReplies_TotalsUseTheSameRule(t *testing.T) {
	db, svc, org, viewer := setup(t)
	now := time.Now().UTC()
	seedCampaign(t, db, org.ID, "One", 100, 100, 20, now.Add(-2*time.Hour))
	seedCampaign(t, db, org.ID, "Two", 100, 100, 40, now.Add(-time.Hour))

	result, err := svc.CampaignReplies(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)

	assert.EqualValues(t, 200, result.Delivered)
	assert.EqualValues(t, 60, result.Replied)
	assert.InDelta(t, 30.0, result.ReplyRate, 0.01)
	assert.NotEmpty(t, result.CountingRule, "the rule has to be stated, not assumed")
}
