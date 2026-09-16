package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/internal/queue"
	"github.com/shridarpatil/whatomate/internal/scheduler"
	"gorm.io/gorm"
)

// RegisterJobs registers the periodic jobs the server owns (plan 00, F4).
//
// Everything here runs behind the scheduler's leader lock, so adding a replica
// does not multiply the work. Jobs must be idempotent and safe to skip a tick.
func (a *App) RegisterJobs(s *scheduler.Scheduler) {
	s.Register(scheduler.Job{
		Name:     "scheduled_campaigns",
		Interval: time.Minute,
		Timeout:  5 * time.Minute,
		Run:      a.startDueCampaigns,
	})
	s.Register(scheduler.Job{
		Name:     "conversation_snooze_wakeup",
		Interval: time.Minute,
		Timeout:  2 * time.Minute,
		Run:      a.wakeSnoozedConversations,
	})
	s.Register(scheduler.Job{
		Name:     "task_due_notifier",
		Interval: time.Minute,
		Timeout:  3 * time.Minute,
		Run:      a.notifyDueTasks,
	})
	// Time-based automation rules (plan 08). Nothing happens when a customer
	// goes quiet, so somebody has to go looking; five minutes is fine-grained
	// enough for rules measured in hours and days.
	s.Register(scheduler.Job{
		Name:     "automation_time_triggers",
		Interval: 5 * time.Minute,
		Timeout:  10 * time.Minute,
		Run:      a.runAutomationTimeTriggers,
	})
	// Run history is for debugging what happened recently, not an archive.
	s.Register(scheduler.Job{
		Name:     "automation_runs_retention",
		Interval: 24 * time.Hour,
		Timeout:  10 * time.Minute,
		Run:      a.pruneAutomationRuns,
	})
	// Segment counts go stale as contacts change (plan 05). A number with no
	// age beside it is a number people quote long after it was true.
	s.Register(scheduler.Job{
		Name:     "segment_count_refresh",
		Interval: 15 * time.Minute,
		Timeout:  10 * time.Minute,
		Run:      a.refreshSegmentCounts,
	})
	// Duplicate scanning (plan 06). Two records for one person is a problem
	// that grows quietly; nobody goes looking for it by hand.
	s.Register(scheduler.Job{
		Name:     "duplicate_scan",
		Interval: 6 * time.Hour,
		Timeout:  15 * time.Minute,
		Run:      a.scanForDuplicates,
	})
}

// SegmentCountStaleAfter is how recently a segment must have been used for the
// refresher to bother recounting it. Recounting every saved audience every
// quarter hour would spend the database on numbers nobody is reading.
const SegmentCountStaleAfter = 7 * 24 * time.Hour

// refreshSegmentCounts recounts the segments people are actually using.
func (a *App) refreshSegmentCounts(ctx context.Context) error {
	var rows []models.Segment
	if err := a.DB.WithContext(ctx).
		Where("last_used_at IS NOT NULL AND last_used_at > ?", time.Now().UTC().Add(-SegmentCountStaleAfter)).
		Find(&rows).Error; err != nil {
		return err
	}

	for _, segment := range rows {
		registry, err := a.contactRegistry(segment.OrganizationID)
		if err != nil {
			a.Log.Error("Failed to build registry for segment count", "error", err,
				"segment_id", segment.ID)
			continue
		}
		// The refreshed number is the organization's, not one viewer's, so it
		// is counted unscoped; browsing still applies the viewer's scope.
		viewer := contactquery.Viewer{OrgID: segment.OrganizationID, CanSeeAllContacts: true}
		if _, err := a.Segments().Count(ctx, registry, viewer, segment.ID); err != nil {
			a.Log.Error("Failed to refresh segment count", "error", err, "segment_id", segment.ID)
		}
	}
	return nil
}

// scanForDuplicates looks for contacts that are probably the same person
// (plan 06).
func (a *App) scanForDuplicates(ctx context.Context) error {
	var orgIDs []uuid.UUID
	if err := a.DB.WithContext(ctx).Model(&models.Organization{}).Pluck("id", &orgIDs).Error; err != nil {
		return err
	}

	service := a.Dedupe()
	for _, orgID := range orgIDs {
		found, err := service.Scan(ctx, orgID)
		if err != nil {
			a.Log.Error("Duplicate scan failed", "error", err, "org_id", orgID)
			continue
		}
		if found > 0 {
			a.Log.Info("Duplicate candidates found", "org_id", orgID, "count", found)
		}
	}
	return nil
}

// runAutomationTimeTriggers fires the rules the scheduler owns (plan 08).
func (a *App) runAutomationTimeTriggers(ctx context.Context) error {
	fired, err := a.AutomationEngine().RunTimeTriggers(ctx)
	if err != nil {
		return err
	}
	if fired > 0 {
		a.Log.Info("Automation time triggers fired", "count", fired)
	}
	return nil
}

// AutomationRunRetention is how long a run stays readable.
const AutomationRunRetention = 30 * 24 * time.Hour

// pruneAutomationRuns deletes run history past the retention window.
func (a *App) pruneAutomationRuns(ctx context.Context) error {
	cutoff := time.Now().UTC().Add(-AutomationRunRetention)
	res := a.DB.WithContext(ctx).
		Where("started_at < ?", cutoff).
		Delete(&models.AutomationRun{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		a.Log.Info("Pruned automation runs", "rows", res.RowsAffected)
	}
	return nil
}

// notifyDueTasks reminds owners about tasks coming due, and about tasks that
// have lapsed (plan 04).
//
// A due date nobody is told about is a due date nobody meets. Each task is
// claimed by stamping the notification column, so a task is reminded once even
// though the job runs every minute.
func (a *App) notifyDueTasks(ctx context.Context) error {
	now := time.Now().UTC()

	if err := a.notifyTaskBatch(ctx, now,
		a.DB.Where("status = ? AND remind_at IS NOT NULL AND remind_at <= ? AND reminder_sent_at IS NULL",
			models.TaskOpen, now),
		"reminder_sent_at", models.NotificationTaskDue,
		"Task due soon", "A task you own is due soon."); err != nil {
		return err
	}

	// Overdue is derived from status and due_at; overdue_notified_at only
	// records that we said so, so the flag can never disagree with the data.
	return a.notifyTaskBatch(ctx, now,
		a.DB.Where("status = ? AND due_at < ? AND overdue_notified_at IS NULL",
			models.TaskOpen, now),
		"overdue_notified_at", models.NotificationTaskOverdue,
		"Task overdue", "A task you own is past its due date.")
}

// notifyTaskBatch notifies the owners of one batch and stamps the claim column.
func (a *App) notifyTaskBatch(ctx context.Context, now time.Time, query *gorm.DB, claimColumn, notificationType, title, body string) error {
	var due []models.Task
	if err := query.WithContext(ctx).Limit(500).Find(&due).Error; err != nil {
		return err
	}

	for i := range due {
		task := due[i]

		// Claim first: if the notification fails we would rather miss one
		// than send it on every tick forever.
		result := a.DB.WithContext(ctx).Model(&models.Task{}).
			Where("id = ? AND "+claimColumn+" IS NULL", task.ID).
			Update(claimColumn, now)
		if result.Error != nil {
			a.Log.Error("Failed to claim task notification", "error", result.Error, "task_id", task.ID)
			continue
		}
		if result.RowsAffected == 0 {
			continue
		}

		if err := a.Notify().Send(ctx, notify.Input{
			OrgID:   task.OrganizationID,
			UserIDs: []uuid.UUID{task.OwnerID},
			Type:    notificationType,
			Title:   title,
			Body:    task.Title,
			Link:    "/tasks?task=" + task.ID.String(),
			Entity:  notify.Entity{Type: "task", ID: &task.ID},
		}); err != nil {
			a.Log.Error("Failed to notify task owner", "error", err, "task_id", task.ID)
		}

		if notificationType == models.NotificationTaskOverdue {
			a.PublishEvent(crmevents.New(task.OrganizationID, "task.overdue",
				crmevents.SystemActor(), map[string]any{
					"task_id": task.ID.String(),
					"title":   task.Title,
					"due_at":  task.DueAt.UTC(),
				}).ForContact(task.ContactID).About(crmevents.SubjectTask, task.ID))
		}
	}

	return nil
}

// wakeSnoozedConversations reopens conversations whose snooze has expired
// (plan 03).
//
// Snoozing is "deal with this later"; without something to wake them, later
// never arrives and the conversation stays invisible to every inbox view.
//
// Conversations are woken in batches and each one is claimed by its status, so
// a conversation someone reopened by hand in the meantime is left alone.
func (a *App) wakeSnoozedConversations(ctx context.Context) error {
	var due []models.Conversation
	if err := a.DB.WithContext(ctx).
		Where("status = ? AND snoozed_until IS NOT NULL AND snoozed_until <= ?",
			models.ConversationSnoozed, time.Now().UTC()).
		Limit(500).Find(&due).Error; err != nil {
		return err
	}

	for i := range due {
		conv := due[i]
		result := a.DB.WithContext(ctx).Model(&models.Conversation{}).
			Where("id = ? AND status = ?", conv.ID, models.ConversationSnoozed).
			Updates(map[string]any{
				"status":        models.ConversationOpen,
				"snoozed_until": nil,
			})
		if result.Error != nil {
			a.Log.Error("Failed to wake snoozed conversation", "error", result.Error, "conversation_id", conv.ID)
			continue
		}
		if result.RowsAffected == 0 {
			// Someone changed it first; nothing to announce.
			continue
		}

		a.PublishEvent(crmevents.New(conv.OrganizationID, "conversation.status_changed",
			crmevents.SystemActor(), map[string]any{
				"conversation_id": conv.ID.String(),
				"from":            string(models.ConversationSnoozed),
				"to":              string(models.ConversationOpen),
				"reason":          "snooze_expired",
			}).ForContact(conv.ContactID).About(crmevents.SubjectConversation, conv.ID))

		// Tell whoever snoozed it that it is back, or the wake-up is silent
		// and they only find it by chance.
		if conv.SnoozedByID != nil {
			if err := a.Notify().Send(ctx, notify.Input{
				OrgID:   conv.OrganizationID,
				UserIDs: []uuid.UUID{*conv.SnoozedByID},
				Type:    models.NotificationConversationSnoozeEnded,
				Title:   "Snoozed conversation is back",
				Body:    "A conversation you snoozed is due for attention.",
				Link:    "/chat/" + conv.ContactID.String(),
				Entity:  notify.Entity{Type: "conversation", ID: &conv.ID},
			}); err != nil {
				a.Log.Error("Failed to notify snooze wakeup", "error", err, "conversation_id", conv.ID)
			}
		}
	}

	return nil
}

// startDueCampaigns starts campaigns whose scheduled time has arrived.
//
// Scheduling a campaign stored scheduled_at and set the status to "scheduled",
// and then nothing ever looked at it again — the campaign simply never sent.
// Starting one was only possible by pressing the button manually.
func (a *App) startDueCampaigns(ctx context.Context) error {
	var due []models.BulkMessageCampaign
	if err := a.DB.WithContext(ctx).
		Where("status = ? AND scheduled_at IS NOT NULL AND scheduled_at <= ?",
			models.CampaignStatusScheduled, time.Now()).
		Find(&due).Error; err != nil {
		return err
	}

	for i := range due {
		campaign := due[i]
		if err := a.startScheduledCampaign(ctx, &campaign); err != nil {
			// One broken campaign must not stop the others from starting.
			a.Log.Error("Failed to start scheduled campaign",
				"campaign_id", campaign.ID, "error", err)
		}
	}
	return nil
}

// startScheduledCampaign performs the same work as the manual start button.
func (a *App) startScheduledCampaign(ctx context.Context, campaign *models.BulkMessageCampaign) error {
	// A segment-targeted campaign builds its recipients at start (plan 05), so
	// a campaign scheduled last week goes to whoever matches today. The actor
	// is the campaign's creator, because nobody pressed anything.
	if _, err := a.materializeSegmentAudience(campaign.OrganizationID, campaign.CreatedBy, campaign); err != nil {
		return err
	}

	var recipients []models.BulkMessageRecipient
	if err := a.DB.WithContext(ctx).
		Where("campaign_id = ? AND status = ?", campaign.ID, models.MessageStatusPending).
		Find(&recipients).Error; err != nil {
		return err
	}

	if len(recipients) == 0 {
		// Nothing to send. Complete it rather than leaving it scheduled
		// forever, where it would be retried on every tick.
		return a.DB.WithContext(ctx).Model(campaign).Updates(map[string]any{
			"status":       models.CampaignStatusCompleted,
			"completed_at": time.Now(),
		}).Error
	}

	// A template deleted between scheduling and sending would make the worker
	// fail on every recipient, so fail the campaign once instead.
	if campaign.TemplateID != uuid.Nil {
		var template models.Template
		if err := a.DB.WithContext(ctx).
			Where("id = ? AND organization_id = ?", campaign.TemplateID, campaign.OrganizationID).
			First(&template).Error; err != nil {
			a.Log.Error("Scheduled campaign template is missing, failing campaign",
				"campaign_id", campaign.ID, "template_id", campaign.TemplateID)
			return a.DB.WithContext(ctx).Model(campaign).
				Update("status", models.CampaignStatusFailed).Error
		}
	}

	// Claim the campaign before enqueuing. The status change is what stops a
	// later tick from starting it a second time.
	result := a.DB.WithContext(ctx).Model(&models.BulkMessageCampaign{}).
		Where("id = ? AND status = ?", campaign.ID, models.CampaignStatusScheduled).
		Updates(map[string]any{
			"status":     models.CampaignStatusProcessing,
			"started_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Someone started it in the meantime.
		return nil
	}

	if a.Queue == nil {
		// Without a queue there is nothing to enqueue into. Returning an
		// error keeps the campaign scheduled for a later tick; panicking
		// here would take the scheduler goroutine down with it.
		return fmt.Errorf("handlers: no job queue configured, cannot start campaign %s", campaign.ID)
	}

	jobs := make([]*queue.RecipientJob, len(recipients))
	for i, recipient := range recipients {
		jobs[i] = &queue.RecipientJob{
			CampaignID:     campaign.ID,
			RecipientID:    recipient.ID,
			OrganizationID: campaign.OrganizationID,
			PhoneNumber:    recipient.PhoneNumber,
			RecipientName:  recipient.RecipientName,
			TemplateParams: recipient.TemplateParams,
			HeaderParams:   recipient.HeaderParams,
		}
	}

	if err := a.Queue.EnqueueRecipients(ctx, jobs); err != nil {
		// Put it back so the next tick can retry rather than stranding it
		// in "processing" with nothing queued.
		a.DB.WithContext(ctx).Model(campaign).
			Update("status", models.CampaignStatusScheduled)
		return err
	}

	a.Log.Info("Scheduled campaign started",
		"campaign_id", campaign.ID, "recipients", len(jobs))
	return nil
}
