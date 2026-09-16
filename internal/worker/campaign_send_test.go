package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/queue"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCampaignJob sends one recipient through the worker against a stub Meta
// endpoint and returns the resulting message row.
func runCampaignJob(t *testing.T, failSend bool) (*Worker, *models.Message, *models.Contact, *models.Organization) {
	t.Helper()

	w := testWorker(t)
	org, account, _, campaign, recipient := createTestCampaignData(t, w)

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if failSend {
			rw.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"error": map[string]any{"message": "invalid recipient"},
			})
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"messages": []map[string]any{{"id": "wamid.campaign123"}},
		})
	}))
	t.Cleanup(server.Close)

	require.NoError(t, w.DB.Model(account).Update("api_version", "v21.0").Error)
	w.WhatsApp = whatsapp.NewWithBaseURL(w.Log, server.URL)

	require.NoError(t, w.HandleRecipientJob(context.Background(), &queue.RecipientJob{
		CampaignID:     campaign.ID,
		RecipientID:    recipient.ID,
		OrganizationID: org.ID,
		PhoneNumber:    recipient.PhoneNumber,
		RecipientName:  recipient.RecipientName,
		TemplateParams: recipient.TemplateParams,
	}))

	var msg models.Message
	require.NoError(t, w.DB.Where("organization_id = ?", org.ID).First(&msg).Error)

	var contact models.Contact
	require.NoError(t, w.DB.Where("id = ?", msg.ContactID).First(&contact).Error)

	return w, &msg, &contact, org
}

// A campaign send must be attributable. Without sender_type it is
// indistinguishable from an agent reply, and would inflate first-response and
// agent analytics for every bulk send.
func TestCampaignSend_IsTaggedAsCampaign(t *testing.T) {
	_, msg, _, _ := runCampaignJob(t, false)

	assert.Equal(t, models.SenderCampaign, msg.SenderType)
	assert.False(t, msg.SenderType.CountsAsAgentReply(),
		"a campaign send must never count as an agent reply")
	assert.Equal(t, models.MessageStatusSent, msg.Status)
	assert.Equal(t, "wamid.campaign123", msg.WhatsAppMessageID)
}

// The worker wrote Message rows directly and never touched the contact, so a
// campaign left no trace in the inbox.
func TestCampaignSend_UpdatesContactLastMessage(t *testing.T) {
	_, msg, contact, _ := runCampaignJob(t, false)

	require.NotNil(t, contact.LastMessageAt, "a campaign send updates the contact's last message time")
	assert.NotEmpty(t, contact.LastMessagePreview, "the inbox needs a preview line")
	assert.Equal(t, msg.ContactID, contact.ID)
}

// The worker has no *App and cannot reach the WebSocket hub, but it can write
// an outbox row, which the relay fans out. Before this, campaigns produced no
// message.outgoing event at all.
func TestCampaignSend_WritesOutgoingEventToOutbox(t *testing.T) {
	w, msg, contact, org := runCampaignJob(t, false)

	var events []models.CRMEventOutbox
	require.NoError(t, w.DB.Where("organization_id = ? AND type = ?",
		org.ID, string(models.WebhookEventMessageOutgoing)).Find(&events).Error)

	require.Len(t, events, 1, "a successful campaign send records one outgoing event")
	event := events[0]
	require.NotNil(t, event.ContactID)
	assert.Equal(t, contact.ID, *event.ContactID)
	assert.Equal(t, msg.ID.String(), event.Data["message_id"])
	assert.Equal(t, contact.PhoneNumber, event.Data["contact_phone"])
	assert.Nil(t, event.PublishedAt, "the worker records; the relay publishes")
}

// A failed send must not claim delivery: no contact update, no outgoing event.
func TestCampaignSend_FailureRecordsNoOutgoingEvent(t *testing.T) {
	w, msg, contact, org := runCampaignJob(t, true)

	assert.Equal(t, models.MessageStatusFailed, msg.Status)
	assert.NotEmpty(t, msg.ErrorMessage)
	assert.Equal(t, models.SenderCampaign, msg.SenderType,
		"a failed send is still attributed to the campaign")

	var count int64
	require.NoError(t, w.DB.Model(&models.CRMEventOutbox{}).
		Where("organization_id = ? AND type = ?",
			org.ID, string(models.WebhookEventMessageOutgoing)).Count(&count).Error)
	assert.Zero(t, count, "a failed send must not announce an outgoing message")

	assert.Nil(t, contact.LastMessageAt, "a failed send must not update the contact")
}

// The row has to exist before Meta is called, or a delivery status webhook
// that arrives first has no message to attach to and is dropped.
func TestCampaignSend_MessageRowExistsBeforeMetaIsCalled(t *testing.T) {
	w := testWorker(t)
	org, account, _, campaign, recipient := createTestCampaignData(t, w)

	var existedDuringSend bool
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// Inspect the database from inside the outbound call: at this point
		// the message row must already be persisted.
		var count int64
		_ = w.DB.Model(&models.Message{}).
			Where("organization_id = ? AND direction = ?", org.ID, models.DirectionOutgoing).
			Count(&count).Error
		existedDuringSend = count > 0

		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"messages": []map[string]any{{"id": "wamid.ordering"}},
		})
	}))
	defer server.Close()

	require.NoError(t, w.DB.Model(account).Update("api_version", "v21.0").Error)
	w.WhatsApp = whatsapp.NewWithBaseURL(w.Log, server.URL)

	require.NoError(t, w.HandleRecipientJob(context.Background(), &queue.RecipientJob{
		CampaignID:     campaign.ID,
		RecipientID:    recipient.ID,
		OrganizationID: org.ID,
		PhoneNumber:    recipient.PhoneNumber,
		RecipientName:  recipient.RecipientName,
		TemplateParams: recipient.TemplateParams,
	}))

	assert.True(t, existedDuringSend,
		"the message row must be persisted before the Meta call, not after it returns")
}
