package automation_test

import (
	"context"
	"testing"

	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rule is built in two steps: name it and pick its trigger, then work out
// what it should do. The first step has to be savable or "Start from scratch"
// is a button that cannot work.
func TestCreate_AllowsADraftWithNoActionsYet(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)
	orgID := org.ID

	rule, err := svc.Create(context.Background(), orgID, automation.Input{
		Name:        "Half-written rule",
		TriggerType: "contact.tag_added",
	})
	require.NoError(t, err)
	assert.False(t, rule.Enabled, "a new rule starts switched off")
	assert.Empty(t, automation.RuleActions(rule))
}

func TestCreate_RefusesToSaveAnEnabledRuleThatDoesNothing(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)
	orgID := org.ID
	on := true

	_, err := svc.Create(context.Background(), orgID, automation.Input{
		Name:        "Switched on, does nothing",
		TriggerType: "contact.tag_added",
		Enabled:     &on,
	})
	assert.ErrorIs(t, err, automation.ErrNoActions)
}

func TestSetEnabled_RefusesARuleWithNothingToDo(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)
	orgID := org.ID

	rule, err := svc.Create(context.Background(), orgID, automation.Input{
		Name:        "Draft",
		TriggerType: "contact.tag_added",
	})
	require.NoError(t, err)

	_, err = svc.SetEnabled(context.Background(), orgID, rule.ID, true)
	assert.ErrorIs(t, err, automation.ErrNoActions, "switching on a rule with no actions is the case that matters")

	// Turning it off is always allowed.
	_, err = svc.SetEnabled(context.Background(), orgID, rule.ID, false)
	assert.NoError(t, err)
}

func TestSetEnabled_AllowsARuleOnceItHasAnAction(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)
	orgID := org.ID

	rule, err := svc.Create(context.Background(), orgID, automation.Input{
		Name:        "Draft",
		TriggerType: "contact.tag_added",
	})
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), orgID, rule.ID, automation.Input{
		Name:        "Now it does something",
		TriggerType: "contact.tag_added",
		Actions:     []automation.ActionSpec{{ID: "a1", Type: crmactions.TypeAddTags, Config: map[string]any{"tags": []any{"seen"}}}},
	})
	require.NoError(t, err)

	on, err := svc.SetEnabled(context.Background(), orgID, rule.ID, true)
	require.NoError(t, err)
	assert.True(t, on.Enabled)
}
