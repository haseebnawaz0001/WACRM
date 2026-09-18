package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type inboxPage struct {
	Data struct {
		Conversations []handlers.ConversationResponse `json:"conversations"`
		Total         int64                           `json:"total"`
		View          string                          `json:"view"`
	} `json:"data"`
}

// openConversation creates a contact with a live conversation.
func openConversation(t *testing.T, app *handlers.App, orgID uuid.UUID, phone string) *models.Contact {
	t.Helper()
	contact := testutil.CreateTestContactWith(t, app.DB, orgID, testutil.WithPhoneNumber(phone))
	_, err := app.Conversations().TouchInbound(context.Background(), orgID, contact.ID,
		"acct", time.Now().UTC(), true)
	require.NoError(t, err)
	return contact
}

func listInbox(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, view string) inboxPage {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, orgID, userID)
	if view != "" {
		testutil.SetQueryParam(req, "view", view)
	}

	require.NoError(t, app.ListInbox(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var page inboxPage
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &page))
	return page
}

func inboxIDs(page inboxPage) map[string]bool {
	out := map[string]bool{}
	for _, c := range page.Data.Conversations {
		out[c.ContactID] = true
	}
	return out
}

// "What is mine and still open?" is the question the contact list could never
// answer, because there was no conversation state to filter on.
func TestListInbox_MineShowsOnlyMyConversations(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	me := adminFor(t, app, org)
	someoneElse := testutil.CreateTestUser(t, app.DB, org.ID)

	mine := openConversation(t, app, org.ID, "15553100001")
	theirs := openConversation(t, app, org.ID, "15553100002")

	svc := app.Conversations()
	_, err := svc.Assign(context.Background(), org.ID, mine.ID, &me.ID, nil, crmevents.SystemActor())
	require.NoError(t, err)
	_, err = svc.Assign(context.Background(), org.ID, theirs.ID, &someoneElse.ID, nil, crmevents.SystemActor())
	require.NoError(t, err)

	got := inboxIDs(listInbox(t, app, org.ID, me.ID, "mine"))
	assert.True(t, got[mine.ID.String()])
	assert.False(t, got[theirs.ID.String()])
}

// The queue is what a human should pick up. A bot-handled conversation is not
// waiting on a person, so listing it there would pad the queue with work
// nobody needs to do.
func TestListInbox_UnassignedExcludesBotHandled(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	botHandled := openConversation(t, app, org.ID, "15553110001")
	needsAgent := openConversation(t, app, org.ID, "15553110002")
	require.NoError(t, app.DB.Model(&models.Conversation{}).
		Where("contact_id = ?", needsAgent.ID).
		Update("handling", models.HandlingNone).Error)

	queue := inboxIDs(listInbox(t, app, org.ID, admin.ID, "unassigned"))
	assert.True(t, queue[needsAgent.ID.String()])
	assert.False(t, queue[botHandled.ID.String()])

	bot := inboxIDs(listInbox(t, app, org.ID, admin.ID, "bot"))
	assert.True(t, bot[botHandled.ID.String()])
	assert.False(t, bot[needsAgent.ID.String()])
}

// Resolved conversations are history. Showing them by default would make the
// inbox grow without limit and never look finished.
func TestListInbox_HidesResolvedByDefault(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	done := openConversation(t, app, org.ID, "15553120001")
	_, err := app.Conversations().Resolve(context.Background(), org.ID, done.ID,
		models.ResolutionAgent, crmevents.SystemActor())
	require.NoError(t, err)

	got := inboxIDs(listInbox(t, app, org.ID, admin.ID, "all"))
	assert.False(t, got[done.ID.String()])

	// Asking for them explicitly still works.
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetQueryParam(req, "view", "all")
	testutil.SetQueryParam(req, "status", "resolved")
	require.NoError(t, app.ListInbox(req))

	var page inboxPage
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &page))
	assert.True(t, inboxIDs(page)[done.ID.String()])
}

func TestListInbox_RejectsUnknownView(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetQueryParam(req, "view", "everything")

	require.NoError(t, app.ListInbox(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// The inbox must not become a way around contact visibility.
func TestListInbox_RespectsContactVisibility(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "chat-only-inbox",
		[]string{"chat:read", "chat:write"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	mine := openConversation(t, app, org.ID, "15553130001")
	hidden := openConversation(t, app, org.ID, "15553130002")
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", mine.ID).
		Update("assigned_user_id", agent.ID).Error)

	got := inboxIDs(listInbox(t, app, org.ID, agent.ID, "all"))
	assert.True(t, got[mine.ID.String()])
	assert.False(t, got[hidden.ID.String()], "a chat-only agent must not see unrelated conversations")
}

func TestInboxCounts(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	mine := openConversation(t, app, org.ID, "15553140001")
	_, err := app.Conversations().Assign(context.Background(), org.ID, mine.ID,
		&admin.ID, nil, crmevents.SystemActor())
	require.NoError(t, err)

	queued := openConversation(t, app, org.ID, "15553140002")
	require.NoError(t, app.DB.Model(&models.Conversation{}).
		Where("contact_id = ?", queued.ID).
		Update("handling", models.HandlingNone).Error)

	openConversation(t, app, org.ID, "15553140003") // bot-handled

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.GetInboxCounts(req))

	var result struct {
		Data struct {
			Mine, Unassigned, Bot, All int64
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))

	assert.EqualValues(t, 1, result.Data.Mine)
	assert.EqualValues(t, 1, result.Data.Unassigned)
	assert.EqualValues(t, 1, result.Data.Bot)
	assert.EqualValues(t, 3, result.Data.All)
}

// --- Actions ---

func TestResolveConversation(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553150001")

	req := testutil.NewJSONRequest(t, map[string]any{"contact_id": contact.ID.String()})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ResolveConversation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	_, err := app.Conversations().Active(context.Background(), org.ID, contact.ID)
	assert.Error(t, err, "the conversation is no longer active")
}

// A snooze in the past would be woken on the very next tick, which is not what
// anyone means by snoozing.
func TestSnoozeConversation_RejectsPastTimes(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553160001")

	past := time.Now().Add(-time.Hour)
	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "until": past,
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.SnoozeConversation(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestSnoozeConversation(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553170001")

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "until": time.Now().Add(2 * time.Hour),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.SnoozeConversation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	conv, err := app.Conversations().Active(context.Background(), org.ID, contact.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ConversationSnoozed, conv.Status)
}

func TestAssignConversation_RejectsOutsiders(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553180001")

	stranger := uuid.New().String()
	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "assignee_id": stranger,
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.AssignConversation(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

// A contact who has never written has no conversation; that is a normal state,
// not an error the UI should have to special-case.
func TestGetConversation_ReturnsNullWhenNoneActive(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15553190001"))

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	req.RequestCtx.SetUserValue("id", contact.ID.String())

	require.NoError(t, app.GetConversation(req))
	assert.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Conversation *handlers.ConversationResponse `json:"conversation"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))
	assert.Nil(t, result.Data.Conversation)
}

// A handoff the product could not complete — out of hours, or no agent free —
// is a queue item, not a bot conversation. The boolean this replaced could not
// say that, so a customer who asked for a person at 11pm looked exactly like
// one happily talking to the chatbot, and nobody followed up in the morning.
func TestListInbox_UnassignedIncludesSuppressedHandoffs(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	pending := openConversation(t, app, org.ID, "15553110011")
	require.NoError(t, app.DB.Model(&models.Conversation{}).
		Where("contact_id = ?", pending.ID).
		Update("handling", models.HandlingHandoffPending).Error)

	queue := listInbox(t, app, org.ID, admin.ID, "unassigned")
	assert.True(t, inboxIDs(queue)[pending.ID.String()],
		"someone asked for a person; that is exactly what the queue is for")

	bot := inboxIDs(listInbox(t, app, org.ID, admin.ID, "bot"))
	assert.False(t, bot[pending.ID.String()],
		"the bot is not handling a conversation it already handed off")

	var found bool
	for _, c := range queue.Data.Conversations {
		if c.ContactID == pending.ID.String() {
			found = true
			assert.Equal(t, string(models.HandlingHandoffPending), c.Handling,
				"the client needs the state to show why it is waiting")
			assert.False(t, c.BotActive,
				"the compatibility flag must agree with the state it is derived from")
		}
	}
	assert.True(t, found)
}

// notificationsFor reads one user's notifications of a type.
func notificationsFor(t *testing.T, app *handlers.App, userID uuid.UUID, kind string) []models.Notification {
	t.Helper()
	var out []models.Notification
	require.NoError(t, app.DB.Where("user_id = ? AND type = ?", userID, kind).Find(&out).Error)
	return out
}

// Being handed a customer and finding out only when you next happen to open the
// inbox is how a conversation sits unanswered for an afternoon.
func TestAssignConversation_TellsTheNewAssignee(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	agent := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := openConversation(t, app, org.ID, "15553190001")

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "assignee_id": agent.ID.String(),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.AssignConversation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	notes := notificationsFor(t, app, agent.ID, models.NotificationConversationAssigned)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0].Link, contact.ID.String())
}

// Taking a conversation yourself is not news.
func TestAssignConversation_TakingItYourselfIsNotANotification(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := openConversation(t, app, org.ID, "15553190002")

	req := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "assignee_id": admin.ID.String(),
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.AssignConversation(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	assert.Empty(t, notificationsFor(t, app, admin.ID, models.NotificationConversationAssigned))
}

// Unassigning has nobody to tell.
func TestAssignConversation_UnassigningNotifiesNobody(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	agent := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := openConversation(t, app, org.ID, "15553190003")

	assign := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "assignee_id": agent.ID.String(),
	})
	testutil.SetAuthContext(assign, org.ID, admin.ID)
	require.NoError(t, app.AssignConversation(assign))

	unassign := testutil.NewJSONRequest(t, map[string]any{
		"contact_id": contact.ID.String(), "assignee_id": "",
	})
	testutil.SetAuthContext(unassign, org.ID, admin.ID)
	require.NoError(t, app.AssignConversation(unassign))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(unassign))

	assert.Len(t, notificationsFor(t, app, agent.ID, models.NotificationConversationAssigned), 1,
		"only the assignment notified, not the unassignment")
}

// The unanswered view is "who is waiting on us right now", which is not the
// same question as "who has never been answered".
//
// A conversation answered yesterday with a fresh question in it today is
// exactly the one that gets forgotten, and first_response_at cannot see it —
// that field was set the first time anybody replied and never moves again.
func TestListInbox_UnansweredFindsAConversationAnsweredBefore(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	me := adminFor(t, app, org)
	orgID, userID := org.ID, me.ID
	ctx := context.Background()

	// Answered once, so first_response_at is set and stays set.
	answered := testutil.CreateTestContactWith(t, app.DB, orgID, testutil.WithPhoneNumber("919990000001"))
	_, err := app.Conversations().TouchInbound(ctx, orgID, answered.ID, "acct", time.Now().UTC(), false)
	require.NoError(t, err)
	_, err = app.Conversations().RecordOutbound(ctx, orgID, answered.ID,
		models.SenderAgent, &userID, time.Now().UTC())
	require.NoError(t, err)

	// …and then the customer writes again, so somebody is waiting.
	_, err = app.Conversations().TouchInbound(ctx, orgID, answered.ID,
		"acct", time.Now().UTC(), false)
	require.NoError(t, err)

	page := listInbox(t, app, orgID, userID, handlers.InboxViewUnanswered)
	assert.True(t, inboxIDs(page)[answered.ID.String()],
		"a conversation with a new unanswered message belongs in the unanswered view")
}

// Answering it takes it out of the view, which is the only behaviour that makes
// the view worth opening twice.
func TestListInbox_UnansweredDropsOnceAnswered(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	me := adminFor(t, app, org)
	orgID, userID := org.ID, me.ID
	ctx := context.Background()

	waiting := testutil.CreateTestContactWith(t, app.DB, orgID, testutil.WithPhoneNumber("919990000002"))
	_, err := app.Conversations().TouchInbound(ctx, orgID, waiting.ID, "acct", time.Now().UTC(), false)
	require.NoError(t, err)

	page := listInbox(t, app, orgID, userID, handlers.InboxViewUnanswered)
	require.True(t, inboxIDs(page)[waiting.ID.String()], "should start out waiting")

	_, err = app.Conversations().RecordOutbound(ctx, orgID, waiting.ID,
		models.SenderAgent, &userID, time.Now().UTC())
	require.NoError(t, err)

	page = listInbox(t, app, orgID, userID, handlers.InboxViewUnanswered)
	assert.False(t, inboxIDs(page)[waiting.ID.String()],
		"answering should take it out of the unanswered view")
}

// The view exists to be worked top to bottom, so the longest wait comes first.
// Sorted by recency — the inbox default — that person is at the bottom.
func TestListInbox_UnansweredPutsTheLongestWaitFirst(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	me := adminFor(t, app, org)
	orgID, userID := org.ID, me.ID
	ctx := context.Background()

	now := time.Now().UTC()
	oldest := testutil.CreateTestContactWith(t, app.DB, orgID, testutil.WithPhoneNumber("919990000003"))
	_, err := app.Conversations().TouchInbound(ctx, orgID, oldest.ID, "acct", now.Add(-4*time.Hour), false)
	require.NoError(t, err)

	newest := testutil.CreateTestContactWith(t, app.DB, orgID, testutil.WithPhoneNumber("919990000004"))
	_, err = app.Conversations().TouchInbound(ctx, orgID, newest.ID, "acct", now.Add(-5*time.Minute), false)
	require.NoError(t, err)

	page := listInbox(t, app, orgID, userID, handlers.InboxViewUnanswered)
	require.GreaterOrEqual(t, len(page.Data.Conversations), 2)
	assert.Equal(t, oldest.ID.String(), page.Data.Conversations[0].ContactID,
		"the person waiting longest should be the one to answer next")
}
