// Package orgpurge deletes everything belonging to one organization
// (plan 10, S8).
//
// Deleting an organization row on its own left every table behind it: hundreds
// of thousands of messages, contacts, campaigns and audit logs scoped to an
// organization_id that no longer resolved to anything. Nothing surfaced them
// and nothing could delete them, because every list query starts from the org.
// For a self-hosted deployment that is wasted storage; for anyone honouring a
// deletion request it is data that was supposed to be gone.
//
// The table list is discovered from the live schema rather than hand-written. A
// registry is the right shape for reference checks, where a missing entry fails
// loudly; for a purge it is the wrong shape, because a table nobody remembered
// to register is exactly the data that silently survives. Discovery means a
// table added next year is purged the day it exists.
package orgpurge

import (
	"fmt"
	"sort"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// childTable is a table that belongs to an organization only through a parent.
//
// These five have no organization_id of their own, so the generic sweep cannot
// see them; they are deleted first, through their parent.
type childTable struct {
	Table  string
	Delete string
}

var childTables = []childTable{
	{"chatbot_session_messages", `DELETE FROM chatbot_session_messages
		WHERE session_id IN (SELECT id FROM chatbot_sessions WHERE organization_id = ?)`},
	{"bulk_message_recipients", `DELETE FROM bulk_message_recipients
		WHERE campaign_id IN (SELECT id FROM bulk_message_campaigns WHERE organization_id = ?)`},
	{"automation_contact_state", `DELETE FROM automation_contact_state
		WHERE rule_id IN (SELECT id FROM automation_rules WHERE organization_id = ?)`},
	{"team_members", `DELETE FROM team_members
		WHERE team_id IN (SELECT id FROM teams WHERE organization_id = ?)`},
	{"role_permissions", `DELETE FROM role_permissions
		WHERE custom_role_id IN (SELECT id FROM custom_roles WHERE organization_id = ?)`},
}

// skipTables are global, not per-organization. permissions is the catalog every
// org's roles point at; schema_migrations is the migration ledger.
var skipTables = map[string]bool{
	"permissions":       true,
	"schema_migrations": true,
	"organizations":     true,
}

// Result reports what a purge removed.
type Result struct {
	OrgID   uuid.UUID
	ByTable map[string]int64
	Total   int64
	// UsersRehomed counts users kept because they belong to another
	// organization; they are moved rather than deleted.
	UsersRehomed int64
	OrgFound     bool
}

// ScopedTables lists every table carrying an organization_id, from the live
// schema.
func ScopedTables(db *gorm.DB) ([]string, error) {
	var tables []string
	err := db.Raw(`
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public'
		  AND c.column_name = 'organization_id'
		  AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name`).Scan(&tables).Error
	if err != nil {
		return nil, fmt.Errorf("orgpurge: list scoped tables: %w", err)
	}

	out := tables[:0]
	for _, table := range tables {
		if !skipTables[table] {
			out = append(out, table)
		}
	}
	return out, nil
}

// Purge deletes every row belonging to orgID, then the organization itself.
//
// It runs in one transaction: a purge that stops halfway is worse than one that
// never ran, because the org is then neither usable nor gone.
//
// Foreign keys between org-scoped tables mean there is no single correct delete
// order, and hand-maintaining one would rot on the next migration. Instead each
// table is attempted inside a savepoint and the ones blocked by a dependent are
// retried on the next pass, until a pass makes no progress. If rows are still
// left at that point the data has a cycle the purge cannot break, and it says
// so rather than committing a partial delete.
func Purge(db *gorm.DB, orgID uuid.UUID) (Result, error) {
	result := Result{OrgID: orgID, ByTable: map[string]int64{}}

	var orgCount int64
	if err := db.Raw(`SELECT count(*) FROM organizations WHERE id = ?`, orgID).
		Scan(&orgCount).Error; err != nil {
		return result, fmt.Errorf("orgpurge: look up organization: %w", err)
	}
	result.OrgFound = orgCount > 0
	if !result.OrgFound {
		return result, fmt.Errorf("orgpurge: organization %s does not exist", orgID)
	}

	tables, err := ScopedTables(db)
	if err != nil {
		return result, err
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		rehomed, err := rehomeSharedUsers(tx, orgID)
		if err != nil {
			return err
		}
		if rehomed > 0 {
			result.UsersRehomed = rehomed
		}

		for _, child := range childTables {
			res := tx.Exec(child.Delete, orgID)
			if res.Error != nil {
				return fmt.Errorf("orgpurge: %s: %w", child.Table, res.Error)
			}
			if res.RowsAffected > 0 {
				result.ByTable[child.Table] = res.RowsAffected
				result.Total += res.RowsAffected
			}
		}

		remaining := append([]string(nil), tables...)
		for len(remaining) > 0 {
			var blocked []string
			progress := false

			for _, table := range remaining {
				deleted, err := deleteScoped(tx, table, orgID)
				if err != nil {
					blocked = append(blocked, table)
					continue
				}
				progress = true
				if deleted > 0 {
					result.ByTable[table] += deleted
					result.Total += deleted
				}
			}

			if !progress {
				sort.Strings(blocked)
				return fmt.Errorf("orgpurge: could not delete %v — a foreign key cycle blocks every remaining table", blocked)
			}
			remaining = blocked
		}

		if err := tx.Exec(`DELETE FROM organizations WHERE id = ?`, orgID).Error; err != nil {
			return fmt.Errorf("orgpurge: delete organization: %w", err)
		}
		result.ByTable["organizations"] = 1
		result.Total++
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// rehomeSharedUsers moves users who belong to more than one organization onto
// one of the others before this one is deleted.
//
// users.organization_id is a home org, not an exclusive one: a user invited into
// a second organization still works there. Deleting their row because their home
// org was purged would remove them from a live tenant — and the foreign key from
// users to organizations means leaving them pointing here would block the purge
// outright, which is how this was found. Their role moves with them, because a
// role belongs to the organization that defined it and this one is about to stop
// existing.
func rehomeSharedUsers(tx *gorm.DB, orgID uuid.UUID) (int64, error) {
	res := tx.Exec(`
		UPDATE users
		SET organization_id = keeper.organization_id,
		    role_id = keeper.role_id,
		    updated_at = now()
		FROM (
			SELECT DISTINCT ON (user_id) user_id, organization_id, role_id
			FROM user_organizations
			WHERE organization_id <> ? AND deleted_at IS NULL
			ORDER BY user_id, created_at
		) AS keeper
		WHERE users.id = keeper.user_id AND users.organization_id = ?`, orgID, orgID)
	if res.Error != nil {
		return 0, fmt.Errorf("orgpurge: rehome shared users: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// deleteScoped deletes one table's rows inside a savepoint, so a foreign key
// violation costs the attempt rather than the whole transaction.
func deleteScoped(tx *gorm.DB, table string, orgID uuid.UUID) (int64, error) {
	if err := tx.Exec(`SAVEPOINT purge_table`).Error; err != nil {
		return 0, err
	}
	res := tx.Exec(fmt.Sprintf(`DELETE FROM %q WHERE organization_id = ?`, table), orgID)
	if res.Error != nil {
		if err := tx.Exec(`ROLLBACK TO SAVEPOINT purge_table`).Error; err != nil {
			return 0, err
		}
		return 0, res.Error
	}
	if err := tx.Exec(`RELEASE SAVEPOINT purge_table`).Error; err != nil {
		return 0, err
	}
	return res.RowsAffected, nil
}

// CountRemaining reports rows still carrying an organization_id, per table.
// A purge that worked leaves nothing; this is what says so.
func CountRemaining(db *gorm.DB, orgID uuid.UUID) (map[string]int64, error) {
	tables, err := ScopedTables(db)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, table := range tables {
		var n int64
		if err := db.Raw(fmt.Sprintf(`SELECT count(*) FROM %q WHERE organization_id = ?`, table), orgID).
			Scan(&n).Error; err != nil {
			return nil, fmt.Errorf("orgpurge: count %s: %w", table, err)
		}
		if n > 0 {
			out[table] = n
		}
	}
	return out, nil
}
