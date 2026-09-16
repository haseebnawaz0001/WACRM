package handlers_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type contactSearchPage struct {
	Data struct {
		Contacts []handlers.ContactSearchResult `json:"contacts"`
		Total    int64                          `json:"total"`
		Page     int                            `json:"page"`
		Limit    int                            `json:"limit"`
	} `json:"data"`
}

// searchContacts runs the search handler as the given user.
func searchContacts(t *testing.T, app *handlers.App, orgID, userID uuid.UUID, body map[string]any) contactSearchPage {
	t.Helper()

	req := testutil.NewJSONRequest(t, body)
	testutil.SetAuthContext(req, orgID, userID)
	require.NoError(t, app.SearchContacts(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var page contactSearchPage
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &page))
	return page
}

func resultIDs(page contactSearchPage) map[string]bool {
	out := make(map[string]bool, len(page.Data.Contacts))
	for _, c := range page.Data.Contacts {
		out[c.ID] = true
	}
	return out
}

func TestSearchContacts_ReturnsThePage(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	for i := 0; i < 3; i++ {
		testutil.CreateTestContactWith(t, app.DB, org.ID,
			testutil.WithPhoneNumber(fmt.Sprintf("1555710%04d", i)))
	}

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{"limit": 2})
	assert.Len(t, page.Data.Contacts, 2)
	assert.EqualValues(t, 3, page.Data.Total, "the total counts every match, not just the page")
	assert.Equal(t, 1, page.Data.Page)
}

func TestSearchContacts_FiltersOnACustomField(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	svc := customfields.New(app.DB)

	lead := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557110001"))
	_, err := svc.SetValues(app.DB, org.ID, lead.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyLifecycleStage: "lead"}, nil)
	require.NoError(t, err)

	customer := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557110002"))
	_, err = svc.SetValues(app.DB, org.ID, customer.ID, models.FieldEntityContact,
		map[string]any{models.FieldKeyLifecycleStage: "customer"}, nil)
	require.NoError(t, err)

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{
		"filter": map[string]any{
			"field": "field.lifecycle_stage", "operator": "in", "value": []any{"lead"},
		},
	})

	got := resultIDs(page)
	assert.True(t, got[lead.ID.String()])
	assert.False(t, got[customer.ID.String()])
}

// The list renders field columns, so a page has to carry its values — fetched
// once for the page rather than per row.
func TestSearchContacts_IncludesFieldValues(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557120001"))
	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{"include": []string{"fields"}})
	require.NotEmpty(t, page.Data.Contacts)

	for _, row := range page.Data.Contacts {
		if row.ID == contact.ID.String() {
			assert.Equal(t, "Acme", row.Fields[models.FieldKeyCompany])
			return
		}
	}
	t.Fatal("contact not found in results")
}

func TestSearchContacts_IncludesUnreadCounts(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	account := testutil.CreateTestWhatsAppAccount(t, app.DB, org.ID)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557130001"))
	for i := 0; i < 2; i++ {
		require.NoError(t, app.DB.Create(&models.Message{
			BaseModel:       models.BaseModel{ID: uuid.New()},
			OrganizationID:  org.ID,
			ContactID:       contact.ID,
			WhatsAppAccount: account.Name,
			Direction:       models.DirectionIncoming,
			SenderType:      models.SenderContact,
			MessageType:     models.MessageTypeText,
			Content:         "hello",
			Status:          models.MessageStatusReceived,
		}).Error)
	}

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{"include": []string{"unread"}})
	for _, row := range page.Data.Contacts {
		if row.ID == contact.ID.String() {
			assert.EqualValues(t, 2, row.UnreadCount)
			return
		}
	}
	t.Fatal("contact not found in results")
}

// Sort keys reach SQL, so anything not in the registry's whitelist must be
// ignored rather than interpolated.
func TestSearchContacts_IgnoresUnknownSortKeys(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)
	testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557140001"))

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{
		"sort": []any{
			map[string]any{"field": "contacts.id; DROP TABLE contacts--", "dir": "asc"},
		},
	})
	assert.NotEmpty(t, page.Data.Contacts, "an unknown sort key falls back rather than failing")

	// The table is still there.
	var count int64
	require.NoError(t, app.DB.Model(&models.Contact{}).Count(&count).Error)
	assert.Positive(t, count)
}

func TestSearchContacts_SortsByAllowedField(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557150001"), testutil.WithProfileName("Zoe"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557150002"), testutil.WithProfileName("Adam"))

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{
		"sort": []any{map[string]any{"field": "profile_name", "dir": "asc"}},
	})
	require.GreaterOrEqual(t, len(page.Data.Contacts), 2)
	assert.Equal(t, "Adam", page.Data.Contacts[0].ProfileName)
}

// A malformed filter is the caller's mistake and must be reported, not
// silently ignored — otherwise a segment quietly matches everyone.
func TestSearchContacts_RejectsInvalidFilters(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, map[string]any{
		"filter": map[string]any{"field": "no_such_field", "operator": "equals", "value": "x"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.SearchContacts(req))
	assert.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
}

func TestSearchContacts_FreeTextMatchesNameAndPhone(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	byName := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557160001"), testutil.WithProfileName("Distinctive Name"))
	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15557160002"), testutil.WithProfileName("Someone Else"))

	page := searchContacts(t, app, org.ID, admin.ID, map[string]any{"search": "distinctive"})
	got := resultIDs(page)
	assert.True(t, got[byName.ID.String()])
	assert.EqualValues(t, 1, page.Data.Total)
}

func TestGetContactFilterFields_IncludesCustomFields(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	require.NoError(t, app.GetContactFilterFields(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Fields []struct {
				Key       string   `json:"key"`
				Type      string   `json:"type"`
				Operators []string `json:"operators"`
			} `json:"fields"`
			Sortable []string `json:"sortable"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))

	keys := map[string]bool{}
	for _, f := range result.Data.Fields {
		keys[f.Key] = true
		assert.NotEmpty(t, f.Operators, "%s must declare its operators for the builder", f.Key)
	}

	assert.True(t, keys["tags"], "core fields are offered")
	assert.True(t, keys["field.lifecycle_stage"], "custom fields appear without a frontend change")
	assert.Contains(t, result.Data.Sortable, "last_message_at")
}

// A user who can work the inbox but cannot read all contacts must not widen
// their view through the search endpoint. The default agent role does hold
// contacts:read, so this uses a chat-only role to exercise the scope.
func TestSearchContacts_RespectsContactScope(t *testing.T) {
	app := newTestApp(t)
	org := seedFieldOrg(t, app)
	role := testutil.CreateTestRoleWithKeys(t, app.DB, org.ID, "chat-only",
		[]string{"chat:read", "chat:write"})
	agent := testutil.CreateTestUser(t, app.DB, org.ID, testutil.WithRoleID(&role.ID))

	mine := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557170001"))
	require.NoError(t, app.DB.Model(&models.Contact{}).Where("id = ?", mine.ID).
		Update("assigned_user_id", agent.ID).Error)
	hidden := testutil.CreateTestContactWith(t, app.DB, org.ID, testutil.WithPhoneNumber("15557170002"))

	page := searchContacts(t, app, org.ID, agent.ID, map[string]any{})
	got := resultIDs(page)

	assert.True(t, got[mine.ID.String()])
	assert.False(t, got[hidden.ID.String()], "a chat-only user must not see unrelated contacts")
}
