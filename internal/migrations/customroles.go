package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Custom-role permission backfill (plan 10, S1).
//
// EnsureSystemRolePermissions only fills the three built-in roles. An
// organization that had built its own roles — "Supervisor", "Team lead",
// "Read-only" — got nothing, so every CRM feature shipped invisible to the
// people who actually use those roles, and the only fix was for an admin to
// tick boxes in the role editor for each new permission, having first worked
// out which ones were new.
//
// A custom role cannot be matched by name, so it is matched by what it can
// already do: a role that may write contacts may write the tasks attached to
// them. The mapping is deliberately conservative — read implies read, write
// implies write, and delete is never inferred from anything weaker — because a
// wrong inference here silently widens someone's access.

// ImpliedGrant is one permission and the permissions that earn it.
type ImpliedGrant struct {
	// Permission is the "resource:action" being granted.
	Permission string
	// ImpliedBy lists permissions where holding any one of them means the role
	// should also hold Permission. Only custom roles are matched this way;
	// system roles are handled by name in EnsureSystemRolePermissions.
	ImpliedBy []string
}

// EnsureCustomRolePermissions grants each permission to every custom role that
// already holds one of the permissions that implies it.
//
// Revoked grants (role_permission_revocations) are never re-added: an
// administrator who removed a permission has said what they want, and a
// migration is not entitled to overrule it.
func EnsureCustomRolePermissions(tx *gorm.DB, grants []ImpliedGrant) error {
	if len(grants) == 0 {
		return nil
	}

	permissionID, err := permissionIDsByKey(tx)
	if err != nil {
		return err
	}

	// Custom roles only. System roles are granted by name, and treating them
	// here as well would grant an admin role permissions the by-name list
	// deliberately withheld.
	var roles []models.CustomRole
	if err := tx.Where("is_system = ?", false).Find(&roles).Error; err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	roleIDs := make([]uuid.UUID, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}

	// What each custom role already holds, and what has been taken away from
	// it, in two queries rather than two per role.
	held, err := permissionsByRole(tx, "role_permissions", roleIDs)
	if err != nil {
		return err
	}
	revoked, err := permissionsByRole(tx, "role_permission_revocations", roleIDs)
	if err != nil {
		return err
	}

	rows := make([]models.RolePermission, 0, len(roles))
	for _, grant := range grants {
		grantID, ok := permissionID[grant.Permission]
		if !ok {
			return fmt.Errorf("migrations: permission %q is not seeded", grant.Permission)
		}

		sources := make([]uuid.UUID, 0, len(grant.ImpliedBy))
		for _, key := range grant.ImpliedBy {
			if id, ok := permissionID[key]; ok {
				sources = append(sources, id)
			}
			// A missing source permission is not an error: it may belong to a
			// feature this deployment never had.
		}
		if len(sources) == 0 {
			continue
		}

		for _, role := range roles {
			if held[role.ID][grantID] || revoked[role.ID][grantID] {
				continue
			}
			for _, source := range sources {
				if held[role.ID][source] {
					rows = append(rows, models.RolePermission{
						CustomRoleID: role.ID,
						PermissionID: grantID,
					})
					break
				}
			}
		}
	}

	if len(rows) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(rows, 500).Error
}

// permissionIDsByKey resolves every seeded permission to its id, keyed
// "resource:action".
func permissionIDsByKey(tx *gorm.DB) (map[string]uuid.UUID, error) {
	var permissions []models.Permission
	if err := tx.Find(&permissions).Error; err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(permissions))
	for _, p := range permissions {
		out[p.Resource+":"+p.Action] = p.ID
	}
	return out, nil
}

// permissionsByRole reads a (custom_role_id, permission_id) table into a
// lookup. Both role_permissions and role_permission_revocations have that
// shape, which is why the table name is a parameter.
func permissionsByRole(tx *gorm.DB, table string, roleIDs []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]bool, error) {
	type pair struct {
		CustomRoleID uuid.UUID
		PermissionID uuid.UUID
	}
	var pairs []pair
	if err := tx.Table(table).
		Select("custom_role_id", "permission_id").
		Where("custom_role_id IN ?", roleIDs).
		Scan(&pairs).Error; err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]map[uuid.UUID]bool, len(roleIDs))
	for _, p := range pairs {
		if out[p.CustomRoleID] == nil {
			out[p.CustomRoleID] = map[uuid.UUID]bool{}
		}
		out[p.CustomRoleID][p.PermissionID] = true
	}
	return out, nil
}

// crmImpliedGrants is the full set of CRM permissions added by plans 01–09,
// with the existing permission that earns each one.
//
// Kept as one list rather than spread across the per-plan migrations because
// the question an administrator asks is "why does this role have that?", and
// the answer should be readable in one place.
func crmImpliedGrants() []ImpliedGrant {
	read, write, del := models.ActionRead, models.ActionWrite, models.ActionDelete
	key := func(resource, action string) string { return resource + ":" + action }

	return []ImpliedGrant{
		// Contact fields are the contact record's shape. Reading them comes
		// with reading contacts; changing the shape is an editor's job.
		{key(models.ResourceContactFields, read), []string{key(models.ResourceContacts, read)}},
		{key(models.ResourceContactFields, write), []string{key(models.ResourceContacts, write)}},

		// A follow-up is about a contact, so whoever works contacts works the
		// follow-ups on them.
		{key(models.ResourceTasks, read), []string{key(models.ResourceContacts, read)}},
		{key(models.ResourceTasks, write), []string{key(models.ResourceContacts, write)}},
		{key(models.ResourceTasks, del), []string{key(models.ResourceContacts, del)}},

		// A segment is a saved contact filter; saving one that a campaign will
		// message is a campaign decision.
		{key(models.ResourceSegments, read), []string{key(models.ResourceContacts, read)}},
		{key(models.ResourceSegments, write), []string{key(models.ResourceCampaigns, write)}},

		// Deals hang off contacts the same way tasks do.
		{key(models.ResourceDeals, read), []string{key(models.ResourceContacts, read)}},
		{key(models.ResourceDeals, write), []string{key(models.ResourceContacts, write)}},

		// The pipeline itself is configuration. Reading it comes with reading
		// deals; redesigning it comes with the general settings.
		{key(models.ResourcePipelines, read), []string{key(models.ResourceContacts, read)}},
		{key(models.ResourcePipelines, write), []string{key(models.ResourceSettingsGeneral, write)}},

		// Automations act on everybody's customers, so they follow the chatbot
		// permission — the existing "may configure what the system says on its
		// own" permission — not a contact one.
		{key(models.ResourceAutomations, read), []string{key(models.ResourceSettingsChatbot, read)}},
		{key(models.ResourceAutomations, write), []string{key(models.ResourceSettingsChatbot, write)}},

		// Reports are analytics over CRM data.
		{key(models.ResourceReports, read), []string{key(models.ResourceAnalytics, read)}},
		{key(models.ResourceReports, models.ActionExport), []string{key(models.ResourceAnalytics, read)}},
	}
}

// backfillCustomRoleCRMPermissions gives custom roles the CRM permissions that
// plans 01–09 added, based on what each role can already do.
func backfillCustomRoleCRMPermissions(tx *gorm.DB) error {
	return EnsureCustomRolePermissions(tx, crmImpliedGrants())
}
