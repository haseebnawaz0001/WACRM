package crmcontext_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmcontext"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templating"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ctx() context.Context { return context.Background() }

func get(t *testing.T, data map[string]any, root, key string) any {
	t.Helper()
	section, ok := data[root].(map[string]any)
	require.True(t, ok, "expected a %q section", root)
	return section[key]
}

// The namespace exists so one spelling works everywhere. The contact's name,
// owner and custom fields are all things templates ask for constantly and had
// to be assembled differently in each screen.
func TestBuild_ContactSection(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	owner := testutil.CreateTestUser(t, db, org.ID)
	contact := testutil.CreateTestContact(t, db, org.ID)
	require.NoError(t, db.Model(contact).Update("assigned_user_id", owner.ID).Error)

	data, err := crmcontext.New(db).Build(ctx(), org.ID, crmcontext.Opts{
		ContactID: contact.ID,
	})
	require.NoError(t, err)

	assert.Equal(t, contact.ProfileName, get(t, data, "contact", "name"))
	assert.Equal(t, contact.PhoneNumber, get(t, data, "contact", "phone_number"))

	ownerSection, ok := data["contact"].(map[string]any)["owner"].(map[string]any)
	require.True(t, ok, "a contact with an owner should expose them")
	assert.Equal(t, owner.FullName, ownerSection["name"])
	assert.Equal(t, owner.Email, ownerSection["email"])

	assert.Equal(t, org.Name, get(t, data, "org", "name"))
}

// contact.fields.<key> is the whole point of plan 01's typed fields, and was
// the one thing no variable picker offered.
func TestBuild_CustomFieldsAreAddressable(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	require.NoError(t, customfields.SeedOrganization(db, org.ID))
	_, err := customfields.New(db).SetValues(db, org.ID, contact.ID, models.FieldEntityContact,
		map[string]any{"company": "Kano Logistics"}, nil)
	require.NoError(t, err)

	data, buildErr := crmcontext.New(db).Build(ctx(), org.ID, crmcontext.Opts{
		ContactID:     contact.ID,
		IncludeFields: true,
	})
	require.NoError(t, buildErr)

	rendered := templating.RenderEscaped("Hello {{contact.fields.company}}", data, templating.EscapeRaw)
	assert.Equal(t, "Hello Kano Logistics", rendered)
}

// Masking is the viewer's rule, not the template's. A template that prints a
// phone number must print the masked one when the viewer may not see it.
func TestBuild_AppliesPhoneMasking(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	data, err := crmcontext.New(db).Build(ctx(), org.ID, crmcontext.Opts{
		ContactID: contact.ID,
		MaskPhone: func(string) string { return "•••" },
	})
	require.NoError(t, err)

	assert.Equal(t, "•••", get(t, data, "contact", "phone_number"))
}

// A flow that stored a variable called "contact" must not be able to rewrite
// who the message is addressed to.
func TestBuild_ExtraCannotShadowTheRecord(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)

	data, err := crmcontext.New(db).Build(ctx(), org.ID, crmcontext.Opts{
		ContactID: contact.ID,
		Extra: map[string]any{
			"contact": map[string]any{"name": "Someone Else"},
			"ticket":  map[string]any{"id": "T-1"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, contact.ProfileName, get(t, data, "contact", "name"),
		"the record wins over whatever the caller passed")
	assert.Equal(t, "T-1", get(t, data, "ticket", "id"),
		"a caller's own root is still merged in")
}

// Thousands of canned responses in the field use {{contact_name}}. Dropping the
// spelling would turn every one of them into a literal in a customer's message.
func TestWithLegacyAliases_KeepsOldSpellingsWorking(t *testing.T) {
	data := map[string]any{
		"contact": map[string]any{"name": "Amara", "phone_number": "+234801"},
		"user":    map[string]any{"name": "Sam"},
	}
	crmcontext.WithLegacyAliases(data)

	assert.Equal(t, "Amara", data["contact_name"])
	assert.Equal(t, "+234801", data["phone_number"])
	assert.Equal(t, "Sam", data["user_name"])
	assert.Equal(t, "Sam", data["agent_name"])

	rendered := templating.RenderEscaped("Hi {{contact_name}} / {{contact.name}}", data, templating.EscapeRaw)
	assert.Equal(t, "Hi Amara / Amara", rendered, "both spellings resolve to the same person")
}

func TestWithLegacyAliases_NeverOverwritesAnExistingKey(t *testing.T) {
	data := map[string]any{
		"contact":      map[string]any{"name": "Amara"},
		"contact_name": "explicitly set",
	}
	crmcontext.WithLegacyAliases(data)
	assert.Equal(t, "explicitly set", data["contact_name"])
}

func TestIsReserved(t *testing.T) {
	assert.True(t, crmcontext.IsReserved("contact"))
	assert.True(t, crmcontext.IsReserved("contact.fields.company"))
	assert.True(t, crmcontext.IsReserved("conversation.status"))
	assert.False(t, crmcontext.IsReserved("order_number"),
		"a flow's own variables are its own business")
	assert.False(t, crmcontext.IsReserved("contacts"),
		"matching a prefix is not matching the root")
}

// A picker offering variables that will not resolve is worse than one offering
// none: the author only finds out after the message has gone.
func TestCatalog_OffersOnlyWhatTheContextHas(t *testing.T) {
	campaign := crmcontext.Catalog(crmcontext.ContextCampaign)
	require.NotEmpty(t, campaign)
	for _, v := range campaign {
		assert.NotEqual(t, "user", v.Group,
			"a campaign has no acting user, so offering user.name would be a lie")
		assert.NotEqual(t, "conversation", v.Group)
	}

	automation := crmcontext.Catalog(crmcontext.ContextAutomation)
	var hasConversation bool
	for _, v := range automation {
		if v.Group == "conversation" {
			hasConversation = true
		}
	}
	assert.True(t, hasConversation, "automation runs against a conversation")

	assert.Nil(t, crmcontext.Catalog("nonsense"),
		"an unknown context offers nothing rather than everything")
}

// A conversation that is resolved, a user who has left, a contact with no
// owner: all ordinary. A template mentioning them renders empty, never fails.
func TestBuild_MissingPiecesAreAbsentNotFatal(t *testing.T) {
	db := testutil.SetupTestDB(t)
	org := testutil.CreateTestOrganization(t, db)
	contact := testutil.CreateTestContact(t, db, org.ID)
	missing := uuid.New()

	data, err := crmcontext.New(db).Build(ctx(), org.ID, crmcontext.Opts{
		ContactID: contact.ID,
		UserID:    &missing,
	})
	require.NoError(t, err)

	_, hasUser := data["user"]
	assert.False(t, hasUser, "a user who is not there is absent, not an error")
	_, hasConversation := data["conversation"]
	assert.False(t, hasConversation)

	rendered := templating.RenderEscaped("Hi {{contact.owner.name}}", data, templating.EscapeRaw)
	assert.Equal(t, "Hi {{contact.owner.name}}", rendered,
		"an unresolvable path stays visible so the author can see their typo")
}

// "Not found" is not the same as "not ours".
//
// A canned response saying "your order {{order_id}} is ready" has a placeholder
// the agent fills in the dialog. Rendering it as empty because the namespace has
// no `order_id` leaves the customer told their order number is blank — and the
// agent's own input with nothing to substitute into.
func TestRenderOwned_LeavesTheAuthorsOwnPlaceholdersAlone(t *testing.T) {
	data := map[string]any{
		"contact": map[string]any{"name": "Amara"},
	}
	crmcontext.WithLegacyAliases(data)

	rendered := crmcontext.RenderOwned(
		"Hi {{contact.name}}, your order {{order_id}} is ready",
		data, templating.EscapeRaw)

	assert.Equal(t, "Hi Amara, your order {{order_id}} is ready", rendered)
}

func TestRenderOwned_ResolvesLegacyAliasesToo(t *testing.T) {
	data := map[string]any{"contact": map[string]any{"name": "Amara"}}
	crmcontext.WithLegacyAliases(data)

	assert.Equal(t, "Hi Amara", crmcontext.RenderOwned("Hi {{contact_name}}", data, templating.EscapeRaw))
}

// A path that is ours but has no value renders empty, because the record does
// exist and simply has nothing in that field — which is different from a
// placeholder we were never asked to fill.
func TestRenderOwned_BlanksAnOwnedPathWithNoValue(t *testing.T) {
	data := map[string]any{"contact": map[string]any{"name": "Amara", "fields": map[string]any{}}}

	assert.Equal(t, "Company: ",
		crmcontext.RenderOwned("Company: {{contact.fields.company}}", data, templating.EscapeRaw))
}

func TestOwns(t *testing.T) {
	assert.True(t, crmcontext.Owns("contact.name"))
	assert.True(t, crmcontext.Owns("contact_name"), "a legacy alias is ours")
	assert.True(t, crmcontext.Owns("org.timezone"))
	assert.False(t, crmcontext.Owns("order_id"))
	assert.False(t, crmcontext.Owns("customer_reference"))
}
