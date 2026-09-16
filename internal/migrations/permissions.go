package migrations

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EnsureSystemRolePermissions grants permissions to system roles across every
// organization (plan 00, F1).
//
// New permission resources never reached existing organizations. The seeder
// only fills roles that have *zero* permissions, so an org created before a
// feature shipped kept an admin role that could not use it, and the only fix
// was manual SQL. This helper adds what is missing, by role name, and leaves
// everything else alone.
//
// grants maps a system role name (admin, manager, agent) to the
// "resource:action" permissions it should hold.
func EnsureSystemRolePermissions(tx *gorm.DB, grants map[string][]string) error {
	if len(grants) == 0 {
		return nil
	}

	// Resolve every permission key to its id once.
	var permissions []models.Permission
	if err := tx.Find(&permissions).Error; err != nil {
		return err
	}
	permissionID := make(map[string]uuid.UUID, len(permissions))
	for _, p := range permissions {
		permissionID[p.Resource+":"+p.Action] = p.ID
	}

	for roleName, keys := range grants {
		ids := make([]uuid.UUID, 0, len(keys))
		for _, key := range keys {
			id, ok := permissionID[key]
			if !ok {
				return fmt.Errorf("migrations: permission %q is not seeded", key)
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			continue
		}

		var roles []models.CustomRole
		if err := tx.Where("name = ? AND is_system = ?", roleName, true).
			Find(&roles).Error; err != nil {
			return err
		}

		rows := make([]models.RolePermission, 0, len(roles)*len(ids))
		for _, role := range roles {
			for _, permID := range ids {
				rows = append(rows, models.RolePermission{
					CustomRoleID: role.ID,
					PermissionID: permID,
				})
			}
		}
		if len(rows) == 0 {
			continue
		}

		// DoNothing on conflict: a role that already holds the permission is
		// left untouched, which is what makes this safe to re-run and safe to
		// apply to orgs that have customised their roles.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
			CreateInBatches(rows, 500).Error; err != nil {
			return err
		}
	}

	return nil
}

// grantContactFieldPermissions gives existing organizations the contact_fields
// permissions introduced by plan 01.
func grantContactFieldPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourceContactFields + ":" + models.ActionRead,
			models.ResourceContactFields + ":" + models.ActionWrite,
			models.ResourceContactFields + ":" + models.ActionDelete,
		},
		"manager": {
			models.ResourceContactFields + ":" + models.ActionRead,
			models.ResourceContactFields + ":" + models.ActionWrite,
		},
		"agent": {
			models.ResourceContactFields + ":" + models.ActionRead,
		},
	})
}

// grantTaskPermissions gives existing organizations the task permissions
// introduced by plan 04.
func grantTaskPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourceTasks + ":" + models.ActionRead,
			models.ResourceTasks + ":" + models.ActionWrite,
			models.ResourceTasks + ":" + models.ActionDelete,
		},
		"manager": {
			models.ResourceTasks + ":" + models.ActionRead,
			models.ResourceTasks + ":" + models.ActionWrite,
			models.ResourceTasks + ":" + models.ActionDelete,
		},
		// Agents own their follow-ups; deleting a task is a supervisor action.
		"agent": {
			models.ResourceTasks + ":" + models.ActionRead,
			models.ResourceTasks + ":" + models.ActionWrite,
		},
	})
}

// grantDealPermissions gives existing organizations the pipeline and deal
// permissions introduced by plan 07.
func grantDealPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourcePipelines + ":" + models.ActionRead,
			models.ResourcePipelines + ":" + models.ActionWrite,
			models.ResourcePipelines + ":" + models.ActionDelete,
			models.ResourceDeals + ":" + models.ActionRead,
			models.ResourceDeals + ":" + models.ActionWrite,
			models.ResourceDeals + ":" + models.ActionDelete,
		},
		"manager": {
			models.ResourcePipelines + ":" + models.ActionRead,
			models.ResourcePipelines + ":" + models.ActionWrite,
			models.ResourcePipelines + ":" + models.ActionDelete,
			models.ResourceDeals + ":" + models.ActionRead,
			models.ResourceDeals + ":" + models.ActionWrite,
			models.ResourceDeals + ":" + models.ActionDelete,
		},
		// Agents work the board; they do not redesign it or delete the record
		// of an opportunity.
		"agent": {
			models.ResourcePipelines + ":" + models.ActionRead,
			models.ResourceDeals + ":" + models.ActionRead,
			models.ResourceDeals + ":" + models.ActionWrite,
		},
	})
}

// grantAutomationPermissions gives existing organizations the automation
// permissions introduced by plan 08.
func grantAutomationPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourceAutomations + ":" + models.ActionRead,
			models.ResourceAutomations + ":" + models.ActionWrite,
			models.ResourceAutomations + ":" + models.ActionDelete,
		},
		// A manager shapes how the team's work is chased. Deleting a rule
		// destroys its run history, which is the record of what customers
		// were told, so that stays with an admin.
		"manager": {
			models.ResourceAutomations + ":" + models.ActionRead,
			models.ResourceAutomations + ":" + models.ActionWrite,
		},
		// Agents do not get automation permissions: a rule one agent writes
		// acts on everybody's customers.
	})
}

// grantReportPermissions gives existing organizations the CRM report
// permissions introduced by plan 09.
func grantReportPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourceReports + ":" + models.ActionRead,
			models.ResourceReports + ":" + models.ActionExport,
		},
		"manager": {
			models.ResourceReports + ":" + models.ActionRead,
			models.ResourceReports + ":" + models.ActionExport,
		},
		// Agents keep their existing analytics.agents:read, which already
		// scopes them to their own row. A team-wide report is a different
		// question and stays with the people accountable for the team.
	})
}

// grantSegmentPermissions gives existing organizations the segment permissions
// introduced by plan 05.
func grantSegmentPermissions(tx *gorm.DB) error {
	return EnsureSystemRolePermissions(tx, map[string][]string{
		"admin": {
			models.ResourceSegments + ":" + models.ActionRead,
			models.ResourceSegments + ":" + models.ActionWrite,
			models.ResourceSegments + ":" + models.ActionDelete,
		},
		"manager": {
			models.ResourceSegments + ":" + models.ActionRead,
			models.ResourceSegments + ":" + models.ActionWrite,
			models.ResourceSegments + ":" + models.ActionDelete,
		},
		// An agent can filter a list for themselves; saving an audience that a
		// campaign will message is a supervisor's decision.
		"agent": {
			models.ResourceSegments + ":" + models.ActionRead,
		},
	})
}
