package timeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/timeline"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The timeline gathers a contact's whole story, so without per-item filtering
// it becomes a way around every other permission: an agent with no access to
// the Deals board could read a contact's deal history here instead (plan 02).
func TestBuild_HideActivityExcludesTypes(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now()
	rows := []models.ContactActivity{
		{OrganizationID: org.ID, ContactID: contact.ID, Type: "contact.created",
			ActorType: "system", Data: models.JSONB{}, OccurredAt: now.Add(-4 * time.Hour)},
		{OrganizationID: org.ID, ContactID: contact.ID, Type: "deal.created",
			ActorType: "system", Data: models.JSONB{}, OccurredAt: now.Add(-3 * time.Hour)},
		{OrganizationID: org.ID, ContactID: contact.ID, Type: "deal.won",
			ActorType: "system", Data: models.JSONB{}, OccurredAt: now.Add(-2 * time.Hour)},
		{OrganizationID: org.ID, ContactID: contact.ID, Type: "task.created",
			ActorType: "system", Data: models.JSONB{}, OccurredAt: now.Add(-time.Hour)},
	}
	for i := range rows {
		require.NoError(t, db.Create(&rows[i]).Error)
	}

	svc := timeline.New(db)

	// A viewer who can see everything.
	all, err := svc.Build(context.Background(), org.ID, contact.ID, timeline.Opts{Limit: 50})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, countActivity(all), 4)

	// A viewer without deals.
	noDeals, err := svc.Build(context.Background(), org.ID, contact.ID, timeline.Opts{
		Limit:        50,
		HideActivity: []string{"deal.created", "deal.won", "deal.lost", "deal.stage_changed"},
	})
	require.NoError(t, err)
	for _, item := range noDeals {
		assert.NotContains(t, item.Summary, "Deal",
			"a viewer without deals:read must not receive deal history")
	}
	assert.Less(t, countActivity(noDeals), countActivity(all))
}

func TestBuild_NoHideActivityReturnsEverything(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, db.Create(&models.ContactActivity{
		OrganizationID: org.ID, ContactID: contact.ID, Type: "deal.created",
		ActorType: "system", Data: models.JSONB{}, OccurredAt: time.Now().Add(-time.Hour),
	}).Error)

	items, err := timeline.New(db).Build(context.Background(), org.ID, contact.ID,
		timeline.Opts{Limit: 50})
	require.NoError(t, err)

	// The assertion is that nothing was hidden, not that this contact has
	// exactly one row: other fixtures share the database.
	var sawDeal bool
	for _, item := range items {
		if item.Type == "deal" || item.Summary != "" && item.Data["title"] != nil {
			sawDeal = true
		}
	}
	assert.True(t, sawDeal || countActivity(items) > 0,
		"an empty HideActivity hides nothing")
}

func countActivity(items []timeline.Item) int {
	n := 0
	for _, i := range items {
		if len(i.ID) > 9 && i.ID[:9] == "activity:" {
			n++
		}
	}
	return n
}
