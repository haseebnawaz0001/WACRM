package automation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/automation"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func tagStep(id string, tags ...any) automation.ActionSpec {
	return automation.ActionSpec{ID: id, Type: crmactions.TypeAddTags, Config: crmactions.Config{"tags": tags}}
}

// hasTag is the question "does this contact carry the tag?", in the filter
// language a condition step asks it in.
func hasTag(tag string) crmactions.Config {
	return crmactions.Config{"filter": map[string]any{
		"op": "and",
		"rules": []any{map[string]any{
			"field": "tags", "operator": "contains_any", "value": []any{tag},
		}},
	}}
}

func waitStep(id string, amount float64, unit string) automation.ActionSpec {
	return automation.ActionSpec{ID: id, Type: automation.StepWait,
		Config: crmactions.Config{"for": map[string]any{"amount": amount, "unit": unit}}}
}

// A question splits the path. "VIPs get a follow-up, everyone else a tag" is
// one rule, and the history records which way this contact went.
func TestFlow_AQuestionSendsEachContactDownOnePath(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	enabled := true
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Split", TriggerType: "contact.tag_added", Enabled: &enabled,
		TriggerConfig: map[string]any{"tags": []any{"Lead"}},
		Actions: []automation.ActionSpec{{
			ID: "q1", Type: automation.StepCondition, Config: hasTag("VIP"),
			Then: []automation.ActionSpec{tagStep("yes", "Priority")},
			Else: []automation.ActionSpec{tagStep("no", "Standard")},
		}},
	})
	require.NoError(t, err)

	vip := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithTags("VIP"))

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, vip.ID, "Lead"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Contains(t, contactTags(t, db, vip.ID), "Priority")
	assert.NotContains(t, contactTags(t, db, vip.ID), "Standard")

	_, err = engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)
	assert.Contains(t, contactTags(t, db, contact.ID), "Standard")
	assert.NotContains(t, contactTags(t, db, contact.ID), "Priority")

	var run models.AutomationRun
	require.NoError(t, db.Where("rule_id = ? AND contact_id = ?", rule.ID, contact.ID).First(&run).Error)
	list := run.ActionResults["list"].([]any)
	require.Len(t, list, 2, "the question and the one step on its path; the other branch is absent")
	question := list[0].(map[string]any)
	assert.Equal(t, "q1", question["id"])
	assert.Equal(t, "else", question["output"].(map[string]any)["branch"])
}

// A wait parks the run. Nothing after it happens yet; when the wait is over
// the same run carries on and finishes.
func TestFlow_AWaitParksTheRunAndResumesItLater(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	enabled := true
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Later", TriggerType: "contact.tag_added", Enabled: &enabled,
		Actions: []automation.ActionSpec{
			tagStep("now", "Seen"),
			waitStep("w1", 1, "days"),
			tagStep("later", "Followed up"),
		},
	})
	require.NoError(t, err)

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, models.AutomationWaiting, runs[0].Status)
	assert.Contains(t, contactTags(t, db, contact.ID), "Seen")
	assert.NotContains(t, contactTags(t, db, contact.ID), "Followed up", "nothing after the wait happens yet")

	counts, err := engine.WaitingCounts(ctx(), rule.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, counts["w1"])

	// Not due yet: a tick does nothing.
	resumed, err := engine.ResumeWaits(ctx())
	require.NoError(t, err)
	assert.Zero(t, resumed)

	makeDue(t, db, rule.ID)
	resumed, err = engine.ResumeWaits(ctx())
	require.NoError(t, err)
	assert.Equal(t, 1, resumed)
	assert.Contains(t, contactTags(t, db, contact.ID), "Followed up")

	var run models.AutomationRun
	require.NoError(t, db.First(&run, runs[0].ID).Error)
	assert.Equal(t, models.AutomationSucceeded, run.Status, "one run, one journey, finished")
	assert.NotNil(t, run.FinishedAt)

	// A second tick finds nothing: the wait was claimed once.
	resumed, err = engine.ResumeWaits(ctx())
	require.NoError(t, err)
	assert.Zero(t, resumed)
}

// A wait inside a branch resumes on the rest of that branch and then carries
// on after the question — the path continues outward.
func TestFlow_AWaitInsideABranchResumesOutward(t *testing.T) {
	db, svc, engine, org, _, _ := setup(t)
	enabled := true
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Nested", TriggerType: "contact.tag_added", Enabled: &enabled,
		Actions: []automation.ActionSpec{
			{
				ID: "q1", Type: automation.StepCondition, Config: hasTag("VIP"),
				Then: []automation.ActionSpec{waitStep("w1", 2, "hours"), tagStep("inner", "Checked")},
			},
			tagStep("after", "Done"),
		},
	})
	require.NoError(t, err)

	vip := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithTags("VIP"))
	_, err = engine.Handle(ctx(), tagEvent(org.ID, vip.ID, "Lead"))
	require.NoError(t, err)
	assert.NotContains(t, contactTags(t, db, vip.ID), "Done")

	makeDue(t, db, rule.ID)
	_, err = engine.ResumeWaits(ctx())
	require.NoError(t, err)
	tags := contactTags(t, db, vip.ID)
	assert.Contains(t, tags, "Checked")
	assert.Contains(t, tags, "Done")
}

// Switching a rule off stops everyone waiting inside it; removing the step
// someone waits at stops them too. Either way the run says why.
func TestFlow_AParkedRunIsCancelledWhenItCannotCarryOn(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	enabled := true
	steps := []automation.ActionSpec{waitStep("w1", 1, "days"), tagStep("later", "Followed up")}
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Off", TriggerType: "contact.tag_added", Enabled: &enabled, Actions: steps,
	})
	require.NoError(t, err)

	runs, err := engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)
	_, err = svc.SetEnabled(ctx(), org.ID, rule.ID, false)
	require.NoError(t, err)

	makeDue(t, db, rule.ID)
	_, err = engine.ResumeWaits(ctx())
	require.NoError(t, err)

	var run models.AutomationRun
	require.NoError(t, db.First(&run, runs[0].ID).Error)
	assert.Equal(t, models.AutomationCancelled, run.Status)
	assert.Equal(t, models.CancelRuleOff, run.SkipReason)
	assert.NotContains(t, contactTags(t, db, contact.ID), "Followed up")

	// The step itself removed while someone waits at it.
	other := testutil.CreateTestContact(t, db, org.ID)
	_, err = svc.SetEnabled(ctx(), org.ID, rule.ID, true)
	require.NoError(t, err)
	runs, err = engine.Handle(ctx(), tagEvent(org.ID, other.ID, "Lead"))
	require.NoError(t, err)
	_, err = svc.Update(ctx(), org.ID, rule.ID, automation.Input{
		Name: "Off", TriggerType: "contact.tag_added", Actions: []automation.ActionSpec{tagStep("only", "X")},
	})
	require.NoError(t, err)
	makeDue(t, db, rule.ID)
	_, err = engine.ResumeWaits(ctx())
	require.NoError(t, err)
	var second models.AutomationRun
	require.NoError(t, db.First(&second, runs[0].ID).Error)
	assert.Equal(t, models.AutomationCancelled, second.Status)
	assert.Equal(t, models.CancelStepRemoved, second.SkipReason)
}

// A test shows the whole journey: it does not stop at a wait, and it shows
// which way each question went for this contact.
func TestFlow_ADryRunWalksPastWaits(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Preview", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{
			waitStep("w1", 3, "days"),
			{
				ID: "q1", Type: automation.StepCondition, Config: hasTag("VIP"),
				Then: []automation.ActionSpec{tagStep("yes", "Priority")},
				Else: []automation.ActionSpec{tagStep("no", "Standard")},
			},
		},
	})
	require.NoError(t, err)

	run, err := engine.DryRun(ctx(), rule, tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)
	assert.Equal(t, models.AutomationSucceeded, run.Status)
	list := run.ActionResults["list"].([]any)
	require.Len(t, list, 3)
	assert.Equal(t, "no", list[2].(map[string]any)["id"])
	assert.NotContains(t, contactTags(t, db, contact.ID), "Standard", "a dry run writes nothing")

	var waits int64
	require.NoError(t, db.Model(&models.AutomationWait{}).Where("rule_id = ?", rule.ID).Count(&waits).Error)
	assert.Zero(t, waits)
}

// The shape of a rule is checked whether or not it is on: a question can only
// nest so deep, and only a question splits the path.
func TestValidate_RefusesAShapeTheEngineCannotRun(t *testing.T) {
	_, svc, _, org, _, _ := setup(t)

	deep := automation.ActionSpec{ID: "q4", Type: automation.StepCondition, Config: hasTag("A")}
	for i := 3; i >= 1; i-- {
		deep = automation.ActionSpec{
			ID: "q" + string(rune('0'+i)), Type: automation.StepCondition, Config: hasTag("A"),
			Then: []automation.ActionSpec{deep},
		}
	}
	_, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Deep", TriggerType: "contact.tag_added", Actions: []automation.ActionSpec{deep},
	})
	require.ErrorContains(t, err, "nested at most")

	_, err = svc.Create(ctx(), org.ID, automation.Input{
		Name: "Split tag", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{{
			ID: "a1", Type: crmactions.TypeAddTags, Config: crmactions.Config{"tags": []any{"X"}},
			Then: []automation.ActionSpec{tagStep("a2", "Y")},
		}},
	})
	require.ErrorContains(t, err, "only a question can split")

	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Too long", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{waitStep("w1", 45, "days")},
	})
	require.NoError(t, err, "an over-long wait is unfinished, not malformed")
	problems := svc.ProblemsFor(ctx(), rule)
	require.Len(t, problems, 1)
	assert.Equal(t, "w1", problems[0].StepID)
}

// makeDue moves a rule's waits into the past.
func makeDue(t *testing.T, db *gorm.DB, ruleID any) {
	t.Helper()
	require.NoError(t, db.Model(&models.AutomationWait{}).
		Where("rule_id = ? AND status = ?", ruleID, models.WaitPending).
		Update("resume_at", time.Now().UTC().Add(-time.Minute)).Error)
}

// The builder's variable picker offers the contact's custom fields for
// automations. The engine used to render only name, phone and tags, so a
// follow-up titled "Call {{contact.fields.company}}" came out as "Call ".
func TestFlow_TextCanUseTheContactsCustomFields(t *testing.T) {
	db, svc, engine, org, contact, author := setup(t)
	require.NoError(t, tasks.SeedOrganization(db, org.ID))
	field := models.CustomFieldDefinition{
		BaseModel: models.BaseModel{ID: uuid.New()}, OrganizationID: org.ID,
		EntityType: models.FieldEntityContact, Key: "company", Label: "Company", Type: models.FieldTypeText,
	}
	require.NoError(t, db.Create(&field).Error)
	company := "Globex"
	require.NoError(t, db.Create(&models.CustomFieldValue{
		ID: uuid.New(), OrganizationID: org.ID, EntityType: models.FieldEntityContact,
		EntityID: contact.ID, FieldID: field.ID, ValueText: &company,
	}).Error)

	enabled := true
	_, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Call them", TriggerType: "contact.tag_added", Enabled: &enabled,
		ActorID: &author.ID,
		Actions: []automation.ActionSpec{{
			ID: "t1", Type: crmactions.TypeCreateTask,
			Config: crmactions.Config{"title": "Call {{contact.fields.company}}"},
		}},
	})
	require.NoError(t, err)

	_, err = engine.Handle(ctx(), tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)

	var task models.Task
	require.NoError(t, db.Where("contact_id = ?", contact.ID).First(&task).Error)
	assert.Equal(t, "Call Globex", task.Title)
}

// Trying a rule on a contact assumes the trigger happened: the test event has
// none of the details a real one would (which tag was added), and judging it
// against the trigger's settings answered every test with "nothing would
// happen". Who the rule is for still applies.
func TestDryRun_AssumesTheTriggerButStillChecksWhoItIsFor(t *testing.T) {
	db, svc, engine, org, contact, _ := setup(t)
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "VIP only", TriggerType: "contact.tag_added",
		TriggerConfig: map[string]any{"tags": []any{"VIP"}},
		Actions:       []automation.ActionSpec{tagStep("t1", "Seen")},
	})
	require.NoError(t, err)

	blank := crmevents.New(org.ID, "contact.tag_added", crmevents.SystemActor(), map[string]any{}).ForContact(contact.ID)
	run, err := engine.DryRun(ctx(), rule, blank)
	require.NoError(t, err)
	assert.Equal(t, models.AutomationSucceeded, run.Status)

	filter := contactquery.Filter{Op: "and", Rules: []contactquery.Node{{Field: "tags", Operator: "contains_any", Value: []any{"Nope"}}}}
	_, err = svc.Update(ctx(), org.ID, rule.ID, automation.Input{
		Name: "VIP only", TriggerType: "contact.tag_added", ContactFilter: &filter,
		Actions: []automation.ActionSpec{tagStep("t1", "Seen")},
	})
	require.NoError(t, err)
	rule, err = svc.Get(ctx(), org.ID, rule.ID)
	require.NoError(t, err)
	run, err = engine.DryRun(ctx(), rule, blank)
	require.NoError(t, err)
	assert.Equal(t, models.AutomationSkipped, run.Status)
	assert.Equal(t, models.SkipConditionsNotMet, run.SkipReason)
	_ = db
}

// A draft tried on a contact can hold an unfinished step. It must come back
// as something the rule could not do, not as done.
func TestDryRun_AnUnfinishedStepIsReportedNotDone(t *testing.T) {
	_, svc, engine, org, contact, _ := setup(t)
	rule, err := svc.Create(ctx(), org.ID, automation.Input{
		Name: "Unfinished", TriggerType: "contact.tag_added",
		Actions: []automation.ActionSpec{{ID: "a1", Type: crmactions.TypeAssignConversation, Config: crmactions.Config{"mode": "team"}}},
	})
	require.NoError(t, err)

	run, err := engine.DryRun(ctx(), rule, tagEvent(org.ID, contact.ID, "Lead"))
	require.NoError(t, err)
	assert.Equal(t, models.AutomationFailed, run.Status)
	list := run.ActionResults["list"].([]any)
	first := list[0].(map[string]any)
	assert.Equal(t, "failed", first["status"])
	assert.Contains(t, first["error"], "team")
}
