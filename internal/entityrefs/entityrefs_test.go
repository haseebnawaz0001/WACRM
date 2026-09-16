package entityrefs_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The registry has to cover every column that actually exists. A column added
// later without being registered would be silently skipped by a rename, which
// is the exact failure this package exists to stop — so the schema is the
// assertion, not the hand-written list.
func TestAccountNameRefsCoverTheSchema(t *testing.T) {
	db := testutil.SetupTestDB(t)

	type column struct {
		TableName  string `gorm:"column:table_name"`
		ColumnName string `gorm:"column:column_name"`
	}
	var actual []column
	require.NoError(t, db.Raw(`
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND column_name IN ('whats_app_account', 'whatsapp_account')
	`).Scan(&actual).Error)
	require.NotEmpty(t, actual, "the schema should have account-name columns")

	registered := map[string]bool{}
	for _, ref := range entityrefs.AccountNameRefs() {
		registered[ref.Table+"."+ref.Column] = true
	}

	for _, col := range actual {
		key := col.TableName + "." + col.ColumnName
		assert.True(t, registered[key],
			"%s references a WhatsApp account by name but is not registered in entityrefs", key)
	}
}

// Renaming an account used to change one row and leave every reference pointing
// at a name that no longer existed (plan 10, X5).
func TestRenameAccountCascades(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	other := testutil.CreateTestOrganization(t, db)

	const oldName = "Main Store"
	const newName = "Flagship Store"

	mine := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithContactAccount(oldName))
	// Same account name in a different organization: names are only unique
	// within an org, so a rename must not reach across the tenant boundary.
	theirs := testutil.CreateTestContactWith(t, db, other.ID, testutil.WithContactAccount(oldName))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return entityrefs.RenameAccount(tx, org.ID, oldName, newName)
	}))

	var updated models.Contact
	require.NoError(t, db.Where("id = ?", mine.ID).First(&updated).Error)
	assert.Equal(t, newName, updated.WhatsAppAccount, "the reference should follow the rename")

	var untouched models.Contact
	require.NoError(t, db.Where("id = ?", theirs.ID).First(&untouched).Error)
	assert.Equal(t, oldName, untouched.WhatsAppAccount,
		"another organization's account of the same name must not be renamed")
}

func TestRenameAccountIsANoOpWithoutAChange(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithContactAccount("Support"))

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return entityrefs.RenameAccount(tx, org.ID, "Support", "Support")
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return entityrefs.RenameAccount(tx, org.ID, "", "Anything")
	}))

	var after models.Contact
	require.NoError(t, db.Where("id = ?", contact.ID).First(&after).Error)
	assert.Equal(t, "Support", after.WhatsAppAccount)
}

func TestCountAccountRefs(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	testutil.CreateTestContactWith(t, db, org.ID, testutil.WithContactAccount("Busy"))
	testutil.CreateTestContactWith(t, db, org.ID, testutil.WithContactAccount("Busy"))

	counts, err := entityrefs.CountAccountRefs(db, org.ID, "Busy")
	require.NoError(t, err)
	assert.Equal(t, int64(2), counts["contacts"])

	// Tables with no references are left out rather than reported as zero, so
	// a caller can show "what would break" without filtering noise.
	assert.NotContains(t, counts, "templates")

	empty, err := entityrefs.CountAccountRefs(db, org.ID, "")
	require.NoError(t, err)
	assert.Empty(t, empty)
}
