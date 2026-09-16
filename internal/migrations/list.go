package migrations

import (
	"fmt"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"gorm.io/gorm"
)

// Registered migrations. Add new ones with a later sortable name; never edit a
// migration that has shipped, because databases that already ran it will not
// run it again.
func init() {
	Register(Migration{
		Name: "2026_09_16_backfill_message_sender_type",
		Run:  backfillMessageSenderType,
	})
	Register(Migration{
		Name: "2026_09_16_backfill_contact_phone_normalized",
		Run:  backfillContactPhoneNormalized,
	})
	Register(Migration{
		Name: "2026_09_16_seed_builtin_contact_fields",
		Run:  customfields.SeedAllOrganizations,
	})
	Register(Migration{
		Name: "2026_09_17_grant_contact_field_permissions",
		Run:  grantContactFieldPermissions,
	})
	Register(Migration{
		Name: "2026_09_18_seed_task_types",
		Run:  tasks.SeedAllOrganizations,
	})
	Register(Migration{
		Name: "2026_09_18_grant_task_permissions",
		Run:  grantTaskPermissions,
	})
	Register(Migration{
		Name: "2026_09_19_seed_default_pipeline",
		Run:  deals.SeedAllOrganizations,
	})
	Register(Migration{
		Name: "2026_09_19_grant_deal_permissions",
		Run:  grantDealPermissions,
	})
	Register(Migration{
		Name: "2026_09_20_grant_automation_permissions",
		Run:  grantAutomationPermissions,
	})
	Register(Migration{
		Name: "2026_09_21_grant_report_permissions",
		Run:  grantReportPermissions,
	})
	Register(Migration{
		Name: "2026_09_22_grant_segment_permissions",
		Run:  grantSegmentPermissions,
	})
	Register(Migration{
		Name: "2026_09_23_normalize_audit_resource_types",
		Run:  normalizeAuditResourceTypes,
	})
}

// normalizeAuditResourceTypes collapses the two spellings the audit log
// grew for the same entity (plan 10, S9).
//
// Contact edits were logged as "contact" while a contact merge was logged as
// "contacts", and campaigns split the same way. Filtering the audit log for
// contacts therefore returned the edits but hid the merges — the single most
// consequential change a contact can undergo. The handlers now write one
// spelling; this moves the history onto it so the filter reaches the past too.
func normalizeAuditResourceTypes(tx *gorm.DB) error {
	renames := map[string]string{
		"contact":  "contacts",
		"campaign": "campaigns",
	}
	for from, to := range renames {
		if err := tx.Exec(`UPDATE audit_logs SET resource_type = ? WHERE resource_type = ?`,
			to, from).Error; err != nil {
			return fmt.Errorf("normalize audit resource_type %s: %w", from, err)
		}
	}
	return nil
}

// backfillContactPhoneNormalized fills phone_normalized on contacts created
// before the column existed (plan 00, F7).
//
// Without it the lifecycle service's normalised-phone lookup step would match
// nothing for existing contacts, so an import or an inbound message in a
// different format would create a duplicate of a contact that is already there.
//
// The expression mirrors phoneutil.Normalize: strip everything that is not a
// digit. Group JIDs contain "@" and are left alone, exactly as the Go code
// leaves them alone.
func backfillContactPhoneNormalized(tx *gorm.DB) error {
	return tx.Exec(`
		UPDATE contacts
		SET phone_normalized = regexp_replace(phone_number, '[^0-9]', '', 'g')
		WHERE (phone_normalized IS NULL OR phone_normalized = '')
		  AND phone_number NOT LIKE '%@%'`).Error
}

// backfillMessageSenderType fills sender_type on messages written before the
// column existed (plan 10, S4).
//
// The value is inferred from what those rows already record, which is the same
// information the old code used:
//   - incoming is always the customer
//   - outgoing with a campaign_id in metadata came from the campaign worker
//   - outgoing with a sent_by_user_id came from a person in the inbox (or an
//     API key, which historically set the same column — those rows are
//     indistinguishable in hindsight and are attributed to the agent)
//   - anything else outgoing was produced by the product itself
//
// New rows never rely on this inference: every send path now declares its
// sender explicitly.
func backfillMessageSenderType(tx *gorm.DB) error {
	statements := []string{
		`UPDATE messages SET sender_type = 'contact'
		 WHERE sender_type IS NULL AND direction = 'incoming'`,

		// jsonb_exists rather than the ? operator: GORM would read ? as a
		// bind placeholder and the statement would fail to prepare.
		`UPDATE messages SET sender_type = 'campaign'
		 WHERE sender_type IS NULL AND direction = 'outgoing'
		   AND jsonb_exists(metadata, 'campaign_id')`,

		`UPDATE messages SET sender_type = 'agent'
		 WHERE sender_type IS NULL AND direction = 'outgoing'
		   AND sent_by_user_id IS NOT NULL`,

		`UPDATE messages SET sender_type = 'system'
		 WHERE sender_type IS NULL`,
	}

	for _, stmt := range statements {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}
