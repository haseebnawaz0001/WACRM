// Package entityrefs knows which tables point at which entities (plan 10, S8).
//
// WhatsApp accounts are referenced by *name* in eighteen columns across the
// schema. Renaming an account changed one row and left the other seventeen
// pointing at a name that no longer existed: campaigns failed to resolve their
// account, the worker looked one up by name and gave up, and every message,
// template and flow that belonged to that account detached from it silently.
// Nothing errored — the history simply stopped being reachable.
//
// The fix plan 10 specifies is not to convert every column to a UUID (that is a
// later migration); it is to make a rename cascade to every registered
// reference in one transaction, so the name stays consistent everywhere or the
// rename does not happen at all.
package entityrefs

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AccountNameRef is one table column holding a WhatsApp account name.
type AccountNameRef struct {
	Table  string
	Column string
}

// accountNameRefs is every column that stores an account by name.
//
// The list is explicit rather than discovered from the schema at runtime: a
// column that is added later should fail review for not being registered here,
// which is a better outcome than a rename quietly skipping it.
//
// Two spellings exist because most models let GORM derive the column from the
// WhatsAppAccount field (whats_app_account) while a few pin the unsplit
// spelling (whatsapp_account).
var accountNameRefs = []AccountNameRef{
	{"agent_transfers", "whats_app_account"},
	{"ai_contexts", "whats_app_account"},
	{"bulk_message_campaigns", "whats_app_account"},
	{"call_logs", "whatsapp_account"},
	{"call_permissions", "whats_app_account"},
	{"call_transfers", "whats_app_account"},
	{"catalogs", "whats_app_account"},
	{"chatbot_flows", "whats_app_account"},
	{"chatbot_sessions", "whats_app_account"},
	{"chatbot_settings", "whats_app_account"},
	{"contacts", "whats_app_account"},
	{"conversations", "whatsapp_account"},
	{"ivr_flows", "whatsapp_account"},
	{"keyword_rules", "whats_app_account"},
	{"messages", "whats_app_account"},
	{"notification_rules", "whats_app_account"},
	{"templates", "whats_app_account"},
	{"whatsapp_flows", "whats_app_account"},
}

// AccountNameRefs returns the registered references.
func AccountNameRefs() []AccountNameRef {
	out := make([]AccountNameRef, len(accountNameRefs))
	copy(out, accountNameRefs)
	return out
}

// RenameAccount rewrites every reference to an account's old name.
//
// The caller supplies the transaction: the account row and its references have
// to move together, or a failure halfway through leaves exactly the split-brain
// this function exists to prevent.
func RenameAccount(tx *gorm.DB, orgID uuid.UUID, oldName, newName string) error {
	if oldName == newName || oldName == "" {
		return nil
	}
	for _, ref := range accountNameRefs {
		stmt := fmt.Sprintf("UPDATE %s SET %s = ? WHERE organization_id = ? AND %s = ?",
			ref.Table, ref.Column, ref.Column)
		if err := tx.Exec(stmt, newName, orgID, oldName).Error; err != nil {
			return fmt.Errorf("entityrefs: rename account in %s.%s: %w", ref.Table, ref.Column, err)
		}
	}
	return nil
}

// CountAccountRefs reports how many rows point at an account name, per table.
// Delete handlers use it to say what would break rather than cascading.
func CountAccountRefs(db *gorm.DB, orgID uuid.UUID, name string) (map[string]int64, error) {
	out := map[string]int64{}
	if name == "" {
		return out, nil
	}
	for _, ref := range accountNameRefs {
		var n int64
		stmt := fmt.Sprintf("SELECT count(*) FROM %s WHERE organization_id = ? AND %s = ?",
			ref.Table, ref.Column)
		if err := db.Raw(stmt, orgID, name).Scan(&n).Error; err != nil {
			return nil, fmt.Errorf("entityrefs: count refs in %s: %w", ref.Table, err)
		}
		if n > 0 {
			out[ref.Table] = n
		}
	}
	return out, nil
}

// TeamRef is one table column holding a team id.
type TeamRef struct {
	Table  string
	Column string
	// Active narrows the count to rows that still matter. A resolved
	// conversation or a finished transfer names a team historically; blocking
	// a delete on those would mean a team could never be retired.
	Active string
}

// teamRefs is every column that points at a team.
//
// team_members is deliberately absent: membership is part of the team, not a
// dependency on it, and is removed with it.
var teamRefs = []TeamRef{
	{"agent_transfers", "team_id", "status = 'active' AND deleted_at IS NULL"},
	{"call_transfers", "team_id", "status = 'pending' AND deleted_at IS NULL"},
	{"conversations", "team_id", "status <> 'resolved' AND deleted_at IS NULL"},
}

// TeamRefs returns the registered team references.
func TeamRefs() []TeamRef {
	out := make([]TeamRef, len(teamRefs))
	copy(out, teamRefs)
	return out
}

// CountTeamRefs reports the live work still pointing at a team, per table.
//
// Deleting a team that owns a queue silently orphans the rows naming it: the
// transfers and conversations keep a team_id that resolves to nothing, so they
// vanish from every team view without being reassigned to anyone (plan 10, S8).
func CountTeamRefs(db *gorm.DB, orgID, teamID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	for _, ref := range teamRefs {
		var n int64
		stmt := fmt.Sprintf("SELECT count(*) FROM %s WHERE organization_id = ? AND %s = ? AND %s",
			ref.Table, ref.Column, ref.Active)
		if err := db.Raw(stmt, orgID, teamID).Scan(&n).Error; err != nil {
			return nil, fmt.Errorf("entityrefs: count team refs in %s: %w", ref.Table, err)
		}
		if n > 0 {
			out[ref.Table] = n
		}
	}
	return out, nil
}

// ReassignTeam moves live work from one team to another.
//
// The caller supplies the transaction so the move and the delete succeed or
// fail together; half-moved work is worse than a refused delete.
func ReassignTeam(tx *gorm.DB, orgID, from, to uuid.UUID) error {
	for _, ref := range teamRefs {
		stmt := fmt.Sprintf("UPDATE %s SET %s = ? WHERE organization_id = ? AND %s = ? AND %s",
			ref.Table, ref.Column, ref.Column, ref.Active)
		if err := tx.Exec(stmt, to, orgID, from).Error; err != nil {
			return fmt.Errorf("entityrefs: reassign team in %s: %w", ref.Table, err)
		}
	}
	return nil
}

// UserCleanup reports what deactivating or removing a user moved.
//
// The counts exist so the handler can tell the admin what happened — "3
// conversations were unassigned, 2 API keys revoked" — rather than leaving them
// to discover it from the queue.
type UserCleanup struct {
	TransferIDs                []uuid.UUID `json:"-"`
	TransfersReturned          int64       `json:"transfers_returned"`
	ConversationsUnassigned    int64       `json:"conversations_unassigned"`
	TeamMembershipsRemoved     int64       `json:"team_memberships_removed"`
	APIKeysRevoked             int64       `json:"api_keys_revoked"`
	PrivateSegmentsShared      int64       `json:"private_segments_shared"`
	AutomationRulesTransferred int64       `json:"automation_rules_transferred"`
}

// ReleaseUser detaches a user from the live work of one organization
// (plan 10, S8).
//
// Without it, deactivating an agent left their work where it was: conversations
// stayed assigned to somebody who could no longer sign in, so they disappeared
// from both the Unassigned view and any active agent's list, and customers
// waited on a person who was gone. Their API keys kept authenticating, and
// their private segments became unreachable — visible to nobody, deletable by
// nobody.
//
// What it deliberately does NOT touch is ownership: contact, task and deal
// owners stay put. An owner is a relationship record, and blanking it loses
// history that an admin may want to reassign deliberately; the UI flags those
// as "Owner inactive" instead.
//
// actingUserID inherits what cannot simply be dropped: automation rules keep
// running under the admin who performed the deactivation, and private segments
// become shared under them.
func ReleaseUser(tx *gorm.DB, orgID, userID, actingUserID uuid.UUID) (UserCleanup, error) {
	var out UserCleanup

	// Active transfers go back to their queue. The ids come back so the caller
	// can broadcast the unassignment once the transaction commits.
	if err := tx.Raw(`SELECT id FROM agent_transfers
		WHERE organization_id = ? AND agent_id = ? AND status = 'active' AND deleted_at IS NULL`,
		orgID, userID).Scan(&out.TransferIDs).Error; err != nil {
		return out, fmt.Errorf("entityrefs: find active transfers: %w", err)
	}
	if len(out.TransferIDs) > 0 {
		res := tx.Exec(`UPDATE agent_transfers SET agent_id = NULL, updated_at = now()
			WHERE organization_id = ? AND agent_id = ? AND status = 'active' AND deleted_at IS NULL`,
			orgID, userID)
		if res.Error != nil {
			return out, fmt.Errorf("entityrefs: return transfers to queue: %w", res.Error)
		}
		out.TransfersReturned = res.RowsAffected
	}

	// Unresolved conversations return to the Unassigned view. Resolved ones keep
	// naming their assignee: that is history, not a queue.
	res := tx.Exec(`UPDATE conversations SET assignee_id = NULL, updated_at = now()
		WHERE organization_id = ? AND assignee_id = ? AND status <> 'resolved' AND deleted_at IS NULL`,
		orgID, userID)
	if res.Error != nil {
		return out, fmt.Errorf("entityrefs: unassign conversations: %w", res.Error)
	}
	out.ConversationsUnassigned = res.RowsAffected

	// Team membership is per-organization, so it is scoped through teams.
	res = tx.Exec(`UPDATE team_members SET deleted_at = now()
		WHERE user_id = ? AND deleted_at IS NULL
		  AND team_id IN (SELECT id FROM teams WHERE organization_id = ?)`, userID, orgID)
	if res.Error != nil {
		return out, fmt.Errorf("entityrefs: remove team memberships: %w", res.Error)
	}
	out.TeamMembershipsRemoved = res.RowsAffected

	// API keys are the one credential a deactivated user would otherwise keep:
	// they authenticate on their own hash and never consult users.is_active.
	res = tx.Exec(`UPDATE api_keys SET is_active = false, updated_at = now()
		WHERE organization_id = ? AND user_id = ? AND is_active = true AND deleted_at IS NULL`,
		orgID, userID)
	if res.Error != nil {
		return out, fmt.Errorf("entityrefs: revoke api keys: %w", res.Error)
	}
	out.APIKeysRevoked = res.RowsAffected

	if actingUserID != uuid.Nil {
		// A private segment owned by a departed user is visible to nobody.
		res = tx.Exec(`UPDATE segments SET visibility = 'shared', created_by_id = ?, updated_at = now()
			WHERE organization_id = ? AND created_by_id = ? AND visibility = 'private' AND deleted_at IS NULL`,
			actingUserID, orgID, userID)
		if res.Error != nil {
			return out, fmt.Errorf("entityrefs: share private segments: %w", res.Error)
		}
		out.PrivateSegmentsShared = res.RowsAffected

		// Rules keep running; only their owner changes, so the admin who
		// removed the user inherits the thing still acting on their org.
		res = tx.Exec(`UPDATE automation_rules SET created_by_id = ?, updated_at = now()
			WHERE organization_id = ? AND created_by_id = ? AND deleted_at IS NULL`,
			actingUserID, orgID, userID)
		if res.Error != nil {
			return out, fmt.Errorf("entityrefs: transfer automation rules: %w", res.Error)
		}
		out.AutomationRulesTransferred = res.RowsAffected
	}

	return out, nil
}
