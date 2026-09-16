package handlers

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A keyword rule is the cheapest automation in the product — "when someone
// says REFUND". Until plan 10's S7 it could only answer, never act.
func TestKeywordRuleActions_ReadFromStoredShape(t *testing.T) {
	rule := models.KeywordRule{
		Actions: models.JSONB{"list": []any{
			map[string]any{"type": "add_tags", "config": map[string]any{"tags": []any{"Refund"}}},
			map[string]any{"type": "create_task", "config": map[string]any{"title": "Handle refund"}},
		}},
	}

	specs := keywordRuleActions(rule)
	require.Len(t, specs, 2)
	assert.Equal(t, "add_tags", specs[0].Type)
	assert.Equal(t, "create_task", specs[1].Type)
	assert.Equal(t, "Handle refund", specs[1].Config.Str("title"))
}

func TestKeywordRuleActions_EmptyShapes(t *testing.T) {
	assert.Nil(t, keywordRuleActions(models.KeywordRule{}))
	assert.Nil(t, keywordRuleActions(models.KeywordRule{Actions: models.JSONB{}}))
	assert.Nil(t, keywordRuleActions(models.KeywordRule{Actions: models.JSONB{"list": []any{}}}))
}

// Round-tripping through the storage wrapper is what the API does on save and
// the processor does on match; if they disagree the actions silently vanish.
func TestKeywordActionsJSONBRoundTrip(t *testing.T) {
	stored := keywordActionsJSONB([]map[string]any{
		{"type": "add_tags", "config": map[string]any{"tags": []any{"VIP"}}},
	})
	specs := keywordRuleActions(models.KeywordRule{Actions: stored})
	require.Len(t, specs, 1)
	assert.Equal(t, "add_tags", specs[0].Type)

	assert.Empty(t, keywordActionsJSONB(nil))
}

// Validation happens when the rule is saved, not when a customer triggers it:
// finding out an action was misconfigured at the moment it was needed is the
// failure mode the library's Validate exists to prevent.
func TestValidateKeywordActions(t *testing.T) {
	require.NoError(t, validateKeywordActions(nil))
	require.NoError(t, validateKeywordActions([]map[string]any{
		{"type": "add_tags", "config": map[string]any{"tags": []any{"VIP"}}},
	}))

	err := validateKeywordActions([]map[string]any{{"config": map[string]any{}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no type")

	err = validateKeywordActions([]map[string]any{{"type": "not_a_real_action"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not_a_real_action")

	// A known action with a configuration it cannot run.
	err = validateKeywordActions([]map[string]any{{"type": "create_task", "config": map[string]any{}}})
	require.Error(t, err, "create_task needs a title")
}
