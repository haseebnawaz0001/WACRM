package migrations_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/migrations"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// customRoleBackfill returns the registered migration, so the test exercises
// what ships rather than a copy of its logic.
func customRoleBackfill(t *testing.T) func(*gorm.DB) error {
	t.Helper()
	const name = "2026_09_24_backfill_custom_role_crm_permissions"
	for _, m := range migrations.Registered() {
		if m.Name == name {
			return m.Run
		}
	}
	t.Fatalf("migration %s is not registered", name)
	return nil
}

func roleHolds(t *testing.T, db *gorm.DB, roleID uuid.UUID, key string) bool {
	t.Helper()
	resource, action := splitKey(t, key)
	var count int64
	require.NoError(t, db.Table("role_permissions rp").
		Joins("JOIN permissions p ON p.id = rp.permission_id").
		Where("rp.custom_role_id = ? AND p.resource = ? AND p.action = ?", roleID, resource, action).
		Count(&count).Error)
	return count > 0
}

func splitKey(t *testing.T, key string) (string, string) {
	t.Helper()
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == ':' {
			return key[:i], key[i+1:]
		}
	}
	t.Fatalf("permission key %q has no action", key)
	return "", ""
}

func permissionID(t *testing.T, db *gorm.DB, key string) uuid.UUID {
	t.Helper()
	resource, action := splitKey(t, key)
	var p models.Permission
	require.NoError(t, db.Where("resource = ? AND action = ?", resource, action).First(&p).Error)
	return p.ID
}

// A custom role that can already edit contacts should be able to work the
// follow-ups attached to them. Before this, every CRM feature shipped invisible
// to organizations that had built their own roles.
func TestCustomRoleBackfill_GrantsFromTheClosestExistingPermission(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	editor := testutil.CreateTestRoleWithKeys(t, db, org.ID, "Editor", []string{
		models.ResourceContacts + ":" + models.ActionRead,
		models.ResourceContacts + ":" + models.ActionWrite,
	})
	viewer := testutil.CreateTestRoleWithKeys(t, db, org.ID, "Viewer", []string{
		models.ResourceContacts + ":" + models.ActionRead,
	})

	require.NoError(t, db.Transaction(customRoleBackfill(t)))

	assert.True(t, roleHolds(t, db, editor.ID, models.ResourceTasks+":"+models.ActionWrite),
		"a role that edits contacts should be able to create follow-ups on them")
	assert.True(t, roleHolds(t, db, editor.ID, models.ResourceDeals+":"+models.ActionWrite),
		"deals hang off contacts the same way tasks do")

	assert.True(t, roleHolds(t, db, viewer.ID, models.ResourceTasks+":"+models.ActionRead),
		"reading contacts earns reading their follow-ups")
	assert.False(t, roleHolds(t, db, viewer.ID, models.ResourceTasks+":"+models.ActionWrite),
		"read access must never be widened into write by a backfill")
	assert.False(t, roleHolds(t, db, viewer.ID, models.ResourceTasks+":"+models.ActionDelete),
		"delete is never inferred from anything weaker")
}

// The backfill must not touch the built-in roles: those are granted by name,
// and inferring extra permissions here would hand them access the by-name list
// deliberately withheld — an agent role would gain automations:write.
func TestCustomRoleBackfill_LeavesSystemRolesAlone(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	system := testutil.CreateTestRoleExact(t, db, org.ID, "agent", true, false,
		testutil.PermissionsByKeys(t, db, []string{
			models.ResourceContacts + ":" + models.ActionRead,
			models.ResourceContacts + ":" + models.ActionWrite,
		}))

	require.NoError(t, db.Transaction(customRoleBackfill(t)))

	assert.False(t, roleHolds(t, db, system.ID, models.ResourceTasks+":"+models.ActionWrite),
		"system roles are granted by name, not by inference")
}

// An administrator who removes a permission has said what they want. A backfill
// that runs on the next deploy is not entitled to overrule it.
func TestCustomRoleBackfill_NeverReAddsARevokedPermission(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	role := testutil.CreateTestRoleWithKeys(t, db, org.ID, "Editor", []string{
		models.ResourceContacts + ":" + models.ActionRead,
		models.ResourceContacts + ":" + models.ActionWrite,
	})

	// The admin took task writing away.
	require.NoError(t, db.Create(&models.RolePermissionRevocation{
		CustomRoleID: role.ID,
		PermissionID: permissionID(t, db, models.ResourceTasks+":"+models.ActionWrite),
	}).Error)

	require.NoError(t, db.Transaction(customRoleBackfill(t)))

	assert.False(t, roleHolds(t, db, role.ID, models.ResourceTasks+":"+models.ActionWrite),
		"a revoked permission must stay revoked across upgrades")
	assert.True(t, roleHolds(t, db, role.ID, models.ResourceTasks+":"+models.ActionRead),
		"revoking one permission must not block the others")
}

// Running an upgrade twice is normal. The second run must change nothing.
func TestCustomRoleBackfill_IsIdempotent(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	role := testutil.CreateTestRoleWithKeys(t, db, org.ID, "Editor", []string{
		models.ResourceContacts + ":" + models.ActionRead,
		models.ResourceContacts + ":" + models.ActionWrite,
	})

	run := customRoleBackfill(t)
	require.NoError(t, db.Transaction(run))

	var first int64
	require.NoError(t, db.Table("role_permissions").
		Where("custom_role_id = ?", role.ID).Count(&first).Error)

	require.NoError(t, db.Transaction(run))

	var second int64
	require.NoError(t, db.Table("role_permissions").
		Where("custom_role_id = ?", role.ID).Count(&second).Error)

	assert.Equal(t, first, second, "a second run must grant nothing new")
}
