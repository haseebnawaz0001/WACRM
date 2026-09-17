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

	// The source the contact arrived by is recorded in both places it is
	// kept: the column segments filter on, and the field the CRM reports
	// read. Writing only one of them made "new contacts by source" disagree
	// with a segment on the same attribute.
	assert.Equal(t, contacts.SourceInbound, contact.Source)
	assert.Equal(t, contacts.SourceInbound, values[models.FieldKeySource])
}

// Every source the product can record has to be an option of the built-in
// field, or the value it writes is one no filter can select and the editor
// shows as blank.
func TestResolve_EverySourceIsASelectableOption(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	sources := []string{
		contacts.SourceInbound, contacts.SourceImport, contacts.SourceCampaign,
		contacts.SourceAPI, contacts.SourceManual, contacts.SourceCall,
		contacts.SourceAddressBookSync,
	}

	var def models.CustomFieldDefinition
	require.NoError(t, db.Where("organization_id = ? AND entity_type = ? AND key = ?",
		org.ID, models.FieldEntityContact, models.FieldKeySource).First(&def).Error)

	for _, source := range sources {
		assert.True(t, def.HasOption(source),
			"the source field offers no option %q, so a contact from that path records nothing", source)
	}
}

// An organization that retired an option must not end up with contacts
// carrying a value its own field no longer offers.
func TestResolve_SkipsASourceTheFieldNoLongerOffers(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, orgseed.Seed(db, org.ID))

	require.NoError(t, db.Model(&models.CustomFieldDefinition{}).
		Where("organization_id = ? AND entity_type = ? AND key = ?",
			org.ID, models.FieldEntityContact, models.FieldKeySource).
		Update("options", models.JSONBArray{
			map[string]any{"value": "manual", "label": "Manual"},
		}).Error)

	contact, outcome, err := contacts.New(db).Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: "14155550196"}, contacts.ResolveOpts{
			CreateIfMissing: true,
			Source:          contacts.SourceInbound,
			Actor:           crmevents.SystemActor(),
		})
	require.NoError(t, err)
	require.Equal(t, contacts.OutcomeCreated, outcome)

	values, err := customfields.New(db).Values(context.Background(), org.ID,
		contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Nil(t, values[models.FieldKeySource],
		"a value outside the option list is unreachable, so it is better left unset")
	assert.Equal(t, contacts.SourceInbound, contact.Source,
		"the column still records what actually happened")
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
