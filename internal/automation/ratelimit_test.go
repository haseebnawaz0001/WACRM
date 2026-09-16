package automation_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An unbounded rule is one bad event away from messaging every customer at
// once, so every rule carries an hourly cap whether its author set one or not.
func TestHandle_RateLimitStopsARunawayRule(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	engine.Redis = testutil.SetupTestRedis(t)

	tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{MaxRunsPerHour: 2},
	})

	for i := 0; i < 2; i++ {
		runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, models.AutomationSucceeded, runs[0].Status)
	}

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Gold"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.SkipRateLimited, runs[0].SkipReason)
}

// A Redis outage must not stop automations: the other limits still apply, and
// silently doing nothing would be worse than running.
func TestHandle_RateLimitIsSkippedWithoutRedis(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	engine.Redis = nil

	tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{MaxRunsPerHour: 1},
	})

	for i := 0; i < 3; i++ {
		runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.NotEqual(t, models.SkipRateLimited, runs[0].SkipReason)
	}
}

// A rule with no policy still gets a cap.
func TestRulePolicy_DefaultsToAnHourlyCap(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{})

	assert.Equal(t, automation.DefaultMaxRunsPerHour, automation.RulePolicy(rule).MaxRunsPerHour)
}
