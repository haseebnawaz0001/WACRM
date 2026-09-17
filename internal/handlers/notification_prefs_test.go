package handlers_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// Plan 00, F5: notify.Send has honoured per-type preferences since the bell
// shipped, and nothing could write them — so every user sat on the default for
// every type, and an agent getting a sound for each of forty campaign updates
// could only mute the browser tab.
func TestUpdateCurrentUserSettings_StoresNotificationPreferences(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	req := testutil.NewJSONRequest(t, map[string]any{
		"email_notifications": true,
		"new_message_alerts":  true,
		"campaign_updates":    true,
		"notifications": map[string]any{
			models.NotificationCampaignPaused: map[string]any{"in_app": true, "sound": false},
			models.NotificationTaskDue:        map[string]any{"in_app": false, "sound": false},
			// A type the product does not send is dropped rather than stored:
			// a preference against something that never fires looks saved and
			// does nothing.
			"invented_type": map[string]any{"in_app": false, "sound": false},
		},
	})
	testutil.SetAuthContext(req, org.ID, user.ID)

	require.NoError(t, app.UpdateCurrentUserSettings(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var saved models.User
	require.NoError(t, app.DB.First(&saved, "id = ?", user.ID).Error)

	assert.True(t, notify.WantsInApp(saved.Settings, models.NotificationCampaignPaused))
	assert.False(t, notify.WantsSound(saved.Settings, models.NotificationCampaignPaused),
		"the bell without the noise is the point of splitting the two")
	assert.False(t, notify.WantsInApp(saved.Settings, models.NotificationTaskDue))

	stored, ok := saved.Settings["notifications"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, stored, "invented_type")

	// A type nobody has expressed an opinion about still reaches them.
	assert.True(t, notify.WantsInApp(saved.Settings, models.NotificationSLAEscalation))
}

// Settings sent without the field must not wipe preferences somebody set: a
// client that predates the field would otherwise silently reset them.
func TestUpdateCurrentUserSettings_LeavesPreferencesAloneWhenNotSent(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	user := testutil.CreateTestUser(t, app.DB, org.ID)

	set := testutil.NewJSONRequest(t, map[string]any{
		"email_notifications": true,
		"notifications": map[string]any{
			models.NotificationTaskDue: map[string]any{"in_app": false, "sound": false},
		},
	})
	testutil.SetAuthContext(set, org.ID, user.ID)
	require.NoError(t, app.UpdateCurrentUserSettings(set))

	// An older client sends the three booleans and nothing else.
	old := testutil.NewJSONRequest(t, map[string]any{
		"email_notifications": false,
		"new_message_alerts":  true,
		"campaign_updates":    true,
	})
	testutil.SetAuthContext(old, org.ID, user.ID)
	require.NoError(t, app.UpdateCurrentUserSettings(old))

	var saved models.User
	require.NoError(t, app.DB.First(&saved, "id = ?", user.ID).Error)
	assert.False(t, notify.WantsInApp(saved.Settings, models.NotificationTaskDue),
		"the preference survived a client that does not know about it")
}
