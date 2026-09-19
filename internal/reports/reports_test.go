package reports_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/activity"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/reports"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *reports.Service, *models.Organization, reports.Viewer) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	require.NoError(t, customfields.SeedOrganization(db, org.ID))

	return db, reports.New(db), org, reports.Viewer{OrgID: org.ID, SeesEveryone: true}
}

func lastMonth() reports.Range {
	from := time.Now().UTC().AddDate(0, 0, -30)
	to := time.Now().UTC()
	return reports.NewRange(&from, &to, "day", time.UTC)
}

// setSource gives a contact a value for a built-in dropdown field.
func setSource(t *testing.T, db *gorm.DB, orgID, contactID uuid.UUID, key, value string) {
	t.Helper()
	_, err := customfields.New(db).SetValues(db, orgID, contactID,
		models.FieldEntityContact, map[string]any{key: value}, nil)
	require.NoError(t, err)
}

// --- Ranges ---

// "To today" that silently excludes today is the kind of off-by-one that makes
// people stop trusting a report.
func TestNewRange_IncludesTheWholeEndDay(t *testing.T) {
	to := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	r := reports.NewRange(nil, &to, "day", time.UTC)

	assert.Equal(t, 23, r.To.Hour())
	assert.Equal(t, 59, r.To.Minute())
}

// "Last week" means the business's week, not the server's.
func TestNewRange_StartsTheDayInTheOrganizationsTimezone(t *testing.T) {
	karachi, err := time.LoadLocation("Asia/Karachi")
	require.NoError(t, err)

	from := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	r := reports.NewRange(&from, nil, "day", karachi)

	assert.Equal(t, 0, r.From.In(karachi).Hour())
	assert.Equal(t, karachi.String(), r.Location.String())
}

func TestNewRange_FallsBackToADayInterval(t *testing.T) {
	r := reports.NewRange(nil, nil, "fortnight", time.UTC)
	assert.Equal(t, reports.Day, r.Interval)
}

// --- R1: contacts by source ---

func TestContactsBySource_CountsNewContactsPerSource(t *testing.T) {
	db, svc, org, viewer := setup(t)

	inbound := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559100001"))
	campaign := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559100002"))
	setSource(t, db, org.ID, inbound.ID, models.FieldKeySource, "inbound")
	setSource(t, db, org.ID, campaign.ID, models.FieldKeySource, "campaign")

	result, err := svc.ContactsBySource(ctx(), viewer, lastMonth(), "")
	require.NoError(t, err)

	assert.Equal(t, int64(2), result.Total)
	byName := map[string]int64{}
	for _, row := range result.Totals {
		byName[row.Source] = row.Contacts
	}
	assert.Equal(t, int64(1), byName["inbound"])
	assert.Equal(t, int64(1), byName["campaign"])
}

// "40% of new contacts have no source" is itself the finding, so they are a
// visible bucket rather than a silent omission.
func TestContactsBySource_ShowsContactsWithNoSource(t *testing.T) {
	db, svc, org, viewer := setup(t)
	testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559100003"))

	result, err := svc.ContactsBySource(ctx(), viewer, lastMonth(), "")
	require.NoError(t, err)

	require.Len(t, result.Totals, 1)
	assert.Equal(t, reports.UnknownBucket, result.Totals[0].Source)
	assert.Equal(t, 100.0, result.Totals[0].Share)
}

// A source that brings volume but no business is exactly what the report is
// meant to expose.
func TestContactsBySource_CountsWhoBecameACustomer(t *testing.T) {
	db, svc, org, viewer := setup(t)

	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559100004"))
	setSource(t, db, org.ID, contact.ID, models.FieldKeySource, "inbound")

	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID: org.ID, ContactID: contact.ID, Type: "contact.field_changed",
		Actor: crmevents.SystemActor(),
		Data:  map[string]any{"field": models.FieldKeyLifecycleStage, "to": "customer"},
	}))

	result, err := svc.ContactsBySource(ctx(), viewer, lastMonth(), "")
	require.NoError(t, err)
	require.Len(t, result.Totals, 1)
	assert.Equal(t, int64(1), result.Totals[0].BecameCustomer)
}

func TestContactsBySource_IsScopedToTheOrganization(t *testing.T) {
	db, svc, org, viewer := setup(t)
	other := testutil.CreateTestOrganization(t, db)
	testutil.CreateTestContactWith(t, db, other.ID, testutil.WithPhoneNumber("15559100005"))

	result, err := svc.ContactsBySource(ctx(), viewer, lastMonth(), "")
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.Total)
	_ = org
}

// --- R2: lifecycle funnel ---

// A contact that reached a later stage necessarily passed the earlier ones;
// without that, skipping a stage reads as a collapse rather than a shortcut.
func TestLifecycleFunnel_CountsEveryStageReached(t *testing.T) {
	db, svc, org, viewer := setup(t)
	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559200001"))

	// Straight to customer, skipping lead and qualified.
	require.NoError(t, activity.Record(db, activity.Entry{
		OrgID: org.ID, ContactID: contact.ID, Type: "contact.field_changed",
		Actor: crmevents.SystemActor(),
		Data:  map[string]any{"field": models.FieldKeyLifecycleStage, "to": "customer"},
	}))

	funnel, err := svc.LifecycleFunnel(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	require.NotEmpty(t, funnel.Steps)

	byKey := map[string]int64{}
	for _, step := range funnel.Steps {
		byKey[step.Key] = step.Reached
	}
	assert.Equal(t, int64(1), byKey["new"])
	assert.Equal(t, int64(1), byKey["lead"])
	assert.Equal(t, int64(1), byKey["customer"])
	assert.Equal(t, int64(0), byKey["churned"], "nothing reached beyond customer")
}

func TestLifecycleFunnel_ReportsStepConversion(t *testing.T) {
	db, svc, org, viewer := setup(t)

	for i, stage := range []string{"lead", "lead", "customer", "customer"} {
		contact := testutil.CreateTestContactWith(t, db, org.ID,
			testutil.WithPhoneNumber(uniquePhone(i)))
		require.NoError(t, activity.Record(db, activity.Entry{
			OrgID: org.ID, ContactID: contact.ID, Type: "contact.field_changed",
			Actor: crmevents.SystemActor(),
			Data:  map[string]any{"field": models.FieldKeyLifecycleStage, "to": stage},
		}))
	}

	funnel, err := svc.LifecycleFunnel(ctx(), viewer, lastMonth())
	require.NoError(t, err)

	steps := map[string]float64{}
	reached := map[string]int64{}
	for _, step := range funnel.Steps {
		steps[step.Key] = step.Conversion
		reached[step.Key] = step.Reached
	}
	assert.Equal(t, int64(4), reached["lead"])
	assert.Equal(t, int64(2), reached["customer"])
	// Conversion is measured between adjacent stages. Two of the four that
	// reached Lead got as far as Qualified (on their way to Customer), so the
	// drop-off shows at that step.
	assert.InDelta(t, 50.0, steps["qualified"], 0.01)
	assert.InDelta(t, 100.0, steps["customer"], 0.01,
		"everyone who reached Qualified in this period went on to Customer")
}

// A funnel whose counting rule is guessed is a funnel that starts an argument.
func TestLifecycleFunnel_ExplainsHowItCounts(t *testing.T) {
	_, svc, _, viewer := setup(t)

	funnel, err := svc.LifecycleFunnel(ctx(), viewer, lastMonth())
	require.NoError(t, err)
	assert.NotEmpty(t, funnel.Note)
}

func uniquePhone(i int) string {
	return "1555930000" + string(rune('0'+i))
}

// A contact that moved lead → customer in the period is one contact. Counting
// per stage and summing up the funnel counted it at both, so every step before
// customer was inflated by exactly the people who progressed.
func TestLifecycleFunnel_CountsAProgressingContactOnce(t *testing.T) {
	db, svc, org, viewer := setup(t)
	contact := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15559200009"))

	for _, stage := range []string{"lead", "customer"} {
		require.NoError(t, activity.Record(db, activity.Entry{
			OrgID: org.ID, ContactID: contact.ID, Type: "contact.field_changed",
			Actor: crmevents.SystemActor(),
			Data:  map[string]any{"field": models.FieldKeyLifecycleStage, "to": stage},
		}))
	}

	funnel, err := svc.LifecycleFunnel(ctx(), viewer, lastMonth())
	require.NoError(t, err)

	reached := map[string]int64{}
	for _, step := range funnel.Steps {
		reached[step.Key] = step.Reached
	}
	assert.Equal(t, int64(1), reached["lead"], "one person, not two")
	assert.Equal(t, int64(1), reached["customer"])
	for _, step := range funnel.Steps {
		if step.Key == "customer" {
			assert.InDelta(t, 100.0, step.Conversion, 0.01)
		}
	}
}
