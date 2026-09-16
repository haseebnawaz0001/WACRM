package handlers_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// An export that silently drops the fields an organization added is an export
// of somebody else's record.
func TestGetExportConfig_OffersTheOrganizationsOwnFields(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	req := testutil.NewGETRequest(t)
	testutil.SetAuthContext(req, org.ID, admin.ID)
	testutil.SetPathParam(req, "table", "contacts")

	require.NoError(t, app.GetExportConfig(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	var result struct {
		Data struct {
			Columns []struct {
				Key   string `json:"key"`
				Label string `json:"label"`
			} `json:"columns"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(testutil.GetResponseBody(req), &result))

	keys := map[string]string{}
	for _, column := range result.Data.Columns {
		keys[column.Key] = column.Label
	}
	assert.Contains(t, keys, "phone_number")
	// Namespaced, so a field called "tags" cannot shadow the built-in column.
	assert.Equal(t, "Company", keys[customfields.FieldKeyPrefix+models.FieldKeyCompany])
}

func TestExportData_IncludesCustomFieldValues(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559800001"), testutil.WithProfileName("Dana"))
	_, err := customfields.New(app.DB).SetValues(app.DB, org.ID, contact.ID,
		models.FieldEntityContact, map[string]any{models.FieldKeyCompany: "Acme"}, nil)
	require.NoError(t, err)

	req := testutil.NewJSONRequest(t, map[string]any{
		"table": "contacts",
		"columns": []string{
			"phone_number", "profile_name",
			customfields.FieldKeyPrefix + models.FieldKeyCompany,
		},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ExportData(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	body := string(testutil.GetResponseBody(req))
	assert.Contains(t, body, "Company")
	assert.Contains(t, body, "Acme")
	assert.Contains(t, body, "Dana")
}

// Asking for a field this organization does not have is the caller's mistake,
// and saying which field is wrong is more useful than an empty column.
func TestExportData_RejectsAnUnknownField(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	require.NoError(t, customfields.SeedOrganization(app.DB, org.ID))
	admin := adminFor(t, app, org)

	req := testutil.NewJSONRequest(t, map[string]any{
		"table":   "contacts",
		"columns": []string{"phone_number", customfields.FieldKeyPrefix + "shoe_size"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ExportData(req))
	require.Equal(t, fasthttp.StatusBadRequest, testutil.GetResponseStatusCode(req))
	assert.Contains(t, strings.ToLower(string(testutil.GetResponseBody(req))), "shoe_size")
}

// Every export without fields has to keep behaving exactly as it did.
func TestExportData_StillWorksWithoutFieldColumns(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	admin := adminFor(t, app, org)

	testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15559800002"), testutil.WithProfileName("Eli"))

	req := testutil.NewJSONRequest(t, map[string]any{
		"table":   "contacts",
		"columns": []string{"phone_number", "profile_name"},
	})
	testutil.SetAuthContext(req, org.ID, admin.ID)

	require.NoError(t, app.ExportData(req))
	require.Equal(t, fasthttp.StatusOK, testutil.GetResponseStatusCode(req))

	body := string(testutil.GetResponseBody(req))
	assert.Contains(t, body, "Phone Number,Name")
	assert.Contains(t, body, "Eli")
}
