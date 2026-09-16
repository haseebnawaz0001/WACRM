package handlers

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

// Each send path must declare who it is. These presets are the only way the
// product distinguishes a human agent reply from a bot, API or system message,
// and response-time metrics depend on getting it right.
func TestSendOptionPresets_DeclareTheirSender(t *testing.T) {
	cases := []struct {
		name string
		opts MessageSendOptions
		want models.SenderType
	}{
		{"agent inbox", DefaultSendOptions(), models.SenderAgent},
		{"chatbot", ChatbotSendOptions(), models.SenderBot},
		{"public API", APISendOptions(), models.SenderAPI},
		{"SLA notice", SLASendOptions(), models.SenderSystem},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.opts.SenderType)
			assert.True(t, tc.opts.SenderType.Valid())
		})
	}
}

// Only a real agent reply may count towards first-response and agent analytics.
func TestOnlyAgentSendsCountAsAgentReplies(t *testing.T) {
	assert.True(t, models.SenderAgent.CountsAsAgentReply())

	for _, s := range []models.SenderType{
		models.SenderContact, models.SenderBot, models.SenderAutomation,
		models.SenderCampaign, models.SenderSystem, models.SenderAPI, models.SenderEcho,
	} {
		assert.False(t, s.CountsAsAgentReply(), "%s must not count as an agent reply", s)
	}
}

// An unset sender must fall back to something that counts towards nothing.
// Defaulting to agent would let any unattributed send inflate response times.
func TestUnsetSenderFallsBackToSystem(t *testing.T) {
	assert.Equal(t, models.SenderSystem, senderTypeOrSystem(""))
	assert.Equal(t, models.SenderSystem, senderTypeOrSystem(models.SenderType("nonsense")))
	assert.Equal(t, models.SenderAgent, senderTypeOrSystem(models.SenderAgent))
}
