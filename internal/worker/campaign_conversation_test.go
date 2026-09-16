package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/contacts"
	"github.com/shridarpatil/whatomate/internal/conversation"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/queue"
	"github.com/shridarpatil/whatomate/pkg/whatsapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubMeta points the worker at a fake Meta endpoint that accepts every send.
func stubMeta(t *testing.T, w *Worker, account *models.WhatsAppAccount) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"messages": []map[string]any{{"id": "wamid.campaignconv"}},
		})
	}))
	t.Cleanup(server.Close)

	require.NoError(t, w.DB.Model(account).Update("api_version", "v21.0").Error)
	w.WhatsApp = whatsapp.NewWithBaseURL(w.Log, server.URL)
}

// A campaign blast must not open a conversation per recipient. Ten thousand
// marketing sends are not ten thousand support conversations, and opening them
// would bury the inbox and make every response-time report meaningless.
func TestCampaignSend_DoesNotOpenAConversation(t *testing.T) {
	w, msg, contact, org := runCampaignJob(t, false)

	var count int64
	require.NoError(t, w.DB.Model(&models.Conversation{}).
		Where("organization_id = ? AND contact_id = ?", org.ID, contact.ID).
		Count(&count).Error)
	assert.Zero(t, count, "a campaign send must not create a conversation")

	assert.Empty(t, msg.ConversationID,
		"with no conversation open there is nothing to link the message to")
}

// Where a conversation is already open, the campaign send belongs to it. The
// worker used to write the Message row directly, so the thread showed a message
// the conversation did not know about: its message_count and last_message_at
// disagreed with the transcript (plan 10, S4/X3).
func TestCampaignSend_LinksToAnOpenConversation(t *testing.T) {
	w := testWorker(t)
	org, account, _, campaign, recipient := createTestCampaignData(t, w)
	stubMeta(t, w, account)

	// The customer wrote in first, which is what opens a conversation.
	contact, _, err := contacts.New(w.DB).Resolve(context.Background(), org.ID,
		contacts.Identity{Phone: recipient.PhoneNumber}, contacts.ResolveOpts{
			CreateIfMissing: true,
			Source:          contacts.SourceInbound,
			Actor:           crmevents.SystemActor(),
		})
	require.NoError(t, err)
	require.NotNil(t, contact)

	conv, err := conversation.New(w.DB).TouchInbound(context.Background(), org.ID,
		contact.ID, account.Name, time.Now().Add(-time.Hour), false)
	require.NoError(t, err)
	require.NotNil(t, conv, "the inbound message should have opened a conversation")
	before := conv.MessageCount

	require.NoError(t, w.HandleRecipientJob(context.Background(), &queue.RecipientJob{
		CampaignID:     campaign.ID,
		RecipientID:    recipient.ID,
		OrganizationID: org.ID,
		PhoneNumber:    recipient.PhoneNumber,
		RecipientName:  recipient.RecipientName,
		TemplateParams: recipient.TemplateParams,
	}))

	var updated models.Conversation
	require.NoError(t, w.DB.Where("id = ?", conv.ID).First(&updated).Error)

	assert.Equal(t, before+1, updated.MessageCount,
		"the open conversation must count the campaign send")
	assert.Nil(t, updated.FirstResponseAt,
		"a campaign send is not an agent answering the customer")

	var msg models.Message
	require.NoError(t, w.DB.Where("organization_id = ? AND sender_type = ?",
		org.ID, models.SenderCampaign).First(&msg).Error)
	assert.Equal(t, conv.ID.String(), msg.ConversationID,
		"the message must point at the conversation it belongs to")
}
