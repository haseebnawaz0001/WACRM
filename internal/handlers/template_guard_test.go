package handlers_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func campaignOn(t *testing.T, app *handlers.App, orgID, templateID, creatorID uuid.UUID,
	account string, status models.CampaignStatus) *models.BulkMessageCampaign {
	t.Helper()
	at := time.Now().Add(12 * time.Hour)
	campaign := &models.BulkMessageCampaign{
		OrganizationID:  orgID,
		WhatsAppAccount: account,
		Name:            "Campaign " + uuid.NewString()[:8],
		TemplateID:      templateID,
		Status:          status,
		ScheduledAt:     &at,
		CreatedBy:       creatorID,
	}
	require.NoError(t, app.DB.Create(campaign).Error)
	// Status carries a GORM default, so a non-default value has to be written
	// explicitly or the insert silently stores "draft".
	require.NoError(t, app.DB.Model(campaign).Update("status", status).Error)
	return campaign
}

// Plan 10, S8: a template leaving APPROVED has to stop the campaigns that
// depend on it.
//
// Meta moves templates out of APPROVED on its own schedule. Nothing noticed, so
// a campaign scheduled for the next morning woke up, materialised its audience
// and failed every single recipient; the first anyone knew was an all-red
// report with the audience already spent.
func TestGuardTemplateDependents_PausesScheduledAndDraftCampaigns(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)

	scheduled := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusScheduled)
	draft := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusDraft)

	template.Status = "PAUSED"
	paused := app.GuardTemplateDependents(template, "Quality rating dropped")

	assert.Equal(t, int64(2), paused)
	assert.Equal(t, models.CampaignStatusPaused, campaignStatus(t, app, scheduled.ID))
	assert.Equal(t, models.CampaignStatusPaused, campaignStatus(t, app, draft.ID))
}

// A campaign already mid-send has messages Meta has accepted; pausing it there
// would strand half an audience with no way to tell which half.
func TestGuardTemplateDependents_LeavesProcessingAndCompletedAlone(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)

	processing := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusProcessing)
	completed := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusCompleted)

	template.Status = "REJECTED"
	assert.Zero(t, app.GuardTemplateDependents(template, ""))
	assert.Equal(t, models.CampaignStatusProcessing, campaignStatus(t, app, processing.ID))
	assert.Equal(t, models.CampaignStatusCompleted, campaignStatus(t, app, completed.ID))
}

// A template that is still approved changes nothing.
func TestGuardTemplateDependents_ApprovedTemplateIsNoOp(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)

	scheduled := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusScheduled)

	assert.Zero(t, app.GuardTemplateDependents(template, ""))
	assert.Equal(t, models.CampaignStatusScheduled, campaignStatus(t, app, scheduled.ID))
}

// The campaign's owner has to be told, or a paused campaign just looks like the
// product lost it.
func TestGuardTemplateDependents_NotifiesTheCampaignOwner(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)

	campaign := campaignOn(t, app, org.ID, template.ID, user.ID, account.Name, models.CampaignStatusScheduled)

	template.Status = "DISABLED"
	require.Equal(t, int64(1), app.GuardTemplateDependents(template, "Policy violation"))

	var notifications []models.Notification
	require.NoError(t, app.DB.Where("organization_id = ? AND user_id = ? AND type = ?",
		org.ID, user.ID, models.NotificationCampaignPaused).Find(&notifications).Error)
	require.Len(t, notifications, 1)
	assert.Contains(t, notifications[0].Title, campaign.Name)
	assert.Contains(t, notifications[0].Body, "Policy violation")
}

// Deleting a template leaves its campaigns pointing at a row that no longer
// exists; they must not stay startable.
func TestDeleteTemplate_PausesDependentCampaigns(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)
	require.NoError(t, app.DB.Model(template).Update("meta_template_id", "").Error)

	scheduled := campaignOn(t, app, org.ID, template.ID, admin.ID, account.Name, models.CampaignStatusScheduled)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", template.ID.String())

	require.NoError(t, app.DeleteTemplate(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	assert.Equal(t, models.CampaignStatusPaused, campaignStatus(t, app, scheduled.ID),
		"a campaign whose template was deleted must not stay scheduled")
}
