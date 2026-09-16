package handlers

import (
	"encoding/json"

	"github.com/shridarpatil/whatomate/internal/templating"
	"testing"

	"github.com/stretchr/testify/require"
)

// hostileContext is the kind of contact a real inbox collects: the profile
// name is chosen by whoever is messaging, so it is attacker-controlled text.
func hostileContext() map[string]any {
	return map[string]any{
		"contact": map[string]any{
			"profile_name": `Bob" , "is_admin": true, "x": "`,
			"phone_number": "1555000111",
			"note":         "line one\r\nX-Injected: yes",
			"query":        "a&b=c d",
		},
	}
}

// TestReplaceVariablesJSONStaysValid is the core of X9: a hostile name must not
// be able to close the JSON string and add fields to the payload.
func TestReplaceVariablesJSONStaysValid(t *testing.T) {
	tpl := `{"name":"{{contact.profile_name}}","phone":"{{contact.phone_number}}"}`
	got := replaceVariables(tpl, hostileContext(), templating.EscapeJSON)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &parsed),
		"interpolated body must still be valid JSON, got: %s", got)

	// The payload has exactly the two fields the template declared: the
	// injected "is_admin" must have landed inside the name string instead.
	require.Len(t, parsed, 2)
	require.NotContains(t, parsed, "is_admin")
	require.Equal(t, `Bob" , "is_admin": true, "x": "`, parsed["name"])
	require.Equal(t, "1555000111", parsed["phone"])
}

func TestReplaceVariablesHeaderCannotSplit(t *testing.T) {
	got := replaceVariables("{{contact.note}}", hostileContext(), templating.EscapeHeader)
	require.NotContains(t, got, "\r")
	require.NotContains(t, got, "\n")
}

func TestReplaceVariablesQueryIsEncoded(t *testing.T) {
	got := replaceVariables("https://x.example/s?q={{contact.query}}", hostileContext(), templating.EscapeQuery)
	require.Equal(t, "https://x.example/s?q=a%26b%3Dc+d", got,
		"a value must not be able to add query parameters")
}

// The admin-authored parts of a template are left alone; only substituted
// values are encoded.
func TestReplaceVariablesLeavesTemplateStructureIntact(t *testing.T) {
	got := replaceVariables("https://x.example/a/b?fixed=1&q={{contact.phone_number}}",
		hostileContext(), templating.EscapeQuery)
	require.Equal(t, "https://x.example/a/b?fixed=1&q=1555000111", got)
}

func TestReplaceVariablesUnknownPathUntouched(t *testing.T) {
	got := replaceVariables("{{contact.nope}}", hostileContext(), templating.EscapeJSON)
	require.Equal(t, "", got)

	got = replaceVariables("{{nothing.here}}", hostileContext(), templating.EscapeJSON)
	require.Equal(t, "{{nothing.here}}", got, "an unresolvable path is left as written")
}
