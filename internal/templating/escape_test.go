package templating_test

import (
	"encoding/json"
	"testing"

	"github.com/shridarpatil/whatomate/internal/templating"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeModes(t *testing.T) {
	assert.Equal(t, `say \"hi\"`, templating.Encode(templating.EscapeJSON, `say "hi"`))
	assert.Equal(t, `a\\b`, templating.Encode(templating.EscapeJSON, `a\b`))
	assert.Equal(t, "a%26b%3Dc+d", templating.Encode(templating.EscapeQuery, "a&b=c d"))
	assert.Equal(t, "oneX-Injected: yes", templating.Encode(templating.EscapeHeader, "one\r\nX-Injected: yes"))
	assert.Equal(t, `say "hi"`, templating.Encode(templating.EscapeRaw, `say "hi"`))
}

// The reason the escaping exists: a value must not be able to add fields to the
// JSON document it is written into.
func TestRenderEscapedKeepsJSONValid(t *testing.T) {
	data := map[string]any{"name": `Bob", "admin": true, "x": "`}
	got := templating.RenderEscaped(`{"name":"{{name}}"}`, data, templating.EscapeJSON)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &parsed), "got: %s", got)
	assert.Len(t, parsed, 1)
	assert.NotContains(t, parsed, "admin")
}

func TestRenderEscapedStringsFlatMap(t *testing.T) {
	got := templating.RenderEscapedStrings("https://x/a?q={{term}}",
		map[string]string{"term": "a b&c"}, templating.EscapeQuery)
	assert.Equal(t, "https://x/a?q=a+b%26c", got)
}

// Found distinguishes "this object has no such key" from "the path does not
// resolve at all", which is what lets one caller blank a field and another
// leave a mistyped reference visible.
func TestLookupNestedReportsResolvability(t *testing.T) {
	data := map[string]any{
		"contact": map[string]any{"name": "Ada", "nickname": nil},
	}

	v, found := templating.LookupNested(data, "contact.name")
	assert.True(t, found)
	assert.Equal(t, "Ada", v)

	// The object exists; the key inside it is simply unset.
	v, found = templating.LookupNested(data, "contact.missing")
	assert.True(t, found)
	assert.Nil(t, v)

	// Nothing called "nothing" exists, so the path cannot be resolved.
	_, found = templating.LookupNested(data, "nothing.here")
	assert.False(t, found)

	_, found = templating.LookupNested(nil, "a")
	assert.False(t, found)
	_, found = templating.LookupNested(data, "")
	assert.False(t, found)
}

func TestProcessVariablesWithNilEncodeFallsBack(t *testing.T) {
	data := map[string]any{"a": "b"}
	assert.Equal(t, "b", templating.ProcessVariablesWith("{{a}}", data, nil))
}
