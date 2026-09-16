package handlers

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The picker must offer every event the catalog marks deliverable. The list
// used to be hand-maintained and had drifted: none of the conversation, task or
// deal events plans 03, 04 and 07 added could be subscribed to, so the webhooks
// those plans promise were unreachable however correctly they fired.
func TestAvailableWebhookEventsCoverTheCatalog(t *testing.T) {
	offered := map[string]bool{}
	for _, e := range availableWebhookEvents() {
		offered[e["value"]] = true
		assert.NotEmpty(t, e["label"], "%s needs a label", e["value"])
	}

	catalogTypes := crmevents.WebhookEventTypes()
	require.NotEmpty(t, catalogTypes)
	for _, t2 := range catalogTypes {
		assert.True(t, offered[t2], "%s is deliverable but is not offered in the picker", t2)
	}
	assert.Len(t, offered, len(catalogTypes), "the picker must not offer events that cannot be delivered")
}

// Every offered event has wording, so the settings page does not show raw
// event identifiers to an administrator.
func TestWebhookEventLabelsAreComplete(t *testing.T) {
	for _, t2 := range crmevents.WebhookEventTypes() {
		meta, ok := webhookEventLabels[t2]
		assert.True(t, ok, "%s has no label", t2)
		assert.NotEmpty(t, meta.Description, "%s has no description", t2)
	}
}

func TestIsKnownWebhookEvent(t *testing.T) {
	assert.True(t, isKnownWebhookEvent("message.incoming"))
	assert.True(t, isKnownWebhookEvent("contact.updated"))

	assert.False(t, isKnownWebhookEvent("not.an.event"))
	assert.False(t, isKnownWebhookEvent(""))

	// In the catalog but deliberately not a webhook: subscribing would create
	// a subscription that never fires.
	assert.False(t, isKnownWebhookEvent("contact.field_changed"),
		"field_changed is carried by contact.updated, not delivered on its own")
}
