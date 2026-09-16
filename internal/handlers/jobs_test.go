package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/queue"
	"github.com/shridarpatil/whatomate/internal/scheduler"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerodha/logf"
)

// scheduledCampaign creates a campaign due at the given time with one pending
// recipient.
func scheduledCampaign(t *testing.T, app *handlers.App, orgID uuid.UUID, accountName string, dueAt time.Time) *models.BulkMessageCampaign {
	t.Helper()

	template := testutil.CreateTestTemplate(t, app.DB, orgID, accountName)
	creator := testutil.CreateTestUser(t, app.DB, orgID)
	campaign := &models.BulkMessageCampaign{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  orgID,
		Name:            "Scheduled blast",
		WhatsAppAccount: accountName,
		TemplateID:      template.ID,
		Status:          models.CampaignStatusScheduled,
		ScheduledAt:     &dueAt,
		CreatedBy:       creator.ID,
		TotalRecipients: 1,
	}
	require.NoError(t, app.DB.Create(campaign).Error)

	require.NoError(t, app.DB.Create(&models.BulkMessageRecipient{
		BaseModel:     models.BaseModel{ID: uuid.New()},
		CampaignID:    campaign.ID,
		PhoneNumber:   "15558880001",
		RecipientName: "Due Recipient",
		Status:        models.MessageStatusPending,
	}).Error)

	return campaign
}

// runScheduledCampaignsJob runs the registered job once.
func runScheduledCampaignsJob(t *testing.T, app *handlers.App) {
	t.Helper()

	// The job enqueues into Redis, so the test app needs a real queue.
	if app.Queue == nil {
		app.Queue = queue.NewRedisQueue(app.Redis, logf.New(logf.Opts{Level: logf.ErrorLevel}))
	}

	s := scheduler.New(app.Redis, logf.New(logf.Opts{Level: logf.ErrorLevel}))
	app.RegisterJobs(s)

	var job scheduler.Job
	for _, j := range s.Jobs() {
		if j.Name == "scheduled_campaigns" {
			job = j
		}
	}
	require.NotEmpty(t, job.Name, "scheduled_campaigns must be registered")

	// Clear any lock a previous test left behind.
	app.Redis.Del(context.Background(), "wacrm:lock:job:scheduled_campaigns")
	require.True(t, s.RunOnce(context.Background(), job))
}

func campaignStatus(t *testing.T, app *handlers.App, id uuid.UUID) models.CampaignStatus {
	t.Helper()
	var c models.BulkMessageCampaign
	require.NoError(t, app.DB.First(&c, "id = ?", id).Error)
	return c.Status
}

// Scheduling a campaign stored scheduled_at and then nothing ever read it, so
// the campaign simply never sent. This is the job that reads it.
func TestScheduledCampaigns_StartsCampaignsThatAreDue(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(-time.Minute))

	runScheduledCampaignsJob(t, app)

	assert.Equal(t, models.CampaignStatusProcessing, campaignStatus(t, app, campaign.ID))
}

func TestScheduledCampaigns_LeavesFutureCampaignsAlone(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(time.Hour))

	runScheduledCampaignsJob(t, app)

	assert.Equal(t, models.CampaignStatusScheduled, campaignStatus(t, app, campaign.ID))
}

// The job runs every minute, so starting must be idempotent or a campaign would
// be enqueued again on each tick and every recipient messaged repeatedly.
func TestScheduledCampaigns_DoesNotStartTheSameCampaignTwice(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(-time.Minute))

	runScheduledCampaignsJob(t, app)
	require.Equal(t, models.CampaignStatusProcessing, campaignStatus(t, app, campaign.ID))

	// A second tick must find nothing to do: the status has moved on.
	runScheduledCampaignsJob(t, app)
	assert.Equal(t, models.CampaignStatusProcessing, campaignStatus(t, app, campaign.ID))
}

// A campaign with nothing left to send must not stay scheduled forever, being
// retried on every tick.
func TestScheduledCampaigns_CompletesWhenThereAreNoPendingRecipients(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(-time.Minute))
	require.NoError(t, app.DB.Model(&models.BulkMessageRecipient{}).
		Where("campaign_id = ?", campaign.ID).
		Update("status", models.MessageStatusSent).Error)

	runScheduledCampaignsJob(t, app)

	assert.Equal(t, models.CampaignStatusCompleted, campaignStatus(t, app, campaign.ID))
}

// A template deleted between scheduling and sending would make the worker fail
// on every single recipient; fail the campaign once instead.
func TestScheduledCampaigns_FailsWhenTheTemplateIsGone(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(-time.Minute))

	// Templates are soft-deleted in production; a foreign key stops the row
	// itself from going away while a campaign still references it.
	require.NoError(t, app.DB.Delete(&models.Template{}, "id = ?", campaign.TemplateID).Error)

	runScheduledCampaignsJob(t, app)

	assert.Equal(t, models.CampaignStatusFailed, campaignStatus(t, app, campaign.ID))
}

// Draft campaigns are not scheduled and must never be started by this job.
func TestScheduledCampaigns_IgnoresDrafts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	campaign := scheduledCampaign(t, app, org.ID, account.Name, time.Now().Add(-time.Minute))
	require.NoError(t, app.DB.Model(campaign).Update("status", models.CampaignStatusDraft).Error)

	runScheduledCampaignsJob(t, app)

	assert.Equal(t, models.CampaignStatusDraft, campaignStatus(t, app, campaign.ID))
}

// runJob runs one registered scheduler job by name.
func runJob(t *testing.T, app *handlers.App, name string) {
	t.Helper()

	s := scheduler.New(app.Redis, logf.New(logf.Opts{Level: logf.ErrorLevel}))
	app.RegisterJobs(s)

	var job scheduler.Job
	for _, j := range s.Jobs() {
		if j.Name == name {
			job = j
		}
	}
	require.NotEmpty(t, job.Name, "%s must be registered", name)

	app.Redis.Del(context.Background(), "wacrm:lock:job:"+name)
	require.True(t, s.RunOnce(context.Background(), job))
}

// Snoozing means "later". Without something to wake them, later never arrives
// and the conversation stays invisible in every inbox view.
func TestSnoozeWakeup_ReopensExpiredSnoozes(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15552000001"))

	svc := app.Conversations()
	_, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Snooze(context.Background(), org.ID, contact.ID,
		time.Now().UTC().Add(-time.Minute), crmevents.UserActor(user.ID, "Agent"))
	require.NoError(t, err)

	runJob(t, app, "conversation_snooze_wakeup")

	woken, err := svc.Active(context.Background(), org.ID, contact.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ConversationOpen, woken.Status)
	assert.Nil(t, woken.SnoozedUntil)
}

func TestSnoozeWakeup_LeavesFutureSnoozesAlone(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15552010001"))

	svc := app.Conversations()
	_, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Snooze(context.Background(), org.ID, contact.ID,
		time.Now().UTC().Add(time.Hour), crmevents.SystemActor())
	require.NoError(t, err)

	runJob(t, app, "conversation_snooze_wakeup")

	still, err := svc.Active(context.Background(), org.ID, contact.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ConversationSnoozed, still.Status)
}

// Waking a conversation silently would leave whoever snoozed it to find it by
// chance.
func TestSnoozeWakeup_NotifiesWhoeverSnoozedIt(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15552020001"))

	svc := app.Conversations()
	_, err := svc.TouchInbound(context.Background(), org.ID, contact.ID, "acct", time.Now().UTC(), true)
	require.NoError(t, err)
	_, err = svc.Snooze(context.Background(), org.ID, contact.ID,
		time.Now().UTC().Add(-time.Minute), crmevents.UserActor(user.ID, "Agent"))
	require.NoError(t, err)

	runJob(t, app, "conversation_snooze_wakeup")

	count, err := app.Notify().UnreadCount(context.Background(), org.ID, user.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}
