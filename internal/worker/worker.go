package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shridarpatil/whatomate/internal/config"
	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/messaging"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/queue"
	"github.com/shridarpatil/whatomate/internal/templateutil"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/zerodha/logf"
	"gorm.io/gorm"
)

// Worker processes jobs from the queue
type Worker struct {
	Config    *config.Config
	DB        *gorm.DB
	Redis     *redis.Client
	Log       logf.Logger
	WhatsApp  *whatsapp.Client
	Consumer  *queue.RedisConsumer
	Publisher *queue.Publisher
}

// Ensure Worker implements JobHandler interface
var _ queue.JobHandler = (*Worker)(nil)

// New creates a new Worker instance
func New(cfg *config.Config, db *gorm.DB, rdb *redis.Client, log logf.Logger) (*Worker, error) {
	consumer, err := queue.NewRedisConsumer(rdb, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer: %w", err)
	}

	publisher := queue.NewPublisher(rdb, log)

	return &Worker{
		Config:    cfg,
		DB:        db,
		Redis:     rdb,
		Log:       log,
		WhatsApp:  whatsapp.New(log),
		Consumer:  consumer,
		Publisher: publisher,
	}, nil
}

// Run starts the worker and processes jobs until context is cancelled
func (w *Worker) Run(ctx context.Context) error {
	w.Log.Info("Worker starting")

	err := w.Consumer.Consume(ctx, w)
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("consumer error: %w", err)
	}

	w.Log.Info("Worker stopped")
	return nil
}

// HandleRecipientJob processes a single recipient message job
func (w *Worker) HandleRecipientJob(ctx context.Context, job *queue.RecipientJob) error {
	// Check if campaign is still active before sending
	var campaign models.BulkMessageCampaign
	if err := w.DB.Where("id = ?", job.CampaignID).Preload("Template").First(&campaign).Error; err != nil {
		w.Log.Error("Failed to load campaign", "error", err, "campaign_id", job.CampaignID)
		return fmt.Errorf("failed to load campaign: %w", err)
	}

	// Skip if campaign is paused or cancelled
	if campaign.Status == models.CampaignStatusPaused || campaign.Status == models.CampaignStatusCancelled {
		w.Log.Info("Campaign not active, skipping recipient", "campaign_id", job.CampaignID, "status", campaign.Status, "recipient_id", job.RecipientID)
		return nil // Not an error, just skip
	}

	// Get WhatsApp account
	var account models.WhatsAppAccount
	if err := w.DB.Where("name = ? AND organization_id = ?", campaign.WhatsAppAccount, job.OrganizationID).First(&account).Error; err != nil {
		w.Log.Error("Failed to load WhatsApp account", "error", err, "account_name", campaign.WhatsAppAccount)
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusFailed, "", "WhatsApp account not found")
		w.incrementCampaignCount(job.CampaignID, "failed_count")
		return nil // Don't retry, mark as failed
	}
	w.decryptAccountSecrets(&account)

	// Get or create contact for this recipient
	// A campaign send must never resurrect a deleted contact, and the
	// recipient name from the CSV must never overwrite the name WhatsApp
	// reported for that person.
	contact, _, err := contacts.New(w.DB).Resolve(ctx, job.OrganizationID,
		contacts.Identity{Phone: job.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing: true,
			AllowRestore:    false,
			UpdateName:      false,
			Source:          contacts.SourceCampaign,
			ProfileName:     job.RecipientName,
			Actor:           crmevents.SystemActor(),
		})
	if err != nil || contact == nil {
		w.Log.Error("Failed to get or create contact", "error", err, "phone", job.PhoneNumber)
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusFailed, "", "Failed to create contact")
		w.incrementCampaignCount(job.CampaignID, "failed_count")
		return nil // Don't retry
	}

	// A template deleted after the campaign's jobs were queued comes back from
	// Preload as nil, and sendTemplateMessage dereferences it immediately. The
	// scheduler checks this when it starts a campaign, but a delete that lands
	// mid-run reaches the worker instead, where a panic would take down every
	// other job on the same worker (plan 10, X6).
	if campaign.Template == nil {
		w.Log.Error("Campaign template is missing, failing recipient",
			"campaign_id", job.CampaignID, "recipient", job.PhoneNumber)
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusFailed, "", "Campaign template no longer exists")
		w.incrementCampaignCount(job.CampaignID, "failed_count")
		w.checkCampaignCompletion(ctx, job.CampaignID, job.OrganizationID)
		return nil // Don't retry: the template will not come back.
	}

	// The same rules every other send path applies (plan 00, F10): approval,
	// marketing consent, and a reachable address. Each of these used to be
	// written out per caller, and they had drifted — the worker checked
	// approval only at campaign start, so a template Meta unapproved in the
	// meantime produced a rejected send instead of a stated reason.
	//
	// Parameters are not checked here: they are filled per recipient below.
	if err := messaging.CheckTemplate(campaign.Template, contact, nil); err != nil {
		w.Log.Info("Skipping recipient", "contact_id", contact.ID,
			"phone", job.PhoneNumber, "reason", err)
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusFailed, "", err.Error())
		w.incrementCampaignCount(job.CampaignID, "failed_count")
		// Permanent: none of these become true by retrying.
		return nil
	}

	// Build recipient for sending
	recipient := &models.BulkMessageRecipient{
		PhoneNumber:    job.PhoneNumber,
		RecipientName:  job.RecipientName,
		TemplateParams: job.TemplateParams,
		HeaderParams:   job.HeaderParams,
	}

	// Build the message record. sender_type marks this as a campaign send so
	// it never counts towards first-response or agent analytics.
	message := models.Message{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  job.OrganizationID,
		WhatsAppAccount: campaign.WhatsAppAccount,
		ContactID:       contact.ID,
		Direction:       models.DirectionOutgoing,
		SenderType:      models.SenderCampaign,
		MessageType:     models.MessageTypeTemplate,
		TemplateParams:  job.TemplateParams,
		Status:          models.MessageStatusPending,
		Metadata: models.JSONB{
			"campaign_id":    job.CampaignID.String(),
			"recipient_name": job.RecipientName,
		},
	}
	message.TemplateName = campaign.Template.Name
	message.Content = templateutil.ReplaceWithJSONBParams(
		campaign.Template.BodyContent, campaign.Template.BodyContent, job.TemplateParams)
	// Store campaign header media so it renders in the chat bubble
	if campaign.HeaderMediaLocalPath != "" {
		message.MediaURL = campaign.HeaderMediaLocalPath
		message.MediaMimeType = campaign.HeaderMediaMimeType
		message.MediaFilename = campaign.HeaderMediaFilename
	}

	// Persist before calling Meta, the same order SendOutgoingMessage uses.
	// Creating the row afterwards left a window where a delivery status
	// webhook could arrive for a message that did not exist yet and be
	// dropped.
	if err := w.DB.Create(&message).Error; err != nil {
		w.Log.Error("Failed to save message", "error", err, "recipient", job.PhoneNumber)
	}

	// Send template message
	waMessageID, err := w.sendTemplateMessage(ctx, &account, campaign.Template, recipient, campaign.HeaderMediaID, campaign.HeaderMediaFilename)

	if err != nil {
		w.Log.Error("Failed to send message", "error", err, "recipient", job.PhoneNumber)
		message.Status = models.MessageStatusFailed
		message.ErrorMessage = err.Error()
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusFailed, "", err.Error())
		w.incrementCampaignCount(job.CampaignID, "failed_count")
	} else {
		w.Log.Info("Message sent", "recipient", job.PhoneNumber, "message_id", waMessageID)
		message.Status = models.MessageStatusSent
		message.WhatsAppMessageID = waMessageID
		w.updateRecipientStatus(job.RecipientID, models.MessageStatusSent, waMessageID, "")
		w.incrementCampaignCount(job.CampaignID, "sent_count")
	}

	if err := w.DB.Model(&models.Message{}).Where("id = ?", message.ID).
		Updates(map[string]any{
			"status":               message.Status,
			"whats_app_message_id": message.WhatsAppMessageID,
			"error_message":        message.ErrorMessage,
		}).Error; err != nil {
		w.Log.Error("Failed to update message status", "error", err, "message_id", message.ID)
	}

	// Side effects the worker used to skip entirely by writing the row
	// directly: the contact's last-message state, the conversation record and
	// the outgoing event.
	if message.Status == models.MessageStatusSent {
		w.recordCampaignSend(ctx, contact, &message)
	}

	// Check if campaign is complete (all recipients processed)
	w.checkCampaignCompletion(ctx, job.CampaignID, job.OrganizationID)

	return nil
}

// updateRecipientStatus updates the recipient's status in the database
func (w *Worker) updateRecipientStatus(recipientID uuid.UUID, status models.MessageStatus, waMessageID, errorMsg string) {
	updates := map[string]any{
		"status":               status,
		"whats_app_message_id": waMessageID,
	}
	if status == models.MessageStatusSent {
		updates["sent_at"] = time.Now()
	}
	if errorMsg != "" {
		updates["error_message"] = errorMsg
	}
	w.DB.Model(&models.BulkMessageRecipient{}).Where("id = ?", recipientID).Updates(updates)
}

// incrementCampaignCount increments a campaign counter atomically
func (w *Worker) incrementCampaignCount(campaignID uuid.UUID, column string) {
	w.DB.Model(&models.BulkMessageCampaign{}).
		Where("id = ?", campaignID).
		Update(column, gorm.Expr(column+" + 1"))
}

// publishCampaignStats publishes campaign stats for real-time updates
func (w *Worker) publishCampaignStats(ctx context.Context, campaignID, organizationID uuid.UUID) {
	var campaign models.BulkMessageCampaign
	if err := w.DB.Where("id = ?", campaignID).First(&campaign).Error; err != nil {
		return
	}

	_ = w.Publisher.PublishCampaignStats(ctx, &queue.CampaignStatsUpdate{
		CampaignID:     campaignID.String(),
		OrganizationID: organizationID,
		Status:         campaign.Status,
		SentCount:      campaign.SentCount,
		DeliveredCount: campaign.DeliveredCount,
		ReadCount:      campaign.ReadCount,
		FailedCount:    campaign.FailedCount,
	})
}

// checkCampaignCompletion checks if all recipients are processed and marks campaign as completed
func (w *Worker) checkCampaignCompletion(ctx context.Context, campaignID, organizationID uuid.UUID) {
	// Count pending recipients
	var pendingCount int64
	w.DB.Model(&models.BulkMessageRecipient{}).
		Where("campaign_id = ? AND status = ?", campaignID, models.MessageStatusPending).
		Count(&pendingCount)

	// If no pending recipients, mark campaign as completed
	if pendingCount == 0 {
		var campaign models.BulkMessageCampaign
		if err := w.DB.Where("id = ?", campaignID).First(&campaign).Error; err != nil {
			return
		}

		// Only complete if currently processing
		if campaign.Status != models.CampaignStatusProcessing {
			return
		}

		now := time.Now()
		w.DB.Model(&campaign).Updates(map[string]any{
			"status":       models.CampaignStatusCompleted,
			"completed_at": now,
		})

		w.Log.Info("Campaign completed", "campaign_id", campaignID, "sent", campaign.SentCount, "failed", campaign.FailedCount)

		// Publish completion status
		_ = w.Publisher.PublishCampaignStats(ctx, &queue.CampaignStatsUpdate{
			CampaignID:     campaignID.String(),
			OrganizationID: organizationID,
			Status:         models.CampaignStatusCompleted,
			SentCount:      campaign.SentCount,
			DeliveredCount: campaign.DeliveredCount,
			ReadCount:      campaign.ReadCount,
			FailedCount:    campaign.FailedCount,
		})
	} else {
		// Publish current stats
		w.publishCampaignStats(ctx, campaignID, organizationID)
	}
}

// sendTemplateMessage sends a template message via WhatsApp Cloud API
func (w *Worker) sendTemplateMessage(ctx context.Context, account *models.WhatsAppAccount, template *models.Template, recipient *models.BulkMessageRecipient, campaignHeaderMediaID, campaignHeaderMediaFilename string) (string, error) {
	waAccount := account.ToWAAccount()

	// Resolve body parameters into a map for BuildTemplateComponents
	resolvedParams := templateutil.ResolveParams(template.BodyContent, recipient.TemplateParams)
	bodyParams := make(map[string]string, len(resolvedParams))
	paramNames := templateutil.ExtParamNames(template.BodyContent)
	for i, val := range resolvedParams {
		if i < len(paramNames) {
			bodyParams[paramNames[i]] = val
		} else {
			bodyParams[fmt.Sprintf("%d", i+1)] = val
		}
	}

	// Resolve the header text parameter (if any) into its own map. Prefer
	// recipient.HeaderParams (new path, populated by AddRecipients) and fall
	// back to a TemplateParams lookup for legacy recipient rows persisted
	// before HeaderParams existed.
	var headerParams map[string]string
	if template.HeaderType == "TEXT" {
		if hNames := templateutil.ExtParamNames(template.HeaderContent); len(hNames) == 1 {
			name := hNames[0]
			if raw, ok := recipient.HeaderParams[name]; ok {
				headerParams = map[string]string{name: fmt.Sprintf("%v", raw)}
			} else if raw, ok := recipient.TemplateParams[name]; ok {
				headerParams = map[string]string{name: fmt.Sprintf("%v", raw)}
			}
		}
	}

	// Use the shared component builder (same as chat template sending).
	components, err := whatsapp.BuildTemplateComponents(
		bodyParams,
		template.HeaderType, template.HeaderContent,
		headerParams,
		campaignHeaderMediaID, campaignHeaderMediaFilename,
	)
	if err != nil {
		return "", fmt.Errorf("failed to build template components: %w", err)
	}
	// Add auto-generated button components (Flow needs flow_token)
	flowComponents := whatsapp.AutoButtonComponents(template.Buttons)
	components = append(components, flowComponents...)

	rcpt := whatsapp.Recipient{Phone: recipient.PhoneNumber}
	return w.WhatsApp.SendTemplateMessage(ctx, waAccount, rcpt, template.Name, template.Language, components)
}

// decryptAccountSecrets decrypts the encrypted secrets on a WhatsApp account.
func (w *Worker) decryptAccountSecrets(account *models.WhatsAppAccount) {
	var key string
	if w.Config != nil {
		key = w.Config.App.EncryptionKey
	}
	account.DecryptSecrets(key)
}

// Close cleans up worker resources
func (w *Worker) Close() error {
	if w.Consumer != nil {
		return w.Consumer.Close()
	}
	return nil
}

// recordCampaignSend applies the side effects a campaign send used to skip.
//
// The worker wrote Message rows directly, so a campaign never updated the
// contact's last-message state (the inbox showed nothing) and never produced a
// message.outgoing event (no webhook, no realtime update). It has no *App and
// cannot reach the WebSocket hub, but it does not need to: writing an outbox
// row is enough, and the relay fans the event out to every replica.
func (w *Worker) recordCampaignSend(ctx context.Context, contact *models.Contact, message *models.Message) {
	now := time.Now()
	if err := w.DB.Model(&models.Contact{}).Where("id = ?", contact.ID).
		Updates(map[string]any{
			"last_message_at":      now,
			"last_message_preview": campaignPreview(message),
		}).Error; err != nil {
		w.Log.Error("Failed to update contact after campaign send", "error", err, "contact_id", contact.ID)
	}

	// Record the send against the contact's conversation, the same hook
	// SendOutgoingMessage runs (plan 10, S4/X3). RecordOutbound deliberately
	// does not open a conversation for a campaign sender — a blast to ten
	// thousand contacts is not ten thousand conversations — but where one is
	// already open the message belongs to it and has to be counted and linked,
	// or the thread shows a message the conversation does not know about.
	if conv, err := conversation.New(w.DB).RecordOutbound(ctx, message.OrganizationID,
		contact.ID, message.SenderType, nil, now); err != nil {
		w.Log.Error("Failed to record campaign send against conversation",
			"error", err, "message_id", message.ID)
	} else if conv != nil {
		if err := w.DB.Model(&models.Message{}).Where("id = ?", message.ID).
			Update("conversation_id", conv.ID.String()).Error; err != nil {
			w.Log.Error("Failed to link campaign message to conversation",
				"error", err, "message_id", message.ID)
		}
	}

	event := crmevents.New(message.OrganizationID, string(models.WebhookEventMessageOutgoing),
		crmevents.SystemActor(), map[string]any{
			"message_id":       message.ID.String(),
			"contact_id":       contact.ID.String(),
			"contact_phone":    contact.PhoneNumber,
			"contact_name":     contact.ProfileName,
			"message_type":     string(message.MessageType),
			"content":          message.Content,
			"whatsapp_account": message.WhatsAppAccount,
			"direction":        string(models.DirectionOutgoing),
		}).ForContact(contact.ID).About(crmevents.SubjectMessage, message.ID)

	if err := crmevents.Publish(w.DB, event); err != nil {
		w.Log.Error("Failed to publish campaign send event", "error", err, "message_id", message.ID)
	}
}

// campaignPreview builds the inbox preview line for a campaign message.
func campaignPreview(message *models.Message) string {
	if message.Content != "" {
		return message.Content
	}
	if message.TemplateName != "" {
		return "Template: " + message.TemplateName
	}
	return "Template message"
}
