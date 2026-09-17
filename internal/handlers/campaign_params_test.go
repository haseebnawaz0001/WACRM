package handlers_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/handlers"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bodyTemplate(body string) *models.Template {
	return &models.Template{Name: "offer", Status: "APPROVED", BodyContent: body}
}

// A campaign renders for thousands of contacts at once. A source that cannot
// resolve would produce empty values the recipients find first.
func TestValidParamSource(t *testing.T) {
	assert.True(t, handlers.ValidParamSource("static"))
	assert.True(t, handlers.ValidParamSource("contact.name"))
	assert.True(t, handlers.ValidParamSource("contact.phone_number"))
	assert.True(t, handlers.ValidParamSource("contact.fields.company"))

	assert.False(t, handlers.ValidParamSource("contact.fields."),
		"a field prefix with no key names nothing")
	assert.False(t, handlers.ValidParamSource("contact.nickname"))
	assert.False(t, handlers.ValidParamSource(""))
}

// "Invalid mapping" sends an author back to compare the template against their
// form by hand. Naming the parameters does not.
func TestValidateParamMappings_NamesWhatIsWrong(t *testing.T) {
	template := bodyTemplate("Hi {{customer_name}}, your order {{order_id}} is ready")

	missing, bad := handlers.ValidateParamMappings(template, map[string]handlers.ParamMapping{
		"customer_name": {Source: "contact.name"},
	})
	assert.Equal(t, []string{"order_id"}, missing)
	assert.Empty(t, bad)

	missing, bad = handlers.ValidateParamMappings(template, map[string]handlers.ParamMapping{
		"customer_name": {Source: "contact.name"},
		"order_id":      {Source: "contact.nickname"},
	})
	assert.Empty(t, missing)
	require.Len(t, bad, 1)
	assert.Contains(t, bad[0], "order_id")
}

// Positional templates key their parameters "1", "2"; named ones use words.
// Plan 05 originally specified numeric-only keys, which would not have matched
// a named template.
func TestValidateParamMappings_HandlesBothKeyStyles(t *testing.T) {
	positional := bodyTemplate("Hi {{1}}, order {{2}}")
	missing, _ := handlers.ValidateParamMappings(positional, map[string]handlers.ParamMapping{
		"1": {Source: "contact.name"},
		"2": {Source: "contact.fields.order_id"},
	})
	assert.Empty(t, missing)

	named := bodyTemplate("Hi {{customer_name}}")
	missing, _ = handlers.ValidateParamMappings(named, map[string]handlers.ParamMapping{
		"1": {Source: "contact.name"},
	})
	assert.Equal(t, []string{"customer_name"}, missing,
		"a numeric key does not satisfy a named parameter")
}

// Meta rejects a send with an empty parameter, so a contact missing a company
// name would fail rather than send. A fallback keeps the message going out.
func TestResolveParamsForContacts_UsesTheFallback(t *testing.T) {
	app := newTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	contact := testutil.CreateTestContactWith(t, app.DB, org.ID,
		testutil.WithPhoneNumber("15556660001"))

	params, err := app.ResolveParamsForContactsForTest(org.ID,
		[]models.Contact{*contact},
		map[string]handlers.ParamMapping{
			"company": {Source: "contact.fields.company", Fallback: "your team"},
			"name":    {Source: "contact.name"},
			"greet":   {Source: "static", Value: "Hello"},
		})
	require.NoError(t, err)

	got := params[contact.ID]
	assert.Equal(t, "your team", got["company"],
		"a contact with no company still gets a sendable message")
	assert.Equal(t, contact.ProfileName, got["name"])
	assert.Equal(t, "Hello", got["greet"])
}
