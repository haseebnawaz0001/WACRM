package migrations

import (
	"fmt"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/models"
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
	Register(Migration{
		Name: "2026_09_24_backfill_custom_role_crm_permissions",
		Run:  backfillCustomRoleCRMPermissions,
	})
	Register(Migration{
		Name: "2026_09_25_backfill_conversation_handling",
		Run:  backfillConversationHandling,
	})
	Register(Migration{
		Name: "2026_09_26_seed_crm_default_widgets",
		Run:  seedCRMDefaultWidgets,
	})
	Register(Migration{
		Name: "2026_09_27_backfill_contact_source_field",
		Run:  backfillContactSourceField,
	})
}

// backfillContactSourceField copies contacts.source into the built-in "source"
// field (plan 01).
//
// The attribute has always been kept twice: the column, which segments filter
// on, and the field, which the CRM reports read. Only the column was written,
// so "new contacts by source" showed every existing contact as unknown while a
// segment on the same attribute matched them. New contacts are written to both
// now; this brings the history onto the same footing, and tops up any source
// option an organization is missing so the values it copies are selectable.
func backfillContactSourceField(tx *gorm.DB) error {
	if err := topUpSourceOptions(tx); err != nil {
		return err
	}

	// Only rows whose source the field actually offers are copied. A value
	// outside the option list would be invisible in the editor and unmatchable
	// by a filter, which is worse than leaving it to the column alone.
	return tx.Exec(`
		INSERT INTO custom_field_values
			(organization_id, entity_type, entity_id, field_id, value_option, created_at, updated_at)
		SELECT c.organization_id, ?, c.id, d.id, c.source, now(), now()
		FROM contacts c
		JOIN custom_field_definitions d
		  ON d.organization_id = c.organization_id
		 AND d.entity_type = ?
		 AND d.key = ?
		 AND d.deleted_at IS NULL
		WHERE c.source <> ''
		  AND c.source IS NOT NULL
		  AND c.deleted_at IS NULL
		  AND EXISTS (
			SELECT 1 FROM jsonb_array_elements(d.options) AS opt
			WHERE opt->>'value' = c.source
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM custom_field_values v
			WHERE v.entity_type = ? AND v.entity_id = c.id AND v.field_id = d.id
		  )`,
		models.FieldEntityContact, models.FieldEntityContact,
		models.FieldKeySource, models.FieldEntityContact).Error
}

// topUpSourceOptions adds any built-in source option an organization's field is
// missing, leaving the ones it has — including its own additions — untouched.
func topUpSourceOptions(tx *gorm.DB) error {
	var builtIn models.CustomFieldDefinition
	for _, field := range customfields.BuiltInFields() {
		if field.Key == models.FieldKeySource {
			builtIn = field
			break
		}
	}

	var defs []models.CustomFieldDefinition
	if err := tx.Where("entity_type = ? AND key = ?",
		models.FieldEntityContact, models.FieldKeySource).Find(&defs).Error; err != nil {
		return err
	}

	for _, def := range defs {
		options := def.Options
		added := false
		for _, raw := range builtIn.Options {
			option, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			value, _ := option["value"].(string)
			if value == "" || def.HasOption(value) {
				continue
			}
			options = append(options, option)
			added = true
		}
		if !added {
			continue
		}
		if err := tx.Model(&models.CustomFieldDefinition{}).
			Where("id = ?", def.ID).
			Update("options", options).Error; err != nil {
			return err
		}
	}
	return nil
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

// backfillConversationHandling derives the new handling column from the
// boolean it replaces (plan 10, S5).
//
// bot_active could say only "the chatbot is answering" or "it is not". The
// second case covered two different situations — an agent owns it, and nobody
// does — so the column is rebuilt from the state that actually exists: an
// assignee means a person has it, the old flag means the bot has it, and
// anything else is nobody.
func backfillConversationHandling(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn(&models.Conversation{}, "bot_active") {
		// A database created after the column was removed has nothing to
		// derive from; AutoMigrate already gave every row the 'bot' default.
		return nil
	}

	return tx.Exec(`
		UPDATE conversations
		SET handling = CASE
			WHEN assignee_id IS NOT NULL THEN 'human'
			WHEN bot_active THEN 'bot'
			ELSE 'none'
		END
		WHERE handling IS NULL OR handling = 'bot'`).Error
}
