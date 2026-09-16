package handlers_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importCSV runs the contacts importer over a CSV string.
func importCSV(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, body string) handlers.ContactImportResult {
	t.Helper()
	result, err := app.ImportContactsCSV(orgID, userID, strings.NewReader(body))
	require.NoError(t, err)
	return result
}

func TestImportContacts_CreatesContacts(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name,tags
15559100001,Alice,"VIP, Lead"
15559100002,Bob,
`)

	assert.Equal(t, 2, result.Created)
	assert.Zero(t, result.Skipped)
	assert.Empty(t, result.Errors)

	var alice models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?", org.ID, "15559100001").
		First(&alice).Error)
	assert.Equal(t, "Alice", alice.ProfileName)
	assert.Equal(t, contacts.SourceImport, alice.Source)
	assert.Len(t, alice.Tags, 2)
}

// The old importer matched the exact phone string, so a file using "+" created
// a second contact for someone already in the database.
func TestImportContacts_MatchesExistingContactAcrossFormatting(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	existing := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("923211234567"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", existing.ID).
		Update("phone_normalized", "923211234567").Error)

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name
+92 321 123-4567,Updated Name
`)

	assert.Zero(t, result.Created, "a differently formatted number is the same person")
	assert.Equal(t, 1, result.Updated)

	var count int64
	require.NoError(t, app.DB.Model(&models.Contact{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count, "the import must not create a duplicate")
}

// The old importer failed the entire file with a unique-constraint error when a
// row collided with a soft-deleted contact.
func TestImportContacts_DoesNotFailOnSoftDeletedDuplicates(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	deleted := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559200001"))
	require.NoError(t, contacts.New(app.DB).Delete(context.Background(), org.ID, deleted.ID,
		contacts.ReasonUser, crmevents.SystemActor()))

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name
15559200001,Back Again
15559200002,Someone Else
`)

	assert.Empty(t, result.Errors, "a deleted duplicate must not fail the import")
	assert.Equal(t, 2, result.Created)

	// The deleted contact stays deleted; a fresh record takes the number.
	var stillDeleted models.Contact
	require.NoError(t, app.DB.Unscoped().First(&stillDeleted, "id = ?", deleted.ID).Error)
	assert.True(t, stillDeleted.DeletedAt.Valid)
}

func TestImportContacts_SetsCustomFieldValues(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name,company,lifecycle_stage
15559300001,Buyer,Acme,lead
`)
	require.Empty(t, result.Errors)
	require.Equal(t, 1, result.Created)

	var contact models.Contact
	require.NoError(t, app.DB.Where("organization_id = ? AND phone_number = ?", org.ID, "15559300001").
		First(&contact).Error)

	values, err := customfields.New(app.DB).Values(context.Background(), org.ID, contact.ID, models.FieldEntityContact)
	require.NoError(t, err)
	assert.Equal(t, "Acme", values[models.FieldKeyCompany])
	assert.Equal(t, "lead", values[models.FieldKeyLifecycleStage])
}

// One malformed row must not cost the other 999.
func TestImportContacts_SkipsBadRowsAndKeepsGoing(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name,lifecycle_stage
15559400001,Good,lead
,Missing Phone,lead
15559400002,Bad Stage,not_a_stage
15559400003,Also Good,customer
`)

	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 2, result.Skipped)
	require.Len(t, result.Errors, 2)
	assert.Contains(t, result.Errors[0].Message, "missing phone number")
	assert.Contains(t, result.Errors[1].Message, "allowed options")
}

func TestImportContacts_AcceptsFriendlyHeaders(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	result := importCSV(t, app, org.ID, admin.ID, `Phone Number,Name,Company
15559500001,Alice,Acme
`)
	assert.Equal(t, 1, result.Created)
	assert.Empty(t, result.Errors)
}

func TestImportContacts_ValidatesAssignedUserAndAccount(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	stranger := uuid.New()
	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name,assigned_user_id
15559600001,Alice,`+stranger.String()+`
`)
	assert.Equal(t, 1, result.Skipped)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "not a member")

	result = importCSV(t, app, org.ID, admin.ID, `phone_number,name,whatsapp_account
15559600002,Bob,No Such Account
`)
	assert.Equal(t, 1, result.Skipped)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "unknown WhatsApp account")
}

// A column the organization has no field for is ignored, so an export from
// another system imports without editing the file first.
func TestImportContacts_IgnoresUnknownColumns(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	result := importCSV(t, app, org.ID, admin.ID, `phone_number,name,their_internal_id,legacy_notes
15559700001,Alice,ABC-123,some notes
`)
	assert.Equal(t, 1, result.Created)
	assert.Empty(t, result.Errors)
}

func TestImportContacts_RequiresAPhoneColumn(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	_, err := app.ImportContactsCSV(org.ID, admin.ID, strings.NewReader("name,company\nAlice,Acme\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phone number column")
}

func TestImportContacts_RecordsCreationEvents(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	importCSV(t, app, org.ID, admin.ID, "phone_number,name\n15559800001,Alice\n")

	var count int64
	require.NoError(t, app.DB.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND type = ?", org.ID, "contact.created").
		Count(&count).Error)
	assert.EqualValues(t, 1, count, "an imported contact is still a created contact")
}
