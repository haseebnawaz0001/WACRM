package crmactions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, crmactions.Deps, crmactions.RunContext, *models.Organization, *models.Contact, *models.User) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	deps := crmactions.Deps{DB: db}
	rc := crmactions.RunContext{
		OrgID:     org.ID,
		ContactID: contact.ID,
		Actor:     crmevents.UserActor(user.ID, "Tester"),
		Vars:      map[string]any{"contact": map[string]any{"name": contact.ProfileName}},
	}
	return db, deps, rc, org, contact, user
}

func tagsOf(t *testing.T, db *gorm.DB, contactID uuid.UUID) []string {
	t.Helper()
	var contact models.Contact
	require.NoError(t, db.Where("id = ?", contactID).First(&contact).Error)

	out := make([]string, 0, len(contact.Tags))
	for _, tag := range contact.Tags {
		if s, ok := tag.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- Registry ---

func TestLookup_KnowsEveryAdvertisedAction(t *testing.T) {
	for _, actionType := range crmactions.Types() {
		_, ok := crmactions.Lookup(actionType)
		assert.True(t, ok, "%s is advertised but not registered", actionType)
	}
	assert.Contains(t, crmactions.Types(), crmactions.TypeAddTags)
}

func TestExecute_RejectsAnUnknownAction(t *testing.T) {
	_, deps, rc, _, _, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, "make_coffee", crmactions.Config{})
	require.Error(t, err)
	assert.True(t, crmactions.IsPermanent(err), "an action that does not exist will never exist")
}

// --- Tags ---

func TestAddTags_TagsTheContactAndCreatesUnknownTags(t *testing.T) {
	db, deps, rc, org, contact, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddTags,
		crmactions.Config{"tags": []any{"VIP", "Renewal"}})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"VIP", "Renewal"}, tagsOf(t, db, contact.ID))

	// An unknown tag is created rather than refused: the alternative is a rule
	// that fails at 3am because nobody had used that word yet.
	var known int64
	require.NoError(t, db.Model(&models.Tag{}).
		Where("organization_id = ? AND name IN ?", org.ID, []string{"VIP", "Renewal"}).
		Count(&known).Error)
	assert.Equal(t, int64(2), known)
}

// A rule that runs twice should not start reporting errors on the second pass.
func TestAddTags_IsIdempotent(t *testing.T) {
	db, deps, rc, _, contact, _ := setup(t)

	for i := 0; i < 2; i++ {
		_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddTags,
			crmactions.Config{"tags": []any{"VIP"}})
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"VIP"}, tagsOf(t, db, contact.ID))
}

// "vip" and "VIP" are the same tag to everyone except a string comparison.
func TestAddTags_DoesNotDuplicateADifferentlyCasedTag(t *testing.T) {
	db, deps, rc, _, contact, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddTags,
		crmactions.Config{"tags": []any{"VIP"}})
	require.NoError(t, err)
	_, err = crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddTags,
		crmactions.Config{"tags": []any{"vip"}})
	require.NoError(t, err)

	assert.Len(t, tagsOf(t, db, contact.ID), 1)
}

func TestRemoveTags_TakesTheTagOff(t *testing.T) {
	db, deps, rc, _, contact, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddTags,
		crmactions.Config{"tags": []any{"VIP", "Renewal"}})
	require.NoError(t, err)

	_, err = crmactions.Execute(ctx(), deps, rc, crmactions.TypeRemoveTags,
		crmactions.Config{"tags": []any{"vip"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"Renewal"}, tagsOf(t, db, contact.ID))
}

// --- Dry run ---

// Everything that mutates has to check it; an action that ignores dry run
// makes the rule tester dangerous.
func TestDryRun_NoActionWritesAnything(t *testing.T) {
	db, deps, rc, org, contact, user := setup(t)
	rc.DryRun = true

	require.NoError(t, tasks.SeedOrganization(db, org.ID))

	cases := []struct {
		actionType string
		config     crmactions.Config
	}{
		{crmactions.TypeAddTags, crmactions.Config{"tags": []any{"VIP"}}},
		{crmactions.TypeRemoveTags, crmactions.Config{"tags": []any{"VIP"}}},
		{crmactions.TypeCreateTask, crmactions.Config{"title": "Call back"}},
		{crmactions.TypeAddNote, crmactions.Config{"content": "A note"}},
		{crmactions.TypeSetContactOwner, crmactions.Config{"mode": "user", "user_id": user.ID.String()}},
		{crmactions.TypeSendMessage, crmactions.Config{"text": "Hello"}},
	}

	for _, c := range cases {
		output, err := crmactions.Execute(ctx(), deps, rc, c.actionType, c.config)
		require.NoError(t, err, c.actionType)
		assert.NotNil(t, output, "%s should still describe what it would do", c.actionType)
	}

	assert.Empty(t, tagsOf(t, db, contact.ID))

	var taskCount, noteCount int64
	require.NoError(t, db.Model(&models.Task{}).Where("contact_id = ?", contact.ID).Count(&taskCount).Error)
	require.NoError(t, db.Model(&models.ConversationNote{}).Where("contact_id = ?", contact.ID).Count(&noteCount).Error)
	assert.Zero(t, taskCount)
	assert.Zero(t, noteCount)

	var after models.Contact
	require.NoError(t, db.Where("id = ?", contact.ID).First(&after).Error)
	assert.Nil(t, after.AssignedUserID)
}

// --- Tasks and notes ---

func TestCreateTask_RendersTheTitleFromTemplateVariables(t *testing.T) {
	db, deps, rc, org, contact, _ := setup(t)
	require.NoError(t, tasks.SeedOrganization(db, org.ID))

	output, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeCreateTask,
		crmactions.Config{"title": "Follow up with {{contact.name}}"})
	require.NoError(t, err)
	assert.Contains(t, output["title"], contact.ProfileName)

	var task models.Task
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&task).Error)
	assert.Equal(t, models.TaskSourceAutomation, task.Source)
}

// An owner who does not exist is a configuration problem: nobody becomes
// active because we tried again.
func TestSetContactOwner_RejectsAnInactiveUserPermanently(t *testing.T) {
	_, deps, rc, _, _, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeSetContactOwner,
		crmactions.Config{"mode": "user", "user_id": uuid.New().String()})
	require.Error(t, err)
	assert.True(t, crmactions.IsPermanent(err))
}

func TestAddNote_AttributesAutomationNotesToTheRule(t *testing.T) {
	db, deps, rc, _, contact, _ := setup(t)

	ruleID := uuid.New()
	rc.Actor = crmevents.Actor{Type: crmevents.ActorAutomation, ID: &ruleID, Name: "Overdue follow-up"}

	// conversation_notes requires a real author, so the rule supplies one.
	rc.Actor.ID = &ruleID
	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddNote,
		crmactions.Config{"content": "Chased by rule"})
	// Without a resolvable author the action refuses rather than writing a
	// note nobody can be asked about.
	require.Error(t, err)

	user := testutil.CreateTestUser(t, db, rc.OrgID)
	_, err = crmactions.Execute(ctx(), deps, rc, crmactions.TypeAddNote,
		crmactions.Config{
			"content": "Chased by rule",
			"owner":   map[string]any{"fallback_user_id": user.ID.String()},
		})
	require.NoError(t, err)

	var note models.ConversationNote
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&note).Error)
	assert.Contains(t, note.Content, "Automation: Overdue follow-up",
		"a note nobody can attribute is a note nobody trusts")
}

// --- Webhook ---

func TestCallWebhook_RejectsSomethingThatIsNotAUrl(t *testing.T) {
	err := crmactions.Validate(crmactions.TypeCallWebhook, crmactions.Config{"url": "ftp://files"})
	require.Error(t, err)
}

func TestCallWebhook_RejectsAnOverlongTimeout(t *testing.T) {
	err := crmactions.Validate(crmactions.TypeCallWebhook,
		crmactions.Config{"url": "https://example.com", "timeout_s": 60.0})
	require.Error(t, err)
}

// The receiver telling us the request is wrong will say the same thing next
// time; a 5xx may well not.
func TestCallWebhook_ClientErrorsArePermanentAndServerErrorsAreNot(t *testing.T) {
	_, deps, rc, _, _, _ := setup(t)
	deps.HTTPClient = http.DefaultClient

	badRequest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer badRequest.Close()

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeCallWebhook,
		crmactions.Config{"url": badRequest.URL})
	require.Error(t, err)
	assert.True(t, crmactions.IsPermanent(err))

	serverError := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer serverError.Close()

	_, err = crmactions.Execute(ctx(), deps, rc, crmactions.TypeCallWebhook,
		crmactions.Config{"url": serverError.URL})
	require.Error(t, err)
	assert.True(t, crmactions.IsRetryable(err))
}

func TestCallWebhook_SendsTheRenderedBodyAndSignature(t *testing.T) {
	_, deps, rc, _, contact, _ := setup(t)
	deps.HTTPClient = http.DefaultClient

	var gotBody, gotSignature string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		gotSignature = r.Header.Get("X-WACRM-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeCallWebhook, crmactions.Config{
		"url":            server.URL,
		"body":           `{"name":"{{contact.name}}"}`,
		"sign":           true,
		"signing_secret": "s3cret",
	})
	require.NoError(t, err)

	assert.Contains(t, gotBody, contact.ProfileName, "template variables are rendered before sending")
	assert.True(t, len(gotSignature) > len("sha256="), "a signed webhook carries its signature")
}

// Without the SSRF-safe client this action would fetch the cloud metadata
// endpoint on behalf of whoever wrote the rule.
func TestCallWebhook_RefusesWithoutAnHTTPClient(t *testing.T) {
	_, deps, rc, _, _, _ := setup(t)
	deps.HTTPClient = nil

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeCallWebhook,
		crmactions.Config{"url": "https://example.com"})
	require.Error(t, err)
	assert.True(t, crmactions.IsPermanent(err))
}

// A process that cannot send says so rather than silently doing nothing.
func TestSendMessage_RefusesWithoutAMessenger(t *testing.T) {
	_, deps, rc, _, _, _ := setup(t)

	_, err := crmactions.Execute(ctx(), deps, rc, crmactions.TypeSendMessage,
		crmactions.Config{"text": "Hello"})
	require.ErrorIs(t, err, crmactions.ErrUnavailable)
}

// --- Validation ---

func TestValidate_CatchesConfigurationMistakesAtSaveTime(t *testing.T) {
	cases := []struct {
		actionType string
		config     crmactions.Config
	}{
		{crmactions.TypeAddTags, crmactions.Config{}},
		{crmactions.TypeSetField, crmactions.Config{"field": "company"}},
		{crmactions.TypeCreateTask, crmactions.Config{}},
		{crmactions.TypeAddNote, crmactions.Config{}},
		{crmactions.TypeNotifyUsers, crmactions.Config{"title": "Hey"}},
		{crmactions.TypeSetContactOwner, crmactions.Config{"mode": "whoever"}},
		{crmactions.TypeSetConversationStatus, crmactions.Config{"status": "pending"}},
		{crmactions.TypeMoveDealStage, crmactions.Config{}},
		{crmactions.TypeSendTemplate, crmactions.Config{}},
	}

	for _, c := range cases {
		assert.Error(t, crmactions.Validate(c.actionType, c.config),
			"%s should reject %v at save time", c.actionType, c.config)
	}
}

func TestValidate_AcceptsWorkableConfigurations(t *testing.T) {
	cases := []struct {
		actionType string
		config     crmactions.Config
	}{
		{crmactions.TypeAddTags, crmactions.Config{"tags": []any{"VIP"}}},
		{crmactions.TypeSetField, crmactions.Config{"field": "company", "value": "Acme"}},
		{crmactions.TypeCreateTask, crmactions.Config{"title": "Call back"}},
		{crmactions.TypeAddNote, crmactions.Config{"content": "Note"}},
		{crmactions.TypeNotifyUsers, crmactions.Config{
			"title": "Hey", "recipients": map[string]any{"contact_owner": true}}},
		{crmactions.TypeSetConversationStatus, crmactions.Config{"status": "resolved"}},
		{crmactions.TypeSetConversationStatus, crmactions.Config{
			"status": "snoozed", "snooze_for": map[string]any{"amount": 2.0, "unit": "hours"}}},
		{crmactions.TypeCallWebhook, crmactions.Config{"url": "https://example.com/hook"}},
	}

	for _, c := range cases {
		assert.NoError(t, crmactions.Validate(c.actionType, c.config), c.actionType)
	}
}
