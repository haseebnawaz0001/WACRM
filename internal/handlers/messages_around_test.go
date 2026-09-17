package handlers_test

import (
	"encoding/json"
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

// numberedMessages writes n messages a minute apart, oldest first, and returns
// their ids in order.
func numberedMessages(t *testing.T, app *handlers.App, orgID, contactID uuid.UUID, n int) []uuid.UUID {
	t.Helper()
	base := time.Now().UTC().Add(-time.Duration(n) * time.Minute)

	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		msg := models.Message{
			OrganizationID:  orgID,
			ContactID:       contactID,
			WhatsAppAccount: "acct",
			Direction:       models.DirectionIncoming,
			MessageType:     "text",
			Content:         "message",
			Status:          models.MessageStatusDelivered,
			SenderType:      models.SenderContact,
		}
		require.NoError(t, app.DB.Create(&msg).Error)
		require.NoError(t, app.DB.Model(&models.Message{}).Where("id = ?", msg.ID).
			Update("created_at", base.Add(time.Duration(i)*time.Minute)).Error)
		ids = append(ids, msg.ID)
	}
	return ids
}

func messagesAround(t *testing.T, app *handlers.App, orgID, userID, contactID uuid.UUID, around string) ([]string, int) {
	t.Helper()
	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, orgID, userID)
	testutil.SetPathParam(req, "id", contactID.String())
	testutil.SetQueryParam(req, "around", around)
	testutil.SetQueryParam(req, "limit", "10")

	require.NoError(t, app.GetMessages(req))
	status := testutil.GetResponseStatusCode(req)

	var body struct {
		Data struct {
			Messages []struct {
				ID string `json:"id"`
			} `json:"messages"`
		} `json:"data"`
	}
	_ = json.Unmarshal(testutil.GetResponseBody(req), &body)

	ids := make([]string, 0, len(body.Data.Messages))
	for _, m := range body.Data.Messages {
		ids = append(ids, m.ID)
	}
	return ids, status
}

// Clicking a burst on the timeline has to land on the messages it describes.
// Paging back from the newest until the right one appears is not a substitute:
// on a busy contact the thing you clicked is hundreds of requests away.
func TestGetMessages_AroundCentresOnTheAnchor(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15554440001"))

	ids := numberedMessages(t, app, org.ID, contact.ID, 40)
	anchor := ids[20]

	got, status := messagesAround(t, app, org.ID, admin.ID, contact.ID, anchor.String())
	require.Equal(t, fasthttp.StatusOK, status)
	require.NotEmpty(t, got)

	assert.Contains(t, got, anchor.String(), "the message you asked for must be on the page")
	assert.LessOrEqual(t, len(got), 10)

	// Context on both sides, which is the point of "around".
	position := -1
	for i, id := range got {
		if id == anchor.String() {
			position = i
		}
	}
	require.GreaterOrEqual(t, position, 1, "there should be older messages above it")
	assert.Less(t, position, len(got)-1, "and newer ones below it")
}

// A timeline burst knows the instant it covers, not necessarily a message id.
func TestGetMessages_AroundAcceptsATimestamp(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15554440002"))

	numberedMessages(t, app, org.ID, contact.ID, 20)

	var middle models.Message
	require.NoError(t, app.DB.Where("contact_id = ?", contact.ID).
		Order("created_at ASC").Offset(10).First(&middle).Error)

	got, status := messagesAround(t, app, org.ID, admin.ID, contact.ID,
		middle.CreatedAt.Format(time.RFC3339))
	require.Equal(t, fasthttp.StatusOK, status)
	assert.NotEmpty(t, got)
}

func TestGetMessages_AroundRejectsNonsense(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15554440003"))

	_, status := messagesAround(t, app, org.ID, admin.ID, contact.ID, "halfway-ish")
	assert.Equal(t, fasthttp.StatusBadRequest, status,
		"a bad anchor is the caller's bug; silently returning the newest page hides it")
}

// A message id from another contact must not become a way to read their thread.
func TestGetMessages_AroundIgnoresAnotherContactsMessage(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	mine := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15554440004"))
	theirs := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15554440005"))

	numberedMessages(t, app, org.ID, mine.ID, 5)
	otherIDs := numberedMessages(t, app, org.ID, theirs.ID, 5)

	_, status := messagesAround(t, app, org.ID, admin.ID, mine.ID, otherIDs[2].String())
	assert.Equal(t, fasthttp.StatusBadRequest, status,
		"an anchor outside this thread does not resolve")
}
