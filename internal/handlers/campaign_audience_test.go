package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// campaignFor creates a draft campaign with a template of the given category.
func campaignFor(t *testing.T, app *handlers.App, org *models.Organization, creator *models.User, category string) *models.BulkMessageCampaign {
	t.Helper()

	template := models.Template{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		Name:            "promo_" + uuid.New().String()[:8],
		Language:        "en",
		Category:        category,
		Status:          "APPROVED",
		BodyContent:     "Hi {{1}}, we have news.",
		WhatsAppAccount: "acct",
	}
	require.NoError(t, app.DB.Create(&template).Error)

	campaign := models.BulkMessageCampaign{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		WhatsAppAccount: "acct",
		Name:            "Autumn sale",
		TemplateID:      template.ID,
		Status:          models.CampaignStatusDraft,
		CreatedBy:       creator.ID,
	}
	require.NoError(t, app.DB.Create(&campaign).Error)
	return &campaign
}

func setAudience(t *testing.T, app *handlers.App, org *models.Organization, user *models.User, campaignID, segmentID string) int {
	t.Helper()
	req := testutil.NewJSONRequest(t, map[string]any{
		"audience_type": "segment", "segment_id": segmentID,
	})
	testutil.SetAuthContext(req, org.ID, user.ID)
	testutil.SetPathParam(req, "id", campaignID)
	require.NoError(t, app.SetCampaignAudience(req))
	return testutil.GetResponseStatusCode(req)
}

func TestSetCampaignAudience_PointsACampaignAtASegment(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "MARKETING")

	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	var reloaded models.BulkMessageCampaign
	require.NoError(t, app.DB.Where("id = ?", campaign.ID).First(&reloaded).Error)
	assert.Equal(t, "segment", reloaded.AudienceType)
	require.NotNil(t, reloaded.SegmentID)
	assert.Equal(t, segment.ID, reloaded.SegmentID.String())
}

// Changing who a campaign is aimed at after it has started would mean the
// recipient list no longer matches what was sent.
func TestSetCampaignAudience_RefusedOnceTheCampaignIsRunning(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "MARKETING")
	require.NoError(t, app.DB.Model(&campaign).
		Update("status", models.CampaignStatusProcessing).Error)

	assert.Equal(t, fasthttp.StatusBadRequest,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))
}

// "12,400 contacts" and "12,400 minus 3,000 who opted out" are different
// decisions, so the preview says which.
func TestPreviewCampaignAudience_ExcludesOptedOutContactsForMarketing(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559900001"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Reachable"))
	optedOut := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559900002"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Opted out"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", optedOut.ID).
		Update("marketing_opt_out", true).Error)

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "MARKETING")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data handlers.AudiencePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, 1, result.Data.Count)
	assert.Equal(t, 1, result.Data.Excluded[handlers.ExcludedOptedOut])

	require.Len(t, result.Data.Sample, 1)
	assert.Equal(t, "Reachable", result.Data.Sample[0].Name)
	// The rendered body, not the raw template: approving "Hi {{1}}" tells
	// nobody what the customer will read.
	assert.Contains(t, result.Data.Sample[0].Preview, "Reachable")
	assert.NotContains(t, result.Data.Sample[0].Preview, "{{1}}")
}

// A utility template is not marketing, so an opt-out does not apply to it.
func TestPreviewCampaignAudience_UtilityTemplatesReachOptedOutContacts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	optedOut := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559900003"), testutil.WithTags("VIP"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", optedOut.ID).
		Update("marketing_opt_out", true).Error)

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "UTILITY")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))

	var result struct {
		Data handlers.AudiencePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, 1, result.Data.Count)
	assert.Empty(t, result.Data.Excluded)
}

// Recipients are built at start, and linked to the contacts they came from so
// results read as "what happened to these contacts".
func TestStartCampaign_MaterialisesTheSegmentIntoRecipients(t *testing.T) {
	app := newTestApp(t, withQueue(testutil.NewMockQueue()))
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	member := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559900004"), testutil.WithTags("VIP"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559900005"))

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "UTILITY")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())
	require.NoError(t, app.StartCampaign(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var recipients []models.BulkMessageRecipient
	require.NoError(t, app.DB.Where("campaign_id = ?", campaign.ID).Find(&recipients).Error)
	require.Len(t, recipients, 1)
	require.NotNil(t, recipients[0].ContactID)
	assert.Equal(t, member.ID, *recipients[0].ContactID)

	var reloaded models.BulkMessageCampaign
	require.NoError(t, app.DB.Where("id = ?", campaign.ID).First(&reloaded).Error)
	require.NotNil(t, reloaded.MaterializedAt)
	require.NotNil(t, reloaded.AudienceCount)
	assert.Equal(t, 1, *reloaded.AudienceCount)
	// The filter as it stood, so "who did this go to and why" stays answerable
	// even if the segment is edited afterwards.
	assert.NotEmpty(t, reloaded.AudienceFilter)
}

// setFrequencyCap turns the organization's marketing cap on.
func setFrequencyCap(t *testing.T, app *handlers.App, org *models.Organization, hours int) {
	t.Helper()
	require.NoError(t, app.DB.Exec(
		`UPDATE organizations SET settings = coalesce(settings, '{}'::jsonb)
		 || jsonb_build_object('marketing_frequency_cap_hours', ?::int) WHERE id = ?`,
		hours, org.ID).Error)
	app.InvalidateOrgSettingsCache(org.ID)
}

// recordCampaignSend writes the message a campaign would have produced.
func recordCampaignSend(t *testing.T, app *handlers.App, org *models.Organization, contactID uuid.UUID, ago string) {
	t.Helper()
	require.NoError(t, app.DB.Exec(`
		INSERT INTO messages (id, organization_id, whats_app_account, contact_id,
			direction, message_type, content, status, sender_type, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, 'acct', ?, 'outgoing', 'template', 'hi', 'sent', ?,
			now() - ?::interval, now())`,
		org.ID, contactID, models.SenderCampaign, ago).Error)
}

// Opt-out is all-or-nothing: somebody sent four marketing campaigns in a week
// has no way to ask for fewer without asking for none. The cap is what stands
// between a well-meaning marketing calendar and a customer being harassed.
func TestPreviewCampaignAudience_FrequencyCapExcludesRecentlyMailedContacts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	setFrequencyCap(t, app, org, 48)

	fresh := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559911001"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Not mailed"))
	recent := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559911002"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Mailed yesterday"))
	recordCampaignSend(t, app, org, recent.ID, "1 day")
	// Outside the window, so still reachable.
	recordCampaignSend(t, app, org, fresh.ID, "10 days")

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "MARKETING")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data handlers.AudiencePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, 1, result.Data.Count)
	assert.Equal(t, 1, result.Data.Excluded[handlers.ExcludedRecentlyMailed])
	require.Len(t, result.Data.Sample, 1)
	assert.Equal(t, "Not mailed", result.Data.Sample[0].Name)
}

// A delivery notification or a one-time code is not what anybody means by "too
// many messages", so the cap must not narrow a utility campaign.
func TestPreviewCampaignAudience_FrequencyCapDoesNotApplyToUtility(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)
	setFrequencyCap(t, app, org, 48)

	recent := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559912001"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Mailed yesterday"))
	recordCampaignSend(t, app, org, recent.ID, "1 day")

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "UTILITY")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))

	var result struct {
		Data handlers.AudiencePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, 1, result.Data.Count)
	assert.Zero(t, result.Data.Excluded[handlers.ExcludedRecentlyMailed])
}

// Off by default: a cap chosen on an organization's behalf would silently drop
// recipients from campaigns they had already approved.
func TestPreviewCampaignAudience_FrequencyCapIsOffUnlessSet(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	recent := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559913001"), testutil.WithTags("VIP"),
		testutil.WithProfileName("Mailed yesterday"))
	recordCampaignSend(t, app, org, recent.ID, "1 day")

	segment := createSegmentVia(t, app, org, admin, vipBody("VIP"))
	campaign := campaignFor(t, app, org, admin, "MARKETING")
	require.Equal(t, fasthttp.StatusOK,
		setAudience(t, app, org, admin, campaign.ID.String(), segment.ID))

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))

	var result struct {
		Data handlers.AudiencePreview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Equal(t, 1, result.Data.Count)
}
