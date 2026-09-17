package messaging_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/messaging"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contactWithLastMessage(ago time.Duration) *models.Contact {
	at := time.Now().UTC().Add(-ago)
	return &models.Contact{PhoneNumber: "15551230000", LastMessageAt: &at}
}

func approved(category, body string) *models.Template {
	return &models.Template{
		Name:        "offer",
		Status:      "APPROVED",
		Category:    category,
		BodyContent: body,
	}
}

// The window is opened by the customer. A contact created by an import or a
// campaign has never opened one, so a free-form message to them is not allowed
// however recently the record was touched.
func TestWithinServiceWindow(t *testing.T) {
	assert.True(t, messaging.WithinServiceWindow(contactWithLastMessage(time.Hour), time.Now().UTC()))
	assert.False(t, messaging.WithinServiceWindow(contactWithLastMessage(25*time.Hour), time.Now().UTC()))
	assert.False(t, messaging.WithinServiceWindow(&models.Contact{PhoneNumber: "1555"}, time.Now().UTC()),
		"a contact who has never written has no open window")
	assert.False(t, messaging.WithinServiceWindow(nil, time.Now().UTC()))
}

// The chat handler used to leave this to Meta, which came back as an opaque API
// error the agent could not act on.
func TestCheckFreeText(t *testing.T) {
	require.NoError(t, messaging.CheckFreeText(contactWithLastMessage(time.Hour), time.Now().UTC()))

	err := messaging.CheckFreeText(contactWithLastMessage(48*time.Hour), time.Now().UTC())
	assert.ErrorIs(t, err, messaging.ErrOutsideServiceWindow)

	err = messaging.CheckFreeText(&models.Contact{}, time.Now().UTC())
	assert.ErrorIs(t, err, messaging.ErrNoRecipient)
}

// Consent is about the message, not the mechanism that sent it: a marketing
// template is refused whether a campaign, a rule or an agent triggered it.
func TestCheckTemplate_HonoursMarketingOptOut(t *testing.T) {
	optedOut := contactWithLastMessage(time.Hour)
	optedOut.MarketingOptOut = true

	err := messaging.CheckTemplate(approved("MARKETING", "hello"), optedOut, nil)
	assert.ErrorIs(t, err, messaging.ErrMarketingOptOut)

	// Opting out of marketing is not opting out of a delivery notification or
	// a one-time password.
	require.NoError(t, messaging.CheckTemplate(approved("UTILITY", "hello"), optedOut, nil))
	require.NoError(t, messaging.CheckTemplate(approved("AUTHENTICATION", "hello"), optedOut, nil))
}

// Meta's category arrives capitalised and has been stored both ways over the
// product's life. A case-sensitive comparison silently stopped honouring
// opt-outs for the other spelling.
func TestCheckTemplate_CategoryComparisonIsCaseInsensitive(t *testing.T) {
	optedOut := contactWithLastMessage(time.Hour)
	optedOut.MarketingOptOut = true

	lower := approved("marketing", "hello")
	assert.ErrorIs(t, messaging.CheckTemplate(lower, optedOut, nil), messaging.ErrMarketingOptOut)
}

func TestCheckTemplate_RequiresApproval(t *testing.T) {
	contact := contactWithLastMessage(time.Hour)

	pending := approved("UTILITY", "hello")
	pending.Status = "PENDING"
	assert.ErrorIs(t, messaging.CheckTemplate(pending, contact, nil), messaging.ErrTemplateNotApproved)

	assert.ErrorIs(t, messaging.CheckTemplate(nil, contact, nil), messaging.ErrTemplateMissing,
		"a deleted template is a permanent failure, not something to retry")
}

// Meta rejects the whole send when a parameter is absent. Finding out here
// turns a failed delivery into something the author can fix.
func TestCheckTemplate_NamesMissingParameters(t *testing.T) {
	contact := contactWithLastMessage(time.Hour)
	template := approved("UTILITY", "Hi {{customer_name}}, your order {{order_id}} is ready")

	err := messaging.CheckTemplate(template, contact, map[string]string{"customer_name": "Amara"})
	require.Error(t, err)
	assert.ErrorIs(t, err, messaging.ErrMissingParams)

	var missing *messaging.MissingParamsError
	require.True(t, errors.As(err, &missing))
	assert.Equal(t, []string{"order_id"}, missing.Names)
	assert.Contains(t, err.Error(), "order_id", "the message has to say which one")
}

// A caller that fills parameters per recipient passes nil and is not second
// guessed — the campaign worker does exactly this.
func TestCheckTemplate_NilParamsSkipsTheParameterCheck(t *testing.T) {
	contact := contactWithLastMessage(time.Hour)
	template := approved("UTILITY", "Hi {{customer_name}}")

	require.NoError(t, messaging.CheckTemplate(template, contact, nil))
}

// Whitespace is not a value: a parameter filled with spaces reaches Meta as an
// empty one and is rejected there instead.
func TestMissingParams_TreatsBlankAsAbsent(t *testing.T) {
	template := approved("UTILITY", "Hi {{name}}")
	assert.Equal(t, []string{"name"}, messaging.MissingParams(template, map[string]string{"name": "   "}))
	assert.Empty(t, messaging.MissingParams(template, map[string]string{"name": "Amara"}))
}

func TestBodyParamNames(t *testing.T) {
	positional := approved("UTILITY", "Hi {{1}}, your order {{2}} is ready. Thanks {{1}}.")
	assert.Equal(t, []string{"1", "2"}, messaging.BodyParamNames(positional),
		"in order, without duplicates")

	assert.Empty(t, messaging.BodyParamNames(approved("UTILITY", "No variables here")))
	assert.Nil(t, messaging.BodyParamNames(nil))
}

// A contact with a BSUID but no phone number is still reachable: that is how
// WhatsApp identifies people who message a business account.
func TestCheckTemplate_AcceptsABSUIDOnlyContact(t *testing.T) {
	at := time.Now().UTC()
	contact := &models.Contact{BSUID: "bsuid-1", LastMessageAt: &at}
	require.NoError(t, messaging.CheckTemplate(approved("UTILITY", "hello"), contact, nil))
}
