package handlers_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plan 06: a file that names the same person twice is a fact about the file,
// not two customers. Importing the rows one at a time made the first create
// the contact and the second report a match against an import that had not
// finished — a confusing way to describe a spreadsheet problem.
func TestImportContacts_CollapsesDuplicateRowsInTheFile(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	// The same number three ways: with a plus, with spaces, and bare.
	result := importCSV(t, app, org.ID, admin.ID,
		"phone_number,name,company\n"+
			"+14155550101,Ada,First\n"+
			"14155550101,Ada Lovelace,Second\n"+
			"+1 415 555 0101,Ada L,Third\n")

	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 2, result.MergedInFile)
	assert.Empty(t, result.Errors)

	var count int64
	require.NoError(t, app.DB.Model(&models.Contact{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count)

	// The last row wins: a file is usually appended to, so the later line is
	// the more recent thing somebody knew.
	var contact models.Contact
	require.NoError(t, app.DB.Where("organization_id = ?", org.ID).First(&contact).Error)
	assert.Equal(t, "Ada L", contact.ProfileName)
}

// skip is the default, because overwriting a record an agent curated with a
// stale spreadsheet is the expensive mistake of the three.
func TestImportContacts_SkipsExistingByDefault(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	existing := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550102"), testutil.WithProfileName("Ada Lovelace"))

	result := importCSV(t, app, org.ID, admin.ID,
		"phone_number,name,company\n14155550102,Somebody Else,Acme\n")

	assert.Equal(t, 0, result.Created)
	assert.Equal(t, 0, result.Updated)
	assert.Equal(t, 1, result.Skipped)

	var after models.Contact
	require.NoError(t, app.DB.First(&after, "id = ?", existing.ID).Error)
	assert.Equal(t, "Ada Lovelace", after.ProfileName, "the record was left alone")
}

// update applies the row, unions tags rather than replacing them, and fills an
// empty name without overwriting one WhatsApp reported.
func TestImportContacts_UpdateOnMatchMergesRatherThanReplaces(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	existing := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550103"),
		testutil.WithProfileName("Ada Lovelace"),
		testutil.WithTags("vip"))

	result := importCSVWith(t, app, org.ID, admin.ID,
		"phone_number,name,tags,company\n14155550103,Spreadsheet Name,webinar,Analytical Engines\n",
		handlers.ContactImportOpts{OnMatch: handlers.OnMatchUpdate})

	assert.Equal(t, 1, result.Updated)
	assert.Equal(t, 0, result.Created)

	var after models.Contact
	require.NoError(t, app.DB.First(&after, "id = ?", existing.ID).Error)
	assert.Equal(t, "Ada Lovelace", after.ProfileName,
		"a name from a spreadsheet must not replace the one WhatsApp reported")
	assert.ElementsMatch(t, []any{"vip", "webinar"}, []any(after.Tags),
		"an import that adds a tag must not strip the ones an agent applied")
}

// create_anyway is for the case where one number genuinely serves two people.
// The pair is raised for review rather than hidden, so somebody can merge them
// while they still remember running the import.
func TestImportContacts_CreateAnywayFlagsTheDuplicate(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	// A record written before numbers were normalised: the stored number keeps
	// its formatting, and only phone_normalized matches what the file supplies.
	// That is the case the plan describes — the contact matches, but the
	// WhatsApp id the new record would carry is a different string, so a
	// second record can exist alongside it.
	existing := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("+1 (415) 555-0104"), testutil.WithProfileName("Reception"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", existing.ID).
		Update("phone_normalized", "14155550104").Error)

	result := importCSVWith(t, app, org.ID, admin.ID,
		"phone_number,name\n14155550104,Dr Ada\n",
		handlers.ContactImportOpts{OnMatch: handlers.OnMatchCreateAnyway})

	assert.Equal(t, 1, result.Created)
	assert.Equal(t, 1, result.Flagged)

	var contacts int64
	require.NoError(t, app.DB.Model(&models.Contact{}).
		Where("organization_id = ?", org.ID).Count(&contacts).Error)
	assert.EqualValues(t, 2, contacts)

	var candidates int64
	require.NoError(t, app.DB.Model(&models.ContactDuplicateCandidate{}).
		Where("organization_id = ? AND status = ?", org.ID, models.DuplicatePending).
		Count(&candidates).Error)
	assert.EqualValues(t, 1, candidates, "the pair is queued for review")

	var still models.Contact
	require.NoError(t, app.DB.First(&still, "id = ?", existing.ID).Error)
	assert.Equal(t, "Reception", still.ProfileName)
}

// One WhatsApp number is one chat thread, so a second live contact under the
// identical number is refused by the database — and quietly updating the
// record the option asked not to touch would be the worst of the answers.
func TestImportContacts_CreateAnywayExplainsAnIdenticalNumber(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("14155550105"), testutil.WithProfileName("Reception"))

	result := importCSVWith(t, app, org.ID, admin.ID,
		"phone_number,name\n14155550105,Dr Ada\n",
		handlers.ContactImportOpts{OnMatch: handlers.OnMatchCreateAnyway})

	assert.Equal(t, 0, result.Created)
	assert.Equal(t, 1, result.Skipped)
	require.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Message, "already belongs to another contact")

	var count int64
	require.NoError(t, app.DB.Model(&models.Contact{}).
		Where("organization_id = ?", org.ID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
