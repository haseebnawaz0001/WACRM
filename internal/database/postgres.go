package database

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/migrations"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/orgseed"
	"github.com/zerodha/logf"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewPostgres creates a new PostgreSQL connection
func NewPostgres(cfg *config.DatabaseConfig, debug bool) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode,
	)

	logLevel := logger.Silent
	if debug {
		logLevel = logger.Info
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	return db, nil
}

// MigrationModel holds model info for migration progress
type MigrationModel struct {
	Name  string
	Model any
}

// GetMigrationModels returns all models to migrate with their names
func GetMigrationModels() []MigrationModel {
	return []MigrationModel{
		// Core models
		{"Organization", &models.Organization{}},
		{"Permission", &models.Permission{}},
		{"CustomRole", &models.CustomRole{}},
		{"RolePermissionRevocation", &models.RolePermissionRevocation{}},
		{"User", &models.User{}},
		{"UserOrganization", &models.UserOrganization{}},
		{"Team", &models.Team{}},
		{"TeamMember", &models.TeamMember{}},
		{"APIKey", &models.APIKey{}},
		{"SSOProvider", &models.SSOProvider{}},
		{"Webhook", &models.Webhook{}},
		{"CustomAction", &models.CustomAction{}},
		{"WhatsAppAccount", &models.WhatsAppAccount{}},
		{"Contact", &models.Contact{}},
		{"Tag", &models.Tag{}},
		{"Message", &models.Message{}},
		{"Template", &models.Template{}},
		{"WhatsAppFlow", &models.WhatsAppFlow{}},

		// Bulk & Notifications
		{"BulkMessageCampaign", &models.BulkMessageCampaign{}},
		{"BulkMessageRecipient", &models.BulkMessageRecipient{}},
		{"NotificationRule", &models.NotificationRule{}},

		// Chatbot models
		{"ChatbotSettings", &models.ChatbotSettings{}},
		{"KeywordRule", &models.KeywordRule{}},
		{"ChatbotFlow", &models.ChatbotFlow{}},
		// ChatbotFlowStep table is no longer managed by AutoMigrate — the
		// v2 graph runner uses ChatbotFlow.Graph exclusively. The model
		// type is retained only so BackfillChatbotFlowGraph can read
		// existing rows once during startup, then the table can be
		// dropped in a future maintenance migration.
		{"ChatbotSession", &models.ChatbotSession{}},
		{"ChatbotSessionMessage", &models.ChatbotSessionMessage{}},
		{"AIContext", &models.AIContext{}},
		{"AgentTransfer", &models.AgentTransfer{}},

		// User tracking
		{"UserAvailabilityLog", &models.UserAvailabilityLog{}},

		// Canned responses
		{"CannedResponse", &models.CannedResponse{}},

		// Catalogs
		{"Catalog", &models.Catalog{}},
		{"CatalogProduct", &models.CatalogProduct{}},

		// Dashboard
		{"Widget", &models.Widget{}},

		// Conversation Notes
		{"ConversationNote", &models.ConversationNote{}},

		// Calling / IVR
		{"CallLog", &models.CallLog{}},
		{"IVRFlow", &models.IVRFlow{}},
		{"CallTransfer", &models.CallTransfer{}},
		{"CallPermission", &models.CallPermission{}},
		{"AuditLog", &models.AuditLog{}},

		// CRM event outbox, webhook delivery log and contact timeline
		{"CRMEventOutbox", &models.CRMEventOutbox{}},
		{"WebhookDelivery", &models.WebhookDelivery{}},
		{"ContactActivity", &models.ContactActivity{}},
		{"Notification", &models.Notification{}},
		{"CustomFieldDefinition", &models.CustomFieldDefinition{}},
		{"CustomFieldValue", &models.CustomFieldValue{}},
		{"Conversation", &models.Conversation{}},
		{"ConversationRead", &models.ConversationRead{}},
		{"TaskType", &models.TaskType{}},
		{"Task", &models.Task{}},
		{"Segment", &models.Segment{}},
		{"ContactIdentity", &models.ContactIdentity{}},
		{"ContactDuplicateCandidate", &models.ContactDuplicateCandidate{}},
		{"ContactMerge", &models.ContactMerge{}},
		{"Pipeline", &models.Pipeline{}},
		{"PipelineStage", &models.PipelineStage{}},
		{"Deal", &models.Deal{}},
		{"DealStageHistory", &models.DealStageHistory{}},
		{"AutomationRule", &models.AutomationRule{}},
		{"AutomationRun", &models.AutomationRun{}},
		{"AutomationContactState", &models.AutomationContactState{}},
		{"AutomationWait", &models.AutomationWait{}},
	}
}

// AutoMigrate runs auto migration for all models (silent mode)
func AutoMigrate(db *gorm.DB) error {
	migrationModels := GetMigrationModels()
	for _, m := range migrationModels {
		if err := db.AutoMigrate(m.Model); err != nil {
			return err
		}
	}
	return nil
}

// RunMigrationWithProgress runs migrations with a progress bar display
func RunMigrationWithProgress(db *gorm.DB, adminCfg *config.DefaultAdminConfig) error {
	// Silence GORM logging during migration
	silentDB := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})

	migrationModels := GetMigrationModels()
	indexes := getIndexes()

	// Total steps: models + indexes + default admin check
	totalSteps := len(migrationModels) + len(indexes) + 1
	currentStep := 0
	barWidth := 40

	printProgress := func(step int, total int) {
		percent := float64(step) / float64(total)
		filled := int(percent * float64(barWidth))
		empty := barWidth - filled

		bar := repeatChar("█", filled) + "\033[90m" + repeatChar("░", empty) + "\033[0m"
		fmt.Printf("\r  Running migrations  %s %3d%%", bar, int(percent*100))
		_ = os.Stdout.Sync()
	}

	fmt.Println()

	// Migrate models
	for _, m := range migrationModels {
		printProgress(currentStep, totalSteps)
		if err := silentDB.AutoMigrate(m.Model); err != nil {
			fmt.Printf("\n  \033[31m✗ Migration failed: %s\033[0m\n\n", m.Name)
			return fmt.Errorf("failed to migrate %s: %w", m.Name, err)
		}
		currentStep++
	}

	// Create indexes
	for _, idx := range indexes {
		printProgress(currentStep, totalSteps)
		if err := silentDB.Exec(idx).Error; err != nil {
			fmt.Printf("\n  \033[31m✗ Index creation failed\033[0m\n\n")
			return fmt.Errorf("failed to create index: %w", err)
		}
		currentStep++
	}

	// Seed permissions (always run, will skip if already seeded)
	printProgress(currentStep, totalSteps)
	if err := SeedPermissionsAndRoles(silentDB); err != nil {
		fmt.Printf("\n  \033[31m✗ Failed to seed permissions\033[0m\n\n")
		return err
	}

	// Fix existing organizations - link permissions to system roles if missing
	if err := SeedSystemRolesForAllOrgs(silentDB); err != nil {
		fmt.Printf("\n  \033[31m✗ Failed to fix existing role permissions\033[0m\n\n")
		return err
	}

	// Backfill user_organizations from existing users
	if err := MigrateUserOrganizations(silentDB); err != nil {
		fmt.Printf("\n  \033[31m✗ Failed to backfill user organizations\033[0m\n\n")
		return err
	}

	// Create default admin (only runs if no users exist)
	printProgress(currentStep, totalSteps)
	if err := CreateDefaultAdmin(silentDB, adminCfg); err != nil {
		fmt.Printf("\n  \033[31m✗ Setup failed\033[0m\n\n")
		return err
	}
	currentStep++

	// Seed default widgets for all organizations
	printProgress(currentStep, totalSteps)
	if err := SeedDefaultWidgets(silentDB); err != nil {
		fmt.Printf("\n  \033[31m✗ Failed to seed widgets\033[0m\n\n")
		return err
	}

	// Backfill last_inbound_at from existing messages
	if err := BackfillLastInboundAt(silentDB); err != nil {
		fmt.Printf("\n  \033[31m✗ Failed to backfill last_inbound_at\033[0m\n\n")
		return err
	}

	// Versioned run-once data migrations (plan 00, F1). These run last, after
	// the schema and the seeds they may depend on are in place.
	if err := migrations.RunPending(silentDB, migrationLogger()); err != nil {
		fmt.Printf("\n  \033[31m✗ Data migration failed\033[0m\n\n")
		return err
	}

	printProgress(currentStep, totalSteps)
	fmt.Printf("\n  \033[32m✓ Migration completed\033[0m\n\n")

	return nil
}

// migrationLogger returns a quiet logger for data migrations so their output
// does not break the progress bar; failures are reported by the caller.
func migrationLogger() logf.Logger {
	return logf.New(logf.Opts{Level: logf.InfoLevel, TimestampFormat: "2006-01-02 15:04:05"})
}

// repeatChar repeats a character n times
func repeatChar(char string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += char
	}
	return result
}

// getIndexes returns all index creation SQL statements
func getIndexes() []string {
	return []string{
		// Expand phone_number columns to support group JIDs (e.g., 120363422675615917@g.us)
		`ALTER TABLE contacts ALTER COLUMN phone_number TYPE varchar(50)`,
		`ALTER TABLE chatbot_sessions ALTER COLUMN phone_number TYPE varchar(50)`,
		`ALTER TABLE agent_transfers ALTER COLUMN phone_number TYPE varchar(50)`,
		`ALTER TABLE bulk_message_recipients ALTER COLUMN phone_number TYPE varchar(50)`,
		// Indexes
		`CREATE INDEX IF NOT EXISTS idx_messages_contact_created ON messages(contact_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id)`,
		// One live contact per phone number. The index deliberately excludes
		// soft-deleted rows: a deleted contact used to keep ownership of its
		// number, so the number could never be used again — an import or an
		// inbound message from it failed with a unique-constraint error, and
		// the contact lifecycle's "create a fresh record" path was impossible.
		// Deleted rows may now share a number with the live contact that
		// replaced them.
		`DROP INDEX IF EXISTS idx_contacts_org_phone`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_contacts_org_phone_live ON contacts(organization_id, phone_number) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_contacts_assigned_read ON contacts(assigned_user_id, is_read)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_phone_status ON chatbot_sessions(organization_id, phone_number, status)`,
		`CREATE INDEX IF NOT EXISTS idx_keyword_rules_priority ON keyword_rules(organization_id, is_enabled, priority DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_transfers_active ON agent_transfers(organization_id, phone_number, status)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_transfers_org_contact ON agent_transfers(organization_id, contact_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_transfers_agent_active ON agent_transfers(agent_id, status) WHERE status = 'active'`,
		`CREATE INDEX IF NOT EXISTS idx_agent_transfers_team ON agent_transfers(team_id, status) WHERE team_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_accounts_org_phone ON whatsapp_accounts(organization_id, phone_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_templates_account_name_lang ON templates(whats_app_account, name, language)`,
		`CREATE INDEX IF NOT EXISTS idx_keyword_rules_account ON keyword_rules(whats_app_account, is_enabled, priority DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_chatbot_flows_account ON chatbot_flows(whats_app_account, is_enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_contexts_account ON ai_contexts(whats_app_account, is_enabled, priority DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_bulk_campaigns_account ON bulk_message_campaigns(whats_app_account, status)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_rules_account ON notification_rules(whats_app_account, is_enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_account ON messages(whats_app_account, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_contacts_account ON contacts(whats_app_account)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_canned_responses_org_name ON canned_responses(organization_id, name)`,
		`CREATE INDEX IF NOT EXISTS idx_canned_responses_active ON canned_responses(organization_id, is_active, usage_count DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_webhooks_org_active ON webhooks(organization_id, is_active)`,
		`CREATE INDEX IF NOT EXISTS idx_availability_logs_user_time ON user_availability_logs(user_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_availability_logs_org_time ON user_availability_logs(organization_id, started_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sso_providers_org_provider ON sso_providers(organization_id, provider)`,
		// Teams indexes
		`CREATE INDEX IF NOT EXISTS idx_teams_org_active ON teams(organization_id, is_active)`,
		// Create partial unique index (soft-deleted members)
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_team_members_unique ON team_members(team_id, user_id) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_team_members_user ON team_members(user_id)`,
		// Custom roles indexes
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_custom_roles_org_name ON custom_roles(organization_id, name)`,
		`CREATE INDEX IF NOT EXISTS idx_custom_roles_org_system ON custom_roles(organization_id, is_system)`,
		`CREATE INDEX IF NOT EXISTS idx_custom_roles_org_default ON custom_roles(organization_id, is_default) WHERE is_default = true`,
		// GIN index for JSONB tag filtering
		`CREATE INDEX IF NOT EXISTS idx_contacts_tags ON contacts USING GIN (tags)`,
		// User organizations
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_org_unique ON user_organizations(user_id, organization_id) WHERE deleted_at IS NULL`,
		// Conversation notes
		`CREATE INDEX IF NOT EXISTS idx_conversation_notes_contact ON conversation_notes(organization_id, contact_id, created_at DESC)`,
		// Call logs
		`CREATE INDEX IF NOT EXISTS idx_call_logs_org_status ON call_logs(organization_id, status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_call_logs_contact ON call_logs(contact_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_call_logs_wa_call_id ON call_logs(whatsapp_call_id) WHERE whatsapp_call_id != ''`,
		// IVR flows
		`CREATE INDEX IF NOT EXISTS idx_ivr_flows_org_active ON ivr_flows(organization_id, whatsapp_account, is_active)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ivr_flows_org_call_start ON ivr_flows(organization_id, whatsapp_account) WHERE is_call_start = true AND is_active = true AND deleted_at IS NULL`,
		// One active transfer per contact (plan 10, S5 / X7). This was checked
		// by counting rows and then inserting, which two concurrent inbound
		// webhooks — each running in its own goroutine — could both pass,
		// leaving a contact with two active transfers and an ambiguous
		// assignee. The index makes the rule the database's job.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transfers_one_active ON agent_transfers(organization_id, contact_id) WHERE status = 'active' AND deleted_at IS NULL`,
		// CRM event outbox. The relay only ever scans unpublished rows, so a
		// partial index keeps the claim query off the published backlog.
		`CREATE INDEX IF NOT EXISTS idx_crm_event_outbox_unpublished ON crm_event_outbox(occurred_at) WHERE published_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_crm_event_outbox_prune ON crm_event_outbox(published_at) WHERE published_at IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_crm_event_outbox_org_contact ON crm_event_outbox(organization_id, contact_id, occurred_at DESC)`,
		// Contact timeline. The composite ordering matches the keyset the
		// timeline pages with, so scrolling never falls back to a sort.
		`CREATE INDEX IF NOT EXISTS idx_contact_activities_timeline ON contact_activities(organization_id, contact_id, occurred_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_contact_activities_type ON contact_activities(organization_id, type, occurred_at DESC)`,
		// Notification bell: the unread count and the list both filter by
		// user and read state, so they share one index.
		`CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, organization_id, read_at, created_at DESC)`,
		// Custom fields. The key is unique per org and entity so filters and
		// templates can refer to a field by name unambiguously.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cfd_org_entity_key ON custom_field_definitions(organization_id, entity_type, key) WHERE deleted_at IS NULL`,
		// One value row per (entity, field): this is what the upsert in
		// SetValues conflicts on, so concurrent edits converge instead of
		// creating a second value for the same field.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_cfv_entity_field ON custom_field_values(entity_type, entity_id, field_id)`,
		// Typed partial indexes: a filter on one field only ever scans the
		// column that field actually uses.
		`CREATE INDEX IF NOT EXISTS idx_cfv_text ON custom_field_values(organization_id, field_id, lower(value_text)) WHERE value_text IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_cfv_number ON custom_field_values(organization_id, field_id, value_number) WHERE value_number IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_cfv_date ON custom_field_values(organization_id, field_id, value_date) WHERE value_date IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_cfv_option ON custom_field_values(organization_id, field_id, value_option) WHERE value_option IS NOT NULL`,
		// Exactly one active conversation per contact. Two inbound webhooks
		// for the same contact run in separate goroutines, so without this the
		// service's advisory lock would be the only thing preventing a
		// duplicate — and a code path that forgot to take it would create one.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_one_active ON conversations(organization_id, contact_id) WHERE status <> 'resolved' AND deleted_at IS NULL`,
		// Inbox views: status plus assignee or team, newest first.
		`CREATE INDEX IF NOT EXISTS idx_conversations_inbox ON conversations(organization_id, status, assignee_id, last_message_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_team ON conversations(organization_id, status, team_id, last_message_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_snoozed ON conversations(snoozed_until) WHERE status = 'snoozed'`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_contact ON conversations(contact_id, opened_at DESC)`,
		// Tasks. The partial indexes match the queries that run constantly:
		// one agent's open list, and the two notifier scans.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_task_types_org_key ON task_types(organization_id, key) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_owner_open ON tasks(organization_id, owner_id, due_at) WHERE status = 'open' AND deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_contact ON tasks(contact_id, status, due_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_reminder_due ON tasks(remind_at) WHERE status = 'open' AND reminder_sent_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_overdue_scan ON tasks(due_at) WHERE status = 'open' AND overdue_notified_at IS NULL`,
		// Segments. Names are unique case-insensitively so "VIP" and "vip"
		// cannot both exist and be picked from a list by mistake.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_segments_org_name ON segments(organization_id, lower(name)) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_segments_org ON segments(organization_id, visibility, created_by_id)`,
		// Merge and duplicate detection (plan 06).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_contact_identities_unique ON contact_identities(organization_id, type, normalized)`,
		`CREATE INDEX IF NOT EXISTS idx_contact_identities_contact ON contact_identities(contact_id)`,
		// The pair is stored lowest-id-first, so this index actually prevents
		// the same pair being recorded twice in opposite orders.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_dup_pair ON contact_duplicate_candidates(organization_id, contact_a_id, contact_b_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dup_pending ON contact_duplicate_candidates(organization_id, status, score DESC)`,
		// Pipelines and deals (plan 07).
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_pipelines_org_name ON pipelines(organization_id, lower(name)) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_pipeline_stages_pipeline ON pipeline_stages(pipeline_id, position)`,
		`CREATE INDEX IF NOT EXISTS idx_deals_board ON deals(organization_id, pipeline_id, stage_id, board_position) WHERE deleted_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_deals_owner ON deals(organization_id, owner_id, status, expected_close_date)`,
		`CREATE INDEX IF NOT EXISTS idx_deals_contact ON deals(contact_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_deal_stage_history_deal ON deal_stage_history(deal_id, created_at)`,

		// Automation (plan 08). The unique run index is what makes a redelivered
		// event a no-op instead of a second message to the customer.
		`CREATE INDEX IF NOT EXISTS idx_automation_rules_trigger ON automation_rules(organization_id, trigger_type) WHERE enabled AND deleted_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_automation_runs_idem ON automation_runs(rule_id, event_id) WHERE dry_run = false`,
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_rule ON automation_runs(rule_id, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_automation_runs_contact ON automation_runs(contact_id, started_at DESC)`,

		// CRM reports (plan 09). Every report is a live aggregate rather than a
		// rollup, so the columns they filter and bucket on have to be indexed.
		`CREATE INDEX IF NOT EXISTS idx_conversations_first_response ON conversations(organization_id, first_response_at)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_resolved ON conversations(organization_id, resolved_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_owner_due ON tasks(organization_id, owner_id, status, due_at)`,
		`CREATE INDEX IF NOT EXISTS idx_contact_activities_type_time ON contact_activities(organization_id, type, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_deal_stage_history_org_time ON deal_stage_history(organization_id, created_at)`,
	}
}

// CreateIndexes creates additional indexes not handled by GORM tags
func CreateIndexes(db *gorm.DB) error {
	for _, idx := range getIndexes() {
		if err := db.Exec(idx).Error; err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}
	return nil
}

// CreateDefaultAdmin creates a default admin user if no users exist
// This should only be called once during initial setup
func CreateDefaultAdmin(db *gorm.DB, cfg *config.DefaultAdminConfig) error {
	// Check if admin already exists (using email from config)
	var existingAdmin models.User
	if err := db.Where("email = ?", cfg.Email).First(&existingAdmin).Error; err == nil {
		// Admin already exists, skip
		return nil
	}

	// Find any existing organization, or create "Default Organization" if none exist
	var org models.Organization
	if err := db.First(&org).Error; err != nil {
		// No organizations exist, create default one
		org = models.Organization{
			BaseModel: models.BaseModel{ID: uuid.New()},
			Name:      "Default Organization",
			Settings:  models.JSONB{},
		}
		if err := db.Create(&org).Error; err != nil {
			return fmt.Errorf("failed to create default organization: %w", err)
		}
	}

	// Hash the default password from config
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(cfg.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Seed permissions if not exist
	if err := SeedPermissionsAndRoles(db); err != nil {
		return fmt.Errorf("failed to seed permissions: %w", err)
	}

	// Seed system roles for this organization if not exist
	if err := SeedSystemRolesForOrg(db, org.ID); err != nil {
		return fmt.Errorf("failed to seed system roles: %w", err)
	}

	// The CRM defaults a first-run organization needs (plan 10, S8).
	if err := orgseed.Seed(db, org.ID); err != nil {
		return fmt.Errorf("failed to seed organization defaults: %w", err)
	}

	// Get admin system role for the organization
	var adminRole models.CustomRole
	if err := db.Where("organization_id = ? AND name = ? AND is_system = ?", org.ID, "admin", true).First(&adminRole).Error; err != nil {
		return fmt.Errorf("failed to find admin role: %w", err)
	}

	// Create default admin user (super admin for cross-organization access)
	admin := models.User{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		OrganizationID: org.ID,
		Email:          cfg.Email,
		PasswordHash:   string(passwordHash),
		FullName:       cfg.FullName,
		RoleID:         &adminRole.ID,
		IsActive:       true,
		IsAvailable:    true,
		IsSuperAdmin:   true,
		Settings:       models.JSONB{},
	}
	if err := db.Create(&admin).Error; err != nil {
		return fmt.Errorf("failed to create default admin user: %w", err)
	}

	// Create UserOrganization entry for the default admin
	userOrg := models.UserOrganization{
		BaseModel:      models.BaseModel{ID: uuid.New()},
		UserID:         admin.ID,
		OrganizationID: org.ID,
		RoleID:         &adminRole.ID,
		IsDefault:      true,
	}
	if err := db.Create(&userOrg).Error; err != nil {
		return fmt.Errorf("failed to create user organization entry: %w", err)
	}

	return nil
}

// MigrateUserOrganizations backfills user_organizations from existing users
func MigrateUserOrganizations(db *gorm.DB) error {
	return db.Exec(`
		INSERT INTO user_organizations (id, user_id, organization_id, role_id, is_default, created_at, updated_at)
		SELECT gen_random_uuid(), u.id, u.organization_id, u.role_id, true, NOW(), NOW()
		FROM users u
		LEFT JOIN user_organizations uo ON uo.user_id = u.id AND uo.organization_id = u.organization_id AND uo.deleted_at IS NULL
		WHERE uo.id IS NULL AND u.deleted_at IS NULL
	`).Error
}

// BackfillLastInboundAt sets last_inbound_at for existing contacts from their
// most recent incoming message. Only updates contacts where the field is NULL.
func BackfillLastInboundAt(db *gorm.DB) error {
	return db.Exec(`
		UPDATE contacts c
		SET last_inbound_at = sub.max_created
		FROM (
			SELECT contact_id, MAX(created_at) AS max_created
			FROM messages
			WHERE direction = 'incoming' AND deleted_at IS NULL
			GROUP BY contact_id
		) sub
		WHERE c.id = sub.contact_id AND c.last_inbound_at IS NULL AND c.deleted_at IS NULL
	`).Error
}

// SeedPermissionsAndRoles seeds the default permissions and system roles
func SeedPermissionsAndRoles(db *gorm.DB) error {
	// Get all default permissions
	defaultPerms := models.DefaultPermissions()

	// Add any missing permissions
	for _, perm := range defaultPerms {
		// The group is derived rather than listed, so a permission added to
		// the catalog cannot silently arrive ungrouped (plan 10, S1).
		perm.Group = models.PermissionGroupFor(perm.Resource)

		var existing models.Permission
		if err := db.Where("resource = ? AND action = ?", perm.Resource, perm.Action).First(&existing).Error; err != nil {
			// Permission doesn't exist, create it
			perm.ID = uuid.New()
			if err := db.Create(&perm).Error; err != nil {
				return fmt.Errorf("failed to create permission %s:%s: %w", perm.Resource, perm.Action, err)
			}
			continue
		}

		// Existing rows are regrouped in place. Grouping is presentation, not
		// authorisation, so correcting it on an installed system is safe and
		// is the only way a regrouping ever reaches one.
		if existing.Group != perm.Group {
			if err := db.Model(&models.Permission{}).Where("id = ?", existing.ID).
				Update("group", perm.Group).Error; err != nil {
				return fmt.Errorf("failed to group permission %s:%s: %w", perm.Resource, perm.Action, err)
			}
		}
	}

	return nil
}

// SeedSystemRolesForAllOrgs creates system roles for all existing organizations
// This is idempotent - it skips organizations that already have system roles
func SeedSystemRolesForAllOrgs(db *gorm.DB) error {
	var orgs []models.Organization
	if err := db.Find(&orgs).Error; err != nil {
		return fmt.Errorf("failed to fetch organizations: %w", err)
	}

	for _, org := range orgs {
		if err := SeedSystemRolesForOrg(db, org.ID); err != nil {
			return fmt.Errorf("failed to seed roles for org %s: %w", org.ID, err)
		}
	}

	// Fix any system roles that don't have permissions linked
	if err := FixSystemRolePermissions(db); err != nil {
		return fmt.Errorf("failed to fix role permissions: %w", err)
	}

	// Migrate existing users from old role column to new role_id
	if err := MigrateExistingUserRoles(db); err != nil {
		return fmt.Errorf("failed to migrate user roles: %w", err)
	}

	// Make admin@admin.com a super admin if exists
	if err := db.Exec("UPDATE users SET is_super_admin = true WHERE email = 'admin@admin.com'").Error; err != nil {
		return fmt.Errorf("failed to set super admin: %w", err)
	}

	return nil
}

// FixSystemRolePermissions links permissions to existing system roles that have no permissions
func FixSystemRolePermissions(db *gorm.DB) error {
	// Get all permissions from database
	var permissions []models.Permission
	if err := db.Find(&permissions).Error; err != nil {
		return fmt.Errorf("failed to fetch permissions: %w", err)
	}

	if len(permissions) == 0 {
		return nil // No permissions to link
	}

	// Create permission map for lookup
	permMap := make(map[string]models.Permission)
	for _, p := range permissions {
		permMap[p.Resource+":"+p.Action] = p
	}

	// Get system role permission mappings
	rolePermissions := models.SystemRolePermissions()

	// Find system roles without permissions
	var systemRoles []models.CustomRole
	if err := db.Where("is_system = ?", true).Find(&systemRoles).Error; err != nil {
		return fmt.Errorf("failed to fetch system roles: %w", err)
	}

	for _, role := range systemRoles {
		// Check if role has permissions
		var permCount int64
		db.Table("role_permissions").Where("custom_role_id = ?", role.ID).Count(&permCount)

		if permCount > 0 {
			continue // Already has permissions, don't overwrite customizations
		}

		// Get the permission keys for this role
		permKeys, ok := rolePermissions[role.Name]
		if !ok {
			continue // Unknown role name
		}

		// Link permissions to role
		var permsToAdd []models.Permission
		for _, key := range permKeys {
			if perm, ok := permMap[key]; ok {
				permsToAdd = append(permsToAdd, perm)
			}
		}

		if len(permsToAdd) > 0 {
			if err := db.Model(&role).Association("Permissions").Replace(permsToAdd); err != nil {
				return fmt.Errorf("failed to link permissions to role %s: %w", role.Name, err)
			}
		}
	}

	return grantNewPermissionsToAdmins(db, systemRoles, permissions)
}

// grantNewPermissionsToAdmins gives every admin system role the permissions it
// does not have yet.
//
// The loop above deliberately skips any role that already has permissions, so
// customised roles are not reset. That is right for manager and agent, whose
// permission sets are curated, but it means a permission added to the catalog
// after an organization was created never reaches its admin — and a feature
// gated on that permission is then unreachable for everyone rather than merely
// restricted. tags:import/export shipped in exactly that state (plan 10, X11).
//
// Only the admin role is topped up, and only by adding: "admin" is defined as
// full system access, so a permission it lacks is a bug rather than a choice.
func grantNewPermissionsToAdmins(db *gorm.DB, systemRoles []models.CustomRole, all []models.Permission) error {
	for _, role := range systemRoles {
		if role.Name != "admin" {
			continue
		}

		var held []uuid.UUID
		if err := db.Table("role_permissions").Where("custom_role_id = ?", role.ID).
			Pluck("permission_id", &held).Error; err != nil {
			return fmt.Errorf("failed to read permissions for role %s: %w", role.ID, err)
		}
		have := make(map[uuid.UUID]bool, len(held))
		for _, id := range held {
			have[id] = true
		}

		var missing []models.Permission
		for _, p := range all {
			if !have[p.ID] {
				missing = append(missing, p)
			}
		}
		if len(missing) == 0 {
			continue
		}

		if err := db.Model(&role).Association("Permissions").Append(missing); err != nil {
			return fmt.Errorf("failed to grant new permissions to admin role %s: %w", role.ID, err)
		}
	}

	return nil
}

// MigrateExistingUserRoles migrates users from the old role column to the new role_id
// This is safe to run on fresh installs - it will simply do nothing if the column doesn't exist
func MigrateExistingUserRoles(db *gorm.DB) error {
	// Check if the old 'role' column exists in the users table
	var columnExists bool
	err := db.Raw(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'users' AND column_name = 'role'
		)
	`).Scan(&columnExists).Error
	if err != nil {
		return fmt.Errorf("failed to check for role column: %w", err)
	}

	if !columnExists {
		return nil // Fresh install, no old role column
	}

	// Get users who have old role but no role_id assigned
	type UserWithLegacyRole struct {
		ID             uuid.UUID
		OrganizationID uuid.UUID
		LegacyRole     string
	}

	var usersToMigrate []UserWithLegacyRole
	err = db.Raw(`
		SELECT id, organization_id, role as legacy_role
		FROM users
		WHERE role_id IS NULL AND role IS NOT NULL AND role != ''
	`).Scan(&usersToMigrate).Error
	if err != nil {
		return fmt.Errorf("failed to fetch users with legacy roles: %w", err)
	}

	if len(usersToMigrate) == 0 {
		return nil // No users to migrate
	}

	// Get all system roles grouped by organization
	var systemRoles []models.CustomRole
	if err := db.Where("is_system = ?", true).Find(&systemRoles).Error; err != nil {
		return fmt.Errorf("failed to fetch system roles: %w", err)
	}

	// Create lookup: orgID -> roleName -> roleID
	roleMap := make(map[uuid.UUID]map[string]uuid.UUID)
	for _, role := range systemRoles {
		if roleMap[role.OrganizationID] == nil {
			roleMap[role.OrganizationID] = make(map[string]uuid.UUID)
		}
		roleMap[role.OrganizationID][role.Name] = role.ID
	}

	// Migrate each user
	for _, user := range usersToMigrate {
		orgRoles, ok := roleMap[user.OrganizationID]
		if !ok {
			continue // Organization doesn't have system roles yet
		}

		roleID, ok := orgRoles[user.LegacyRole]
		if !ok {
			continue // Role not found (shouldn't happen for admin/manager/agent)
		}

		// Update user's role_id
		if err := db.Exec("UPDATE users SET role_id = ? WHERE id = ?", roleID, user.ID).Error; err != nil {
			return fmt.Errorf("failed to update user %s role: %w", user.ID, err)
		}
	}

	return nil
}

// SeedSystemRolesForOrg creates system roles for an organization
func SeedSystemRolesForOrg(db *gorm.DB, orgID uuid.UUID) error {
	// Check if system roles exist for this org
	var roleCount int64
	if err := db.Model(&models.CustomRole{}).Where("organization_id = ? AND is_system = ?", orgID, true).Count(&roleCount).Error; err != nil {
		return fmt.Errorf("failed to count roles: %w", err)
	}

	if roleCount > 0 {
		return nil // Already seeded
	}

	// Get all permissions from database
	var permissions []models.Permission
	if err := db.Find(&permissions).Error; err != nil {
		return fmt.Errorf("failed to fetch permissions: %w", err)
	}

	// Create permission map for lookup
	permMap := make(map[string]models.Permission)
	for _, p := range permissions {
		permMap[p.Resource+":"+p.Action] = p
	}

	// Get system role permission mappings
	rolePermissions := models.SystemRolePermissions()

	// Create system roles
	systemRoles := []struct {
		Name        string
		Description string
		IsDefault   bool
	}{
		{"admin", "Full system access", false},
		{"manager", "Manage chatbot, campaigns, and team operations", false},
		{"agent", "Handle customer conversations", true},
	}

	for _, sr := range systemRoles {
		role := models.CustomRole{
			BaseModel:      models.BaseModel{ID: uuid.New()},
			OrganizationID: orgID,
			Name:           sr.Name,
			Description:    sr.Description,
			IsSystem:       true,
			IsDefault:      sr.IsDefault,
		}

		// Add permissions
		permKeys := rolePermissions[sr.Name]
		for _, key := range permKeys {
			if perm, ok := permMap[key]; ok {
				role.Permissions = append(role.Permissions, perm)
			}
		}

		if err := db.Create(&role).Error; err != nil {
			return fmt.Errorf("failed to create %s role: %w", sr.Name, err)
		}
	}

	return nil
}

// SeedDefaultWidgets creates default dashboard widgets for all organizations
func SeedDefaultWidgets(db *gorm.DB) error {
	// Find the super admin user (admin@admin.com)
	var superAdmin models.User
	if err := db.Where("email = ?", "admin@admin.com").First(&superAdmin).Error; err != nil {
		// No super admin exists yet, skip widget creation
		return nil
	}

	// Get all organizations
	var orgs []models.Organization
	if err := db.Find(&orgs).Error; err != nil {
		return fmt.Errorf("failed to fetch organizations: %w", err)
	}

	for _, org := range orgs {
		// Skip orgs that already have widgets
		var exists int64
		db.Model(&models.Widget{}).Where("organization_id = ?", org.ID).Count(&exists)
		if exists > 0 {
			continue
		}

		if err := SeedDefaultWidgetsForOrg(db, org.ID, superAdmin.ID); err != nil {
			return err
		}
	}

	return nil
}

// SeedDefaultWidgetsForOrg creates default dashboard widgets for a single organization.
// Used when a new organization is created at runtime.
func SeedDefaultWidgetsForOrg(db *gorm.DB, orgID, userID uuid.UUID) error {
	for i, spec := range models.DefaultDashboard() {
		widget := spec.Widget()
		widget.ID = uuid.New()
		widget.OrganizationID = orgID
		widget.UserID = &userID
		widget.DisplayOrder = i + 1
		if err := db.Create(&widget).Error; err != nil {
			return fmt.Errorf("failed to create widget %s: %w", spec.Name, err)
		}
	}
	return nil
}
