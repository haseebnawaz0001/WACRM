package segments_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/segments"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func ctx() context.Context { return context.Background() }

func setup(t *testing.T) (*gorm.DB, *segments.Service, *models.Organization, *models.User) {
	t.Helper()
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	user := testutil.CreateTestUser(t, db, org.ID)
	return db, segments.New(db), org, user
}

// registry builds a query registry that knows about segments.
func registry(t *testing.T, db *gorm.DB, orgID, userID uuid.UUID) *contactquery.Registry {
	t.Helper()
	r := contactquery.NewRegistry()

	available, err := segments.New(db).List(ctx(), orgID, userID)
	require.NoError(t, err)
	segments.RegisterField(r, available)
	return r
}

func tagFilter(tag string) contactquery.Filter {
	return contactquery.Node{Field: "tags", Operator: "contains_any", Value: []any{tag}}
}

func viewerFor(orgID, userID uuid.UUID) contactquery.Viewer {
	return contactquery.Viewer{OrgID: orgID, UserID: userID, CanSeeAllContacts: true}
}

// --- Saving ---

func TestSave_StoresTheFilter(t *testing.T) {
	db, svc, org, user := setup(t)

	segment, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "VIP customers", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	assert.Equal(t, "VIP customers", segment.Name)
	assert.Equal(t, models.SegmentShared, segment.Visibility, "segments are shared unless asked otherwise")
	assert.NotEmpty(t, segment.Filter)
}

// A segment holding a filter that fails when run would surface as a broken
// campaign rather than a bad save, so it is rejected at the point of saving.
func TestSave_RejectsInvalidFilters(t *testing.T) {
	db, svc, org, user := setup(t)

	_, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Broken",
		Filter:  contactquery.Node{Field: "no_such_field", Operator: "equals", Value: "x"},
		ActorID: user.ID,
	})
	assert.ErrorContains(t, err, "unknown field")
}

func TestSave_RequiresAName(t *testing.T) {
	db, svc, org, user := setup(t)

	_, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "   ", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	assert.ErrorContains(t, err, "needs a name")
}

// "VIP" and "vip" must not both exist, or someone picks the wrong one from a
// list that looks like it has duplicates.
func TestSave_NamesAreUniqueIgnoringCase(t *testing.T) {
	db, svc, org, user := setup(t)
	r := registry(t, db, org.ID, user.ID)

	_, err := svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "VIP", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	_, err = svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "vip", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	assert.Error(t, err)
}

// The cached count describes the old filter; leaving it would mislead.
func TestSave_EditingClearsTheCachedCount(t *testing.T) {
	db, svc, org, user := setup(t)
	r := registry(t, db, org.ID, user.ID)

	segment, err := svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "Leads", Filter: tagFilter("Lead"), ActorID: user.ID,
	})
	require.NoError(t, err)

	count := 42
	require.NoError(t, db.Model(&models.Segment{}).Where("id = ?", segment.ID).
		Updates(map[string]any{"contact_count": count, "counted_at": "now()"}).Error)

	updated, err := svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, ID: &segment.ID, Name: "Leads", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)
	assert.Nil(t, updated.ContactCount, "a changed filter invalidates the count")
}

// --- Nesting and cycles ---

// A segment referencing itself would recurse forever when compiled; catching it
// on save turns an infinite query into a clear error.
func TestSave_RejectsSelfReference(t *testing.T) {
	db, svc, org, user := setup(t)
	r := registry(t, db, org.ID, user.ID)

	segment, err := svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "Loop", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	r = registry(t, db, org.ID, user.ID)
	_, err = svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, ID: &segment.ID, Name: "Loop",
		Filter: contactquery.Node{
			Field: segments.FieldKey, Operator: "in", Value: segment.ID.String(),
		},
		ActorID: user.ID,
	})
	assert.ErrorContains(t, err, "cannot reference itself")
}

func TestSave_RejectsReferenceToAMissingSegment(t *testing.T) {
	db, svc, org, user := setup(t)

	_, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Dangling",
		Filter: contactquery.Node{
			Field: segments.FieldKey, Operator: "in", Value: uuid.New().String(),
		},
		ActorID: user.ID,
	})
	assert.ErrorContains(t, err, "does not exist")
}

// --- Applying ---

func TestApply_MatchesTheFilter(t *testing.T) {
	db, svc, org, user := setup(t)

	vip := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15556100001"), testutil.WithTags("VIP"))
	testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15556100002"), testutil.WithTags("Other"))

	segment, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "VIPs", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	query, err := svc.Apply(ctx(), db.Model(&models.Contact{}),
		registry(t, db, org.ID, user.ID), viewerFor(org.ID, user.ID), segment.ID)
	require.NoError(t, err)

	var rows []models.Contact
	require.NoError(t, query.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, vip.ID, rows[0].ID)
}

// A nested reference compiles as one query rather than fetching ids in stages.
func TestApply_ExpandsNestedSegments(t *testing.T) {
	db, svc, org, user := setup(t)

	vip := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15556110001"), testutil.WithTags("VIP"))
	testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15556110002"), testutil.WithTags("Cold"))

	inner, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Inner VIPs", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	outer, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Outer",
		Filter: contactquery.Node{
			Field: segments.FieldKey, Operator: "in", Value: inner.ID.String(),
		},
		ActorID: user.ID,
	})
	require.NoError(t, err)

	query, err := svc.Apply(ctx(), db.Model(&models.Contact{}),
		registry(t, db, org.ID, user.ID), viewerFor(org.ID, user.ID), outer.ID)
	require.NoError(t, err)

	var rows []models.Contact
	require.NoError(t, query.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, vip.ID, rows[0].ID)
}

func TestCount_CachesTheResult(t *testing.T) {
	db, svc, org, user := setup(t)

	for i := 0; i < 3; i++ {
		testutil.CreateTestContactWith(t, db, org.ID,
			testutil.WithPhoneNumber("1555612000"+string(rune('1'+i))),
			testutil.WithTags("VIP"))
	}

	segment, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Counted", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	count, err := svc.Count(ctx(), registry(t, db, org.ID, user.ID), viewerFor(org.ID, user.ID), segment.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, count)

	reloaded, err := svc.Get(ctx(), org.ID, segment.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded.ContactCount)
	assert.Equal(t, 3, *reloaded.ContactCount)
	require.NotNil(t, reloaded.CountedAt, "the list says how stale the number is")
}

// --- Visibility ---

// A half-finished audience someone is still shaping should not clutter
// everyone else's list.
func TestList_PrivateSegmentsBelongToTheirCreator(t *testing.T) {
	db, svc, org, user := setup(t)
	other := testutil.CreateTestUser(t, db, org.ID)
	r := registry(t, db, org.ID, user.ID)

	_, err := svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "My draft", Filter: tagFilter("VIP"),
		Visibility: models.SegmentPrivate, ActorID: user.ID,
	})
	require.NoError(t, err)
	_, err = svc.Save(ctx(), r, segments.SaveInput{
		OrgID: org.ID, Name: "Team wide", Filter: tagFilter("Lead"), ActorID: user.ID,
	})
	require.NoError(t, err)

	mine, err := svc.List(ctx(), org.ID, user.ID)
	require.NoError(t, err)
	assert.Len(t, mine, 2)

	theirs, err := svc.List(ctx(), org.ID, other.ID)
	require.NoError(t, err)
	require.Len(t, theirs, 1)
	assert.Equal(t, "Team wide", theirs[0].Name)
}

// --- Deleting ---

// Deleting a referenced segment would leave the referrer compiling a filter
// that points at nothing, so the block names the dependents.
func TestDelete_BlockedWhileReferenced(t *testing.T) {
	db, svc, org, user := setup(t)

	inner, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Referenced", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	_, err = svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Depends on it",
		Filter: contactquery.Node{
			Field: segments.FieldKey, Operator: "in", Value: inner.ID.String(),
		},
		ActorID: user.ID,
	})
	require.NoError(t, err)

	err = svc.Delete(ctx(), org.ID, inner.ID)
	require.ErrorIs(t, err, segments.ErrInUse)
	assert.Contains(t, err.Error(), "Depends on it", "the error names what is in the way")
}

func TestDelete_RemovesAnUnreferencedSegment(t *testing.T) {
	db, svc, org, user := setup(t)

	segment, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Disposable", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	require.NoError(t, svc.Delete(ctx(), org.ID, segment.ID))
	_, err = svc.Get(ctx(), org.ID, segment.ID)
	assert.ErrorIs(t, err, segments.ErrNotFound)
}

func TestGet_ScopedToTheOrganization(t *testing.T) {
	db, svc, org, user := setup(t)
	other := testutil.CreateTestOrganization(t, db)

	segment, err := svc.Save(ctx(), registry(t, db, org.ID, user.ID), segments.SaveInput{
		OrgID: org.ID, Name: "Ours", Filter: tagFilter("VIP"), ActorID: user.ID,
	})
	require.NoError(t, err)

	_, err = svc.Get(ctx(), other.ID, segment.ID)
	assert.ErrorIs(t, err, segments.ErrNotFound)
}
