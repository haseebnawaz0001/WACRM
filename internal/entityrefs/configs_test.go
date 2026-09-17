package entityrefs_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/entityrefs"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ruleNaming saves an automation rule whose action config mentions a value.
func ruleNaming(t *testing.T, db *gorm.DB, orgID uuid.UUID, name string, value string) uuid.UUID {
	t.Helper()
	rule := models.AutomationRule{
		OrganizationID: orgID,
		Name:           name,
		TriggerType:    "contact.created",
		Actions: models.JSONB{
			"list": []any{
				map[string]any{"type": "send_template", "config": map[string]any{"template_id": value}},
			},
		},
	}
	require.NoError(t, db.Create(&rule).Error)
	return rule.ID
}

func segmentNaming(t *testing.T, db *gorm.DB, orgID, ownerID uuid.UUID, name, value string) uuid.UUID {
	t.Helper()
	segment := models.Segment{
		OrganizationID: orgID,
		Name:           name,
		CreatedByID:    ownerID,
		Filter: models.JSONB{
			"op": "and",
			"children": []any{
				map[string]any{"field": "deal.stage_id", "operator": "is", "value": value},
			},
		},
	}
	require.NoError(t, db.Create(&segment).Error)
	return segment.ID
}

// Deleting a template left the rule that sends it with an id resolving to
// nothing. The rule carried on "running": the send failed per contact, and the
// reason lived in a run record nobody had opened.
func TestFindDependents_FindsRulesAndSegments(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	templateID := uuid.NewString()
	ruleNaming(t, db, org.ID, "Welcome", templateID)
	segmentNaming(t, db, org.ID, owner.ID, "In stage", templateID)

	dependents, err := entityrefs.FindDependents(db, org.ID, templateID)
	require.NoError(t, err)
	require.Len(t, dependents, 2)

	kinds := map[string]bool{}
	for _, d := range dependents {
		kinds[d.Kind] = true
		assert.NotEmpty(t, d.Name, "a dependency nobody can name is not actionable")
	}
	assert.True(t, kinds["automation"])
	assert.True(t, kinds["segment"])
}

// A value that merely contains another must not count: removing the option
// "new" should not report every rule mentioning "renewal".
func TestFindDependents_MatchesWholeValuesOnly(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	ruleNaming(t, db, org.ID, "Renewals", "renewal")

	dependents, err := entityrefs.FindDependents(db, org.ID, "new")
	require.NoError(t, err)
	assert.Empty(t, dependents)
}

func TestFindDependents_IsScopedToTheOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	mine := testutil.CreateTestOrganization(t, db)
	theirs := testutil.CreateTestOrganization(t, db)

	shared := uuid.NewString()
	ruleNaming(t, db, theirs.ID, "Theirs", shared)

	dependents, err := entityrefs.FindDependents(db, mine.ID, shared)
	require.NoError(t, err)
	assert.Empty(t, dependents, "another organization's rules are not ours to report")
}

func TestFindDependents_NothingPointingAtItIsEmpty(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	dependents, err := entityrefs.FindDependents(db, org.ID, uuid.NewString())
	require.NoError(t, err)
	assert.Empty(t, dependents)
}

// A message naming forty rules is a message nobody reads.
func TestDescribeDependents_TruncatesWithACount(t *testing.T) {
	many := make([]entityrefs.Dependent, 0, 8)
	for i := 0; i < 8; i++ {
		many = append(many, entityrefs.Dependent{Kind: "automation", Name: "Rule"})
	}

	described := entityrefs.DescribeDependents(many)
	assert.Contains(t, described, "and 3 more")
	assert.Equal(t, "", entityrefs.DescribeDependents(nil))
}

// A rename should follow into the configs rather than leaving them pointing at
// a value that no longer exists.
func TestRewriteConfigValue_FollowsARename(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	ruleID := ruleNaming(t, db, org.ID, "Stage rule", "qualified")
	segmentID := segmentNaming(t, db, org.ID, owner.ID, "Stage segment", "qualified")

	require.NoError(t, entityrefs.RewriteConfigValue(db, org.ID, "qualified", "Qualified lead"))

	var actions, filter string
	require.NoError(t, db.Raw(`SELECT actions::text FROM automation_rules WHERE id = ?`, ruleID).Scan(&actions).Error)
	require.NoError(t, db.Raw(`SELECT filter::text FROM segments WHERE id = ?`, segmentID).Scan(&filter).Error)

	assert.Contains(t, actions, "Qualified lead")
	assert.NotContains(t, actions, `"qualified"`)
	assert.Contains(t, filter, "Qualified lead")
}

// contact_filter is nullable. Rewriting must leave a NULL alone rather than
// inventing a filter that was never there.
func TestRewriteConfigValue_LeavesNullColumnsNull(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)

	ruleID := ruleNaming(t, db, org.ID, "No filter", "qualified")

	require.NoError(t, entityrefs.RewriteConfigValue(db, org.ID, "qualified", "Qualified lead"))

	var isNull bool
	require.NoError(t, db.Raw(
		`SELECT contact_filter IS NULL FROM automation_rules WHERE id = ?`, ruleID).
		Scan(&isNull).Error)
	assert.True(t, isNull)
}
