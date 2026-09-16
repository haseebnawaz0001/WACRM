package contacts_test

import (
	"context"
	"testing"

	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plan 01 requires a new contact to start at the "new" lifecycle stage.
// Without it the field stays empty until somebody edits the contact by hand,
// so the funnel starts wherever people remembered to set it and a segment on
// "new" never matches the contacts that actually are new.
func TestResolve_NewContactStartsAtLifecycleNew(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	contact, outcome, err := contacts.New(db).Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550199"}, contacts.ResolveOpts{
			CreateIfMissing: true,
			Source:          contacts.SourceInbound,
			Actor:           crmevents.SystemActor(),
		})
	require.NoError(t, err)
	require.Equal(t, contacts.OutcomeCreated, outcome)

	values, err := customfields.New(db).Values(context.Background(), org.ID,
		contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, models.LifecycleNew, values[models.FieldKeyLifecycleStage])

	// The source the contact arrived by is recorded too, which is what the
	// "new contacts by source" report counts.
	assert.Equal(t, contacts.SourceInbound, contact.Source)
}

// An organization that predates the field must not fail contact creation —
// an inbound message is never worth losing over a missing default.
func TestResolve_WithoutLifecycleFieldStillCreatesContact(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	// Deliberately not seeded.

	contact, outcome, err := contacts.New(db).Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550198"}, contacts.ResolveOpts{
			CreateIfMissing: true,
			Source:          contacts.SourceInbound,
			Actor:           crmevents.SystemActor(),
		})
	require.NoError(t, err)
	assert.Equal(t, contacts.OutcomeCreated, outcome)
	assert.NotNil(t, contact)
}

// Resolving an existing contact must not reset a stage somebody has moved on.
func TestResolve_ExistingContactKeepsItsStage(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	svc := contacts.New(db)
	opts := contacts.ResolveOpts{
		CreateIfMissing: true,
		Source:          contacts.SourceInbound,
		Actor:           crmevents.SystemActor(),
	}
	contact, _, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550197"}, opts)
	require.NoError(t, err)

	// Someone qualifies them.
	fields := customfields.New(db)
	_, err = fields.SetValues(db, org.ID, contact.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyLifecycleStage: models.LifecycleCustomer}, nil)
	require.NoError(t, err)

	// They write in again.
	_, outcome, err := svc.Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550197"}, opts)
	require.NoError(t, err)
	require.Equal(t, contacts.OutcomeFound, outcome)

	values, err := fields.Values(context.Background(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, models.LifecycleCustomer, values[models.FieldKeyLifecycleStage],
		"a later message must not demote a customer back to new")
}
