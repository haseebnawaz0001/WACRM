package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// maskingOrg builds an organization with phone masking turned on.
func maskingOrg(t *testing.T, app *handlers.App) *models.Organization {
	t.Helper()
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, app.DB.Model(&models.Organization{}).Where("id = ?", org.ID).
		Update("settings", models.JSONB{"mask_phone_numbers": true}).Error)
	return org
}

func responseBody(t *testing.T, req *fastglue.Request) string {
	t.Helper()
	return string(req.RequestCtx.Response.Body())
}

// Plan 10, S9: phone masking is an organization setting, so every list has to
// honour it.
//
// Masking was applied handler by handler, and each list added afterwards simply
// did not know about it. The deals board and list returned full phone numbers
// to organizations that had explicitly turned masking on — and a setting that
// holds in some lists but not others is worse than no setting, because it is
// trusted.
func TestListDeals_MasksPhoneNumbersWhenTheOrgAsksForIt(t *testing.T) {
	app := newTestApp(t)
	org := maskingOrg(t, app)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("phone_number", "919876543210").Error)

	pipeline := &models.Pipeline{OrganizationID: org.ID, Name: "Sales"}
	require.NoError(t, app.DB.Create(pipeline).Error)
	stage := &models.PipelineStage{OrganizationID: org.ID, PipelineID: pipeline.ID, Name: "New", Position: 1}
	require.NoError(t, app.DB.Create(stage).Error)

	require.NoError(t, app.DB.Create(&models.Deal{
		OrganizationID: org.ID,
		PipelineID:     pipeline.ID,
		StageID:        stage.ID,
		ContactID:      contact.ID,
		Title:          "Renewal",
		Currency:       "USD",
	}).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ListDeals(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	body := responseBody(t, req)
	assert.NotContains(t, body, "919876543210", "the full number must not reach the client")
	assert.Contains(t, body, "3210", "the last four digits are what masking keeps")
}

// With masking off, the same list returns the real number — otherwise the
// assertion above would pass for the wrong reason.
func TestListDeals_ReturnsTheRealNumberWhenMaskingIsOff(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("phone_number", "919876543210").Error)

	pipeline := &models.Pipeline{OrganizationID: org.ID, Name: "Sales"}
	require.NoError(t, app.DB.Create(pipeline).Error)
	stage := &models.PipelineStage{OrganizationID: org.ID, PipelineID: pipeline.ID, Name: "New", Position: 1}
	require.NoError(t, app.DB.Create(stage).Error)

	require.NoError(t, app.DB.Create(&models.Deal{
		OrganizationID: org.ID,
		PipelineID:     pipeline.ID,
		StageID:        stage.ID,
		ContactID:      contact.ID,
		Title:          "Renewal",
		Currency:       "USD",
	}).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ListDeals(req))

	assert.Contains(t, responseBody(t, req), "919876543210")
}

// The duplicate-contact list is a side-by-side of two records, so it shows the
// phone number twice and was the easiest place to read one off.
func TestListDuplicates_MasksPhoneNumbers(t *testing.T) {
	app := newTestApp(t)
	org := maskingOrg(t, app)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	primary := testutil.CreateTestContact(t, app.DB, org.ID)
	secondary := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(primary).Update("phone_number", "919876543210").Error)
	require.NoError(t, app.DB.Model(secondary).Update("phone_number", "919876543211").Error)

	require.NoError(t, app.DB.Create(&models.ContactDuplicateCandidate{
		OrganizationID: org.ID,
		ContactAID:     primary.ID,
		ContactBID:     secondary.ID,
		Reasons:        models.JSONBArray{"phone"},
		Score:          90,
		Status:         "pending",
	}).Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.ListDuplicates(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	body := responseBody(t, req)
	assert.NotContains(t, body, "919876543210")
	assert.NotContains(t, body, "919876543211")
}

// A campaign audience preview lists real people about to be messaged. It is
// shown before approval, to whoever is building the campaign.
func TestPreviewCampaignAudience_MasksPhoneNumbers(t *testing.T) {
	app := newTestApp(t)
	org := maskingOrg(t, app)
	role := testutil.CreateAdminRole(t, app.DB, org.ID)
	admin := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)
	template := testutil.CreateTestTemplate(t, app.DB, org.ID, account.Name)

	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	require.NoError(t, app.DB.Model(contact).Update("phone_number", "919876543210").Error)

	segment := &models.Segment{
		OrganizationID: org.ID,
		Name:           "Everyone " + uuid.NewString()[:8],
		Filter:         models.JSONB{"op": "and", "conditions": []any{}},
		Visibility:     "shared",
		CreatedByID:    admin.ID,
	}
	require.NoError(t, app.DB.Create(segment).Error)

	campaign := &models.BulkMessageCampaign{
		OrganizationID:  org.ID,
		WhatsAppAccount: account.Name,
		Name:            "Preview",
		TemplateID:      template.ID,
		AudienceType:    "segment",
		SegmentID:       &segment.ID,
		CreatedBy:       admin.ID,
	}
	require.NoError(t, app.DB.Create(campaign).Error)
	require.NoError(t, app.DB.Model(campaign).Update("audience_type", "segment").Error)

	req := testutil.NewJSONRequest(t, nil)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "id", campaign.ID.String())

	require.NoError(t, app.PreviewCampaignAudience(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	// The preview may legitimately be empty; what must never happen is a full
	// number appearing in it.
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(req.RequestCtx.Response.Body(), &decoded))
	assert.NotContains(t, responseBody(t, req), "919876543210")
}
