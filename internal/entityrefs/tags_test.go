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

// segmentWithTag saves a filter that matches on one tag name.
func segmentWithTag(t *testing.T, db *gorm.DB, orgID, ownerID uuid.UUID, tag string) uuid.UUID {
	t.Helper()
	filter := map[string]any{
		"op": "and",
		"children": []any{
			map[string]any{"field": "tag", "operator": "has_any", "value": []string{tag}},
		},
	}
	segment := models.Segment{
		OrganizationID: orgID,
		Name:           "Tagged " + tag + " " + uuid.NewString()[:8],
		CreatedByID:    ownerID,
		Filter:         models.JSONB(filter),
	}
	require.NoError(t, db.Create(&segment).Error)
	return segment.ID
}

func segmentFilterText(t *testing.T, db *gorm.DB, id uuid.UUID) string {
	t.Helper()
	var out string
	require.NoError(t, db.Raw(`SELECT filter::text FROM segments WHERE id = ?`, id).Scan(&out).Error)
	return out
}

// A segment for "vip" kept returning zero contacts after the tag became
// "VIP customer", and said nothing about why.
func TestRenameTag_RewritesSegmentFilters(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	segmentID := segmentWithTag(t, db, org.ID, owner.ID, "vip")

	require.NoError(t, entityrefs.RenameTag(db, org.ID, "vip", "VIP customer"))

	filter := segmentFilterText(t, db, segmentID)
	assert.Contains(t, filter, "VIP customer")
	assert.NotContains(t, filter, `"vip"`)
}

// Renaming "vip" must not turn "vip-2024" into "VIP customer-2024".
func TestRenameTag_MatchesWholeNamesOnly(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	other := segmentWithTag(t, db, org.ID, owner.ID, "vip-2024")

	require.NoError(t, entityrefs.RenameTag(db, org.ID, "vip", "VIP customer"))

	assert.Contains(t, segmentFilterText(t, db, other), "vip-2024",
		"a longer tag that merely starts with the renamed one is untouched")
}

// One organization renaming a tag must not rewrite another's filters.
func TestRenameTag_IsScopedToTheOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	mine := testutil.CreateTestOrganization(t, db)
	theirs := testutil.CreateTestOrganization(t, db)
	myUser := testutil.CreateTestUser(t, db, mine.ID)
	theirUser := testutil.CreateTestUser(t, db, theirs.ID)

	segmentWithTag(t, db, mine.ID, myUser.ID, "vip")
	otherSegment := segmentWithTag(t, db, theirs.ID, theirUser.ID, "vip")

	require.NoError(t, entityrefs.RenameTag(db, mine.ID, "vip", "VIP customer"))

	assert.Contains(t, segmentFilterText(t, db, otherSegment), `"vip"`,
		"another organization's saved filter is not ours to rewrite")
}

// The count is what a confirmation dialog shows before somebody renames
// something forty rules depend on.
func TestCountTagRefs(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	segmentWithTag(t, db, org.ID, owner.ID, "vip")
	segmentWithTag(t, db, org.ID, owner.ID, "vip")
	segmentWithTag(t, db, org.ID, owner.ID, "other")

	usage, err := entityrefs.CountTagRefs(db, org.ID, "vip")
	require.NoError(t, err)
	assert.EqualValues(t, 2, usage.Segments)
	assert.EqualValues(t, 2, usage.Total())
}

func TestRenameTag_NoOpWhenNameIsUnchanged(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)

	segmentID := segmentWithTag(t, db, org.ID, owner.ID, "vip")
	before := segmentFilterText(t, db, segmentID)

	require.NoError(t, entityrefs.RenameTag(db, org.ID, "vip", "vip"))

	assert.Equal(t, before, segmentFilterText(t, db, segmentID))
}
