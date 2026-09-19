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

// The profile's Deals filter asks for type "deal". Deal events were labelled
// with the catch-all "activity", so the filter matched nothing and the page
// always said the contact had no history — and because the query read a
// bounded batch of the newest rows before filtering, a contact with plenty of
// recent tagging would have hidden older deals even once the label was right.
func TestBuild_DealFilterFindsDealsBehindNewerActivity(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	now := time.Now()
	require.NoError(t, db.Create(&models.ContactActivity{
		OrganizationID: org.ID, ContactID: contact.ID, Type: "deal.stage_changed",
		ActorType: "system", Data: models.JSONB{}, OccurredAt: now.Add(-48 * time.Hour),
	}).Error)
	for i := 0; i < 120; i++ {
		require.NoError(t, db.Create(&models.ContactActivity{
			OrganizationID: org.ID, ContactID: contact.ID, Type: "contact.tag_added",
			ActorType: "system", Data: models.JSONB{"tag": "x"},
			OccurredAt: now.Add(-time.Duration(i) * time.Minute),
		}).Error)
	}

	items, err := timeline.New(db).Build(context.Background(), org.ID, contact.ID, timeline.Opts{
		Limit: 20, Types: []string{timeline.TypeDeal},
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, timeline.TypeDeal, items[0].Type)

	// Mixed filters still work, and the catch-all still sees everything else.
	tags, err := timeline.New(db).Build(context.Background(), org.ID, contact.ID, timeline.Opts{
		Limit: 20, Types: []string{timeline.TypeTag},
	})
	require.NoError(t, err)
	assert.Len(t, tags, 20)
	for _, item := range tags {
		assert.Equal(t, timeline.TypeTag, item.Type)
	}
}
