package automation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *automation.Service, *automation.Engine, *models.Organization, *models.Contact, *models.User) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	user := testutil.CreateTestUser(t, db, org.ID)

	rules := automation.New(db)
	engine := &automation.Engine{
		Rules: rules,
		Deps:  crmactions.Deps{DB: db},
	}
	return db, rules, engine, org, contact, user
}

// tagRule is the smallest useful rule: when a tag is added, add another.
func tagRule(t *testing.T, svc *automation.Service, orgID uuid.UUID, in automation.Input) *models.AutomationRule {
	t.Helper()
	enabled := true
	if in.Name == "" {
		in.Name = "Test rule"
	}
	if in.TriggerType == "" {
		in.TriggerType = "contact.tag_added"
	}
	if in.Actions == nil {
		in.Actions = []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags,
			Config: crmactions.Config{"tags": []any{"Followed up"}},
		}}
	}
	if in.Enabled == nil {
		in.Enabled = &enabled
	}
	rule, err := svc.Create(ctx(), orgID, in)
	require.NoError(t, err)
	return rule
}

func tagEvent(orgID, contactID uuid.UUID, tag string) crmevents.Event {
	return crmevents.New(orgID, "contact.tag_added", crmevents.SystemActor(),
		map[string]any{"tag": tag}).ForContact(contactID)
}

func contactTags(t *testing.T, db *gorm.DB, contactID uuid.UUID) []string {
	t.Helper()
	var contact models.Contact
	require.NoError(t, db.Where("id = ?", contactID).First(&contact).Error)

	out := make([]string, 0, len(contact.Tags))
	for _, tag := range contact.Tags {
		if s, ok := tag.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// --- Validation ---

func TestCreate_RejectsARuleThatDoesNothing(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)

	_, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Empty", TriggerType: "contact.tag_added", Actions: nil,
	})
	require.Error(t, err)
}

// Silently ignoring an unknown setting is how a rule fires far more often than
// its author believes: they filtered on something nothing ever read.
func TestCreate_RejectsTriggerSettingsTheTriggerDoesNotUnderstand(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)

	_, err := svc.Create(ctx(), org.ID, automation.Input{
		Name:          "Typo",
		TriggerType:   "contact.tag_added",
		TriggerConfig: map[string]any{"tag": "VIP"}, // the key is "tags"
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags,
			Config: crmactions.Config{"tags": []any{"X"}},
		}},
	})
	require.ErrorContains(t, err, "does not understand")
}

func TestCreate_RejectsAnUnconfigurableAction(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)

	_, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Broken", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags, Config: crmactions.Config{},
		}},
	})
	require.ErrorContains(t, err, "at least one tag")
}

// A rule that starts messaging customers the moment it is saved leaves no room
// to check it first.
func TestCreate_RulesStartDisabled(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)

	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Careful", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags,
			Config: crmactions.Config{"tags": []any{"X"}},
		}},
	})
	require.NoError(t, err)
	assert.False(t, rule.Enabled)
}

// --- Firing ---

func TestHandle_RunsAMatchingRule(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{})

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationSucceeded, runs[0].Status)

	assert.Contains(t, contactTags(t, db, contact.ID), "Followed up")
}

func TestHandle_IgnoresADisabledRule(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	disabled := false
	tagRule(t, svc, org.ID, automation.Input{Enabled: &disabled})

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	assert.Empty(t, runs)
	assert.NotContains(t, contactTags(t, db, contact.ID), "Followed up")
}

// "Tag added" is rarely what somebody wants; "the VIP tag was added" is.
func TestHandle_TriggerSettingsNarrowWhichEventsCount(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		TriggerConfig: map[string]any{"tags": []any{"VIP"}},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Newsletter"))
	require.NoError(t, err)
	assert.NotContains(t, contactTags(t, db, contact.ID), "Followed up")

	_, err = engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	assert.Contains(t, contactTags(t, db, contact.ID), "Followed up")
}

// "vip" and "VIP" are the same tag to everyone except a string comparison.
func TestHandle_TagMatchingIgnoresCase(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		TriggerConfig: map[string]any{"tags": []any{"VIP"}},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "vip"))
	require.NoError(t, err)
	assert.Contains(t, contactTags(t, db, contact.ID), "Followed up")
}

// A rule must not act on a contact its conditions exclude.
func TestHandle_ConditionsAreCheckedAgainstTheContact(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	other := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15558100001"), testutil.WithProfileName("Wanted"))

	filter := contactquery.Filter{
		Field: "profile_name", Operator: contactquery.OpEquals, Value: "Wanted",
	}
	tagRule(t, svc, org.ID, automation.Input{ContactFilter: &filter})

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationSkipped, runs[0].Status)
	assert.Equal(t, models.SkipConditionsNotMet, runs[0].SkipReason)

	runs, err = engine.Handle(ctx(), tagEvent(org.ID, other.ID, "VIP"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationSucceeded, runs[0].Status)
}

// --- Idempotency ---

// Redelivering a stream entry must not mean a second message to the customer.
func TestHandle_TheSameEventRunsOnlyOnce(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{})

	event := tagEvent(org.ID, contact.ID, "VIP")

	first, err := engine.Handle(ctx(), event)
	require.NoError(t, err)
	require.Len(t, first, 1)

	second, err := engine.Handle(ctx(), event)
	require.NoError(t, err)
	assert.Empty(t, second, "the second delivery is a no-op")

	var runs int64
	require.NoError(t, db.Model(&models.AutomationRun{}).
		Where("event_id = ?", event.ID).Count(&runs).Error)
	assert.Equal(t, int64(1), runs)
}

// --- Loop protection ---

// A rule reacting to its own output is an infinite loop with a customer at the
// end of it.
func TestHandle_ARuleNeverTriggersItself(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{})

	event := tagEvent(org.ID, contact.ID, "VIP")
	event.Origin = crmevents.Origin{RuleID: &rule.ID, Depth: 1}

	runs, err := engine.Handle(ctx(), event)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.SkipSelfTrigger, runs[0].SkipReason)
}

// A chain of rules reacting to each other has to stop somewhere.
func TestHandle_ChainsStopAtTheDepthLimit(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{})

	other := uuid.New()
	event := tagEvent(org.ID, contact.ID, "VIP")
	event.Origin = crmevents.Origin{RuleID: &other, Depth: automation.MaxDepth}

	runs, err := engine.Handle(ctx(), event)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.SkipLoopDepth, runs[0].SkipReason)
}

// Every event an action causes has to carry the chain's length, or the depth
// limit measures nothing.
func TestHandle_ActionEventsCarryTheOriginAndDepth(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)

	var produced models.CRMEventOutbox
	require.NoError(t, db.Where("organization_id = ? AND type = ? AND actor_type = ?",
		org.ID, "contact.tag_added", crmevents.ActorAutomation).First(&produced).Error)

	require.NotNil(t, produced.OriginRuleID)
	assert.Equal(t, rule.ID, *produced.OriginRuleID)
	assert.Equal(t, 1, produced.OriginDepth)
}

// --- Run policies ---

func TestHandle_OncePerContactStopsTheSecondRun(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{OncePerContact: true},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Gold"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.SkipOncePerContact, runs[0].SkipReason)
}

func TestHandle_CooldownStopsARapidSecondRun(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{CooldownMinutes: 60},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Gold"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.SkipCooldown, runs[0].SkipReason)
}

func TestHandle_CooldownExpires(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{CooldownMinutes: 5},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)

	// Pretend the first run was an hour ago.
	require.NoError(t, db.Model(&models.AutomationContactState{}).
		Where("rule_id = ?", rule.ID).
		Update("last_run_at", time.Now().UTC().Add(-time.Hour)).Error)

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Gold"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationSucceeded, runs[0].Status)
}

// --- Failures ---

// "It sent the message but did not create the task" is a different problem
// from either, so it gets its own status.
func TestHandle_PartialFailureIsItsOwnStatus(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		Actions: []automation.ActionSpec{
			{ID: "a1", Type: crmactions.TypeAddTags,
				Config: crmactions.Config{"tags": []any{"Followed up"}}},
			{ID: "a2", Type: crmactions.TypeSetContactOwner,
				Config:          crmactions.Config{"mode": "user", "user_id": uuid.New().String()},
				ContinueOnError: true},
		},
	})

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationPartiallyFailed, runs[0].Status)
}

// The run log has to show the whole intended sequence, or the reader cannot
// tell "it did not run" from "it was never going to".
func TestHandle_ActionsAfterAFailureAreRecordedAsSkipped(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	tagRule(t, svc, org.ID, automation.Input{
		Actions: []automation.ActionSpec{
			{ID: "a1", Type: crmactions.TypeSetContactOwner,
				Config: crmactions.Config{"mode": "user", "user_id": uuid.New().String()}},
			{ID: "a2", Type: crmactions.TypeAddTags,
				Config: crmactions.Config{"tags": []any{"Never applied"}}},
		},
	})

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationFailed, runs[0].Status)

	results := runs[0].ActionResults["list"].([]any)
	require.Len(t, results, 2)
	assert.Equal(t, "skipped", results[1].(map[string]any)["status"])

	assert.NotContains(t, contactTags(t, db, contact.ID), "Never applied")
}

// A rule that has failed ten times in a row is broken, and leaving it running
// only buries the evidence under more failures.
func TestHandle_ARuleThatKeepsFailingTurnsItselfOff(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeSetContactOwner,
			Config: crmactions.Config{"mode": "user", "user_id": uuid.New().String()},
		}},
	})

	for i := 0; i < automation.AutoDisableAfter; i++ {
		_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
		require.NoError(t, err)
	}

	var after models.AutomationRule
	require.NoError(t, db.Where("id = ?", rule.ID).First(&after).Error)
	assert.False(t, after.Enabled, "ten failures in a row is a broken rule")
	assert.GreaterOrEqual(t, after.ConsecutiveFailures, automation.AutoDisableAfter)
}

// Editing a rule is a fresh start: the old failure count would disable a rule
// the author has just fixed.
func TestUpdate_ClearsTheFailureCount(t *testing.T) {
	db, svc, _, org, _, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{})

	require.NoError(t, db.Model(&models.AutomationRule{}).Where("id = ?", rule.ID).
		Update("consecutive_failures", 9).Error)

	updated, err := svc.Update(ctx(), org.ID, rule.ID, automation.Input{
		Name: "Fixed", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags,
			Config: crmactions.Config{"tags": []any{"Followed up"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, updated.ConsecutiveFailures)
}

// --- Dry run ---

// The rule tester has to be safe to press, or nobody will press it.
func TestDryRun_ChangesNothing(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{})

	run, err := engine.DryRun(ctx(), rule, tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.True(t, run.DryRun)
	assert.Equal(t, models.AutomationSucceeded, run.Status)

	assert.NotContains(t, contactTags(t, db, contact.ID), "Followed up",
		"a dry run that tagged the contact would make the tester dangerous")
}

// "What would this do" is the question; "nothing, it is in cooldown" is not
// the answer the author is asking for.
func TestDryRun_IgnoresTheRunPolicies(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	rule := tagRule(t, svc, org.ID, automation.Input{
		RunPolicy: &automation.RunPolicy{OncePerContact: true},
	})

	_, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)

	run, err := engine.DryRun(ctx(), rule, tagEvent(org.ID, contact.ID, "Gold"))
	require.NoError(t, err)
	assert.Equal(t, models.AutomationSucceeded, run.Status)
}

// A dry run still shows the conditions failing, because that is usually why
// somebody is testing.
func TestDryRun_StillReportsConditionsThatDoNotMatch(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	filter := contactquery.Filter{
		Field: "profile_name", Operator: contactquery.OpEquals, Value: "Somebody else",
	}
	rule := tagRule(t, svc, org.ID, automation.Input{ContactFilter: &filter})

	run, err := engine.DryRun(ctx(), rule, tagEvent(org.ID, contact.ID, "VIP"))
	require.NoError(t, err)
	assert.Equal(t, models.SkipConditionsNotMet, run.SkipReason)
}
