package contactquery_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func leaf(field, operator string, value any) contactquery.Node {
	return contactquery.Node{Field: field, Operator: operator, Value: value}
}

func group(op string, rules ...contactquery.Node) contactquery.Node {
	return contactquery.Node{Op: op, Rules: rules}
}

// run compiles a filter and returns the matching contacts.
func run(t *testing.T, db *gorm.DB, v contactquery.Viewer, f contactquery.Filter) []models.Contact {
	t.Helper()
	q, err := contactquery.Apply(db.Model(&models.Contact{}), contactquery.NewRegistry(), v, f)
	require.NoError(t, err)

	var out []models.Contact
	require.NoError(t, q.Find(&out).Error)
	return out
}

func ids(contacts []models.Contact) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(contacts))
	for _, c := range contacts {
		out[c.ID] = true
	}
	return out
}

func adminViewer(orgID, userID uuid.UUID) contactquery.Viewer {
	return contactquery.Viewer{OrgID: orgID, UserID: userID, CanSeeAllContacts: true, Location: time.UTC}
}

// --- Validation ---

func TestValidate_RejectsUnknownFields(t *testing.T) {
	err := contactquery.Validate(contactquery.NewRegistry(), leaf("secret_column", "equals", "x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

// A field name reaching SQL from the request would be an injection point. The
// registry is the only source of column names, so an unknown field must be
// rejected rather than passed through.
func TestValidate_RejectsInjectionAttemptAsFieldName(t *testing.T) {
	err := contactquery.Validate(contactquery.NewRegistry(),
		leaf("contacts.id; DROP TABLE contacts--", "equals", "x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestValidate_RejectsOperatorTheFieldDoesNotSupport(t *testing.T) {
	err := contactquery.Validate(contactquery.NewRegistry(), leaf("tags", "starts_with", "VIP"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support operator")
}

func TestValidate_EnforcesNestingDepth(t *testing.T) {
	deep := leaf("profile_name", "equals", "x")
	for i := 0; i < contactquery.MaxDepth+2; i++ {
		deep = group("and", deep)
	}
	err := contactquery.Validate(contactquery.NewRegistry(), deep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nests deeper")
}

func TestValidate_EnforcesRuleCount(t *testing.T) {
	rules := make([]contactquery.Node, 0, contactquery.MaxRules+1)
	for i := 0; i <= contactquery.MaxRules; i++ {
		rules = append(rules, leaf("profile_name", "equals", fmt.Sprintf("n%d", i)))
	}
	err := contactquery.Validate(contactquery.NewRegistry(), group("and", rules...))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than")
}

func TestValidate_EnforcesValueLimits(t *testing.T) {
	long := make([]byte, contactquery.MaxStringLen+1)
	for i := range long {
		long[i] = 'a'
	}
	err := contactquery.Validate(contactquery.NewRegistry(), leaf("profile_name", "equals", string(long)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "longer than")

	big := make([]any, contactquery.MaxListLen+1)
	for i := range big {
		big[i] = "t"
	}
	err = contactquery.Validate(contactquery.NewRegistry(), leaf("tags", "contains_any", big))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than")
}

func TestValidate_RequiresAValueWhenTheOperatorNeedsOne(t *testing.T) {
	err := contactquery.Validate(contactquery.NewRegistry(), leaf("profile_name", "equals", nil))
	require.Error(t, err)

	// Operators that take no value must be accepted without one.
	assert.NoError(t, contactquery.Validate(contactquery.NewRegistry(), leaf("profile_name", "is_empty", nil)))
	assert.NoError(t, contactquery.Validate(contactquery.NewRegistry(), leaf("assigned_user_id", "is_unassigned", nil)))
}

// --- Compilation against real data ---

func TestApply_TextOperators(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	alice := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15551110001"), testutil.WithProfileName("Alice Smith"))
	testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15551110002"), testutil.WithProfileName("Bob Jones"))

	got := ids(run(t, db, v, leaf("profile_name", "contains", "Alice")))
	assert.True(t, got[alice.ID])
	assert.Len(t, got, 1)

	got = ids(run(t, db, v, leaf("profile_name", "starts_with", "Bob")))
	assert.Len(t, got, 1)

	got = ids(run(t, db, v, leaf("profile_name", "equals", "Alice Smith")))
	assert.True(t, got[alice.ID])
}

// A "%" someone typed into a search box must match a literal percent sign, not
// every row in the table.
func TestApply_EscapesLikeWildcards(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	literal := testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15551120001"), testutil.WithProfileName("100% Cotton"))
	testutil.CreateTestContactWith(t, db, org.ID,
		testutil.WithPhoneNumber("15551120002"), testutil.WithProfileName("Plain Name"))

	got := ids(run(t, db, v, leaf("profile_name", "contains", "%")))
	assert.True(t, got[literal.ID])
	assert.Len(t, got, 1, "a typed %% must not match everything")
}

func TestApply_TagOperators(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	vip := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551130001"))
	lead := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551130002"))
	none := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551130003"))

	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", vip.ID).
		Update("tags", models.JSONBArray{"VIP", "Lead"}).Error)
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", lead.ID).
		Update("tags", models.JSONBArray{"Lead"}).Error)

	got := ids(run(t, db, v, leaf("tags", "contains_any", []any{"VIP", "Lead"})))
	assert.True(t, got[vip.ID])
	assert.True(t, got[lead.ID])
	assert.False(t, got[none.ID])

	got = ids(run(t, db, v, leaf("tags", "contains_all", []any{"VIP", "Lead"})))
	assert.True(t, got[vip.ID])
	assert.False(t, got[lead.ID], "contains_all needs every tag")

	got = ids(run(t, db, v, leaf("tags", "contains_none", []any{"VIP"})))
	assert.False(t, got[vip.ID])
	assert.True(t, got[lead.ID])
	assert.True(t, got[none.ID])
}

func TestApply_BooleanIsFalseIncludesNulls(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	optedIn := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551140001"))
	optedOut := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551140002"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", optedOut.ID).
		Update("marketing_opt_out", true).Error)

	got := ids(run(t, db, v, leaf("marketing_opt_out", "is_false", nil)))
	assert.True(t, got[optedIn.ID], "a contact who never opted out counts as not opted out")
	assert.False(t, got[optedOut.ID])
}

// NOT IN must keep rows where the column is NULL: a contact with no source is
// genuinely "not in" any list, and SQL's three-valued logic would drop it.
func TestApply_NotInKeepsNulls(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	withSource := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551150001"))
	withoutSource := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551150002"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", withSource.ID).
		Update("source", "import").Error)

	got := ids(run(t, db, v, leaf("source", "not_in", []any{"import"})))
	assert.False(t, got[withSource.ID])
	assert.True(t, got[withoutSource.ID], "a contact with no source is not in the list")
}

func TestApply_UserOperators(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	v := adminViewer(org.ID, owner.ID)

	mine := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551160001"))
	unassigned := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551160002"))
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", mine.ID).
		Update("assigned_user_id", owner.ID).Error)

	got := ids(run(t, db, v, leaf("assigned_user_id", "is_me", nil)))
	assert.True(t, got[mine.ID])
	assert.False(t, got[unassigned.ID])

	got = ids(run(t, db, v, leaf("assigned_user_id", "is_unassigned", nil)))
	assert.True(t, got[unassigned.ID])
	assert.False(t, got[mine.ID])
}

func TestApply_DateWithinLast(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	v := adminViewer(org.ID, uuid.New())

	recent := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551170001"))
	old := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551170002"))

	now := time.Now()
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", recent.ID).
		Update("last_inbound_at", now.Add(-2*24*time.Hour)).Error)
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", old.ID).
		Update("last_inbound_at", now.Add(-90*24*time.Hour)).Error)

	got := ids(run(t, db, v, leaf("last_inbound_at", "within_last",
		map[string]any{"amount": float64(30), "unit": "days"})))
	assert.True(t, got[recent.ID])
	assert.False(t, got[old.ID])

	got = ids(run(t, db, v, leaf("last_inbound_at", "more_than_ago",
		map[string]any{"amount": float64(30), "unit": "days"})))
	assert.True(t, got[old.ID])
	assert.False(t, got[recent.ID])
}

// --- Boolean groups ---

func TestApply_NestedAndOr(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	v := adminViewer(org.ID, owner.ID)

	mineVIP := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551180001"))
	unassignedVIP := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551180002"))
	otherVIP := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551180003"))
	someoneElse := testutil.CreateTestUser(t, db, org.ID)

	for _, c := range []uuid.UUID{mineVIP.ID, unassignedVIP.ID, otherVIP.ID} {
		require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", c).
			Update("tags", models.JSONBArray{"VIP"}).Error)
	}
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", mineVIP.ID).
		Update("assigned_user_id", owner.ID).Error)
	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", otherVIP.ID).
		Update("assigned_user_id", someoneElse.ID).Error)

	// VIP AND (mine OR unassigned)
	filter := group("and",
		leaf("tags", "contains_any", []any{"VIP"}),
		group("or",
			leaf("assigned_user_id", "is_me", nil),
			leaf("assigned_user_id", "is_unassigned", nil),
		),
	)

	got := ids(run(t, db, v, filter))
	assert.True(t, got[mineVIP.ID])
	assert.True(t, got[unassignedVIP.ID])
	assert.False(t, got[otherVIP.ID], "someone else's VIP must not match")
}

// --- Scope and org isolation ---

func TestApply_AlwaysScopesToTheOrganization(t *testing.T) {
	db := testutil.SetupTestDB(t)
	mine := testutil.CreateTestOrganization(t, db)
	other := testutil.CreateTestOrganization(t, db)

	ours := testutil.CreateTestContactWith(t, db, mine.ID, testutil.WithPhoneNumber("15551190001"))
	theirs := testutil.CreateTestContactWith(t, db, other.ID, testutil.WithPhoneNumber("15551190002"))

	got := ids(run(t, db, adminViewer(mine.ID, uuid.New()), contactquery.Filter{}))
	assert.True(t, got[ours.ID])
	assert.False(t, got[theirs.ID], "an empty filter must still be scoped to one organization")
}

// An agent sees contacts they own plus any contact actively transferred to
// them, and nothing else — whatever the filter says.
func TestApply_RestrictsAnAgentToVisibleContacts(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	account := testutil.CreateTestWhatsAppAccount(t, db, org.ID)
	agent := testutil.CreateTestUser(t, db, org.ID)

	owned := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551200001"))
	transferred := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551200002"))
	hidden := testutil.CreateTestContactWith(t, db, org.ID, testutil.WithPhoneNumber("15551200003"))

	require.NoError(t, db.Model(&models.Contact{}).Where("id = ?", owned.ID).
		Update("assigned_user_id", agent.ID).Error)
	require.NoError(t, db.Create(&models.AgentTransfer{
		BaseModel:       models.BaseModel{ID: uuid.New()},
		OrganizationID:  org.ID,
		ContactID:       transferred.ID,
		AgentID:         &agent.ID,
		WhatsAppAccount: account.Name,
		PhoneNumber:     transferred.PhoneNumber,
		Status:          models.TransferStatusActive,
		Source:          models.TransferSourceManual,
	}).Error)

	viewer := contactquery.Viewer{OrgID: org.ID, UserID: agent.ID, Location: time.UTC}
	got := ids(run(t, db, viewer, contactquery.Filter{}))

	assert.True(t, got[owned.ID])
	assert.True(t, got[transferred.ID], "an active transfer grants visibility")
	assert.False(t, got[hidden.ID], "an agent must not see unrelated contacts")
}

// --- Registry ---

func TestRegistry_SortIsWhitelisted(t *testing.T) {
	r := contactquery.NewRegistry()

	col, ok := r.SortColumn("last_message_at")
	assert.True(t, ok)
	assert.Equal(t, "contacts.last_message_at", col)

	_, ok = r.SortColumn("marketing_opt_out")
	assert.False(t, ok, "a non-sortable field must be rejected")

	_, ok = r.SortColumn("contacts.id; DROP TABLE contacts--")
	assert.False(t, ok, "sort keys reach SQL, so they must come from the registry")
}

func TestRegistry_ExposesOperatorsPerType(t *testing.T) {
	r := contactquery.NewRegistry()

	tags, ok := r.Lookup("tags")
	require.True(t, ok)
	assert.Equal(t, contactquery.TypeTags, tags.Type)
	assert.Contains(t, contactquery.OperatorsFor(tags.Type), contactquery.OpContainsAny)
	assert.NotContains(t, contactquery.OperatorsFor(tags.Type), contactquery.OpStartsWith)
}

func TestRegistry_LaterPlansCanRegisterFields(t *testing.T) {
	r := contactquery.NewRegistry()
	r.Register(contactquery.Field{
		Key: "field.lifecycle_stage", LabelKey: "contacts.lifecycleStage",
		Type: contactquery.TypeOption, Column: "contacts.source",
	})

	_, ok := r.Lookup("field.lifecycle_stage")
	assert.True(t, ok)
	assert.NoError(t, contactquery.Validate(r, leaf("field.lifecycle_stage", "in", []any{"lead"})))
}

func TestParseFilter(t *testing.T) {
	f, err := contactquery.ParseFilter([]byte(`{
		"op": "and",
		"rules": [
			{"field": "tags", "operator": "contains_any", "value": ["VIP"]},
			{"op": "or", "rules": [{"field": "assigned_user_id", "operator": "is_me"}]}
		]
	}`))
	require.NoError(t, err)
	require.NoError(t, contactquery.Validate(contactquery.NewRegistry(), f))

	assert.Equal(t, "and", f.Op)
	require.Len(t, f.Rules, 2)
	assert.Equal(t, "tags", f.Rules[0].Field)
	assert.True(t, f.Rules[1].IsGroup())
}

func TestParseFilter_EmptyIsAllowed(t *testing.T) {
	f, err := contactquery.ParseFilter(nil)
	require.NoError(t, err)
	assert.True(t, f.IsEmpty())
}
