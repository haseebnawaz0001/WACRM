package timeline

import (
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

// Every event the catalog records on a contact's timeline has to read as a
// sentence.
//
// An event with no wording falls through to its own dotted type, so a history
// meant to say what happened shows "conversation.sla_breached" instead. The
// catalog is the list that grows, so the check reads from it rather than from a
// copy kept here — a new activity event fails this until somebody words it.
func TestSummaryForActivity_CoversEveryRecordedEvent(t *testing.T) {
	recorded := crmevents.ActivityEventTypes()
	if len(recorded) == 0 {
		t.Fatal("the catalog records no activity at all, which cannot be right")
	}

	for _, eventType := range recorded {
		row := models.ContactActivity{
			Type:       eventType,
			ActorType:  crmevents.ActorSystem,
			Data:       models.JSONB{},
			OccurredAt: time.Now().UTC(),
		}
		assert.NotEqual(t, eventType, summaryForActivity(row),
			"%s has no wording, so a contact's history shows its raw type", eventType)
	}
}

// A customer reading "changed by System" when the chatbot did it learns
// nothing. The actor's kind is the fallback when it has no name.
func TestSummaryForActivity_NamesTheActorByKind(t *testing.T) {
	cases := map[string]string{
		crmevents.ActorBot:        "the chatbot",
		crmevents.ActorAutomation: "an automation",
		crmevents.ActorContact:    "the customer",
		crmevents.ActorAPI:        "the API",
		crmevents.ActorSystem:     "System",
	}

	for actorType, want := range cases {
		row := models.ContactActivity{
			Type:      "contact.tag_added",
			ActorType: actorType,
			Data:      models.JSONB{"tag": "VIP"},
		}
		assert.Contains(t, summaryForActivity(row), want,
			"an actor of type %q should read as %q", actorType, want)
	}
}

// A named actor is used as it stands; the kind is only a fallback.
func TestSummaryForActivity_PrefersTheActorsName(t *testing.T) {
	row := models.ContactActivity{
		Type:      "contact.tag_added",
		ActorType: crmevents.ActorBot,
		ActorName: "Onboarding flow",
		Data:      models.JSONB{"tag": "VIP"},
	}
	assert.Contains(t, summaryForActivity(row), "Onboarding flow")
}
