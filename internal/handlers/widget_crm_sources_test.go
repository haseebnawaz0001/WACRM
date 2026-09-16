package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Plan 09: the widget editor offers the CRM data sources and the funnel and
// leaderboard display types.
//
// Before this the dashboard could only report on what the product did before
// the CRM existed — a manager could pin "messages this week" but not "tasks
// overdue" or "deals won".
func TestWidgetDataSourcesIncludeCRM(t *testing.T) {
	for _, source := range []string{"tasks", "deals", "conversations"} {
		fields, ok := widgetDataSources[source]
		assert.True(t, ok, "%s should be offered as a data source", source)
		assert.NotEmpty(t, fields, "%s should offer something to filter on", source)
	}
}

func TestWidgetDisplayTypesIncludeFunnelAndLeaderboard(t *testing.T) {
	has := map[string]bool{}
	for _, d := range widgetDisplayTypes {
		has[d] = true
	}
	assert.True(t, has["funnel"])
	assert.True(t, has["leaderboard"])
	// The originals must survive.
	for _, d := range []string{"number", "percentage", "chart", "table", "shortcuts"} {
		assert.True(t, has[d], "%s should still be offered", d)
	}
}

// Every field the picker offers must be usable for something — filtering or
// grouping. One that is neither is silently dropped at query time, so an
// operator builds a filter in the UI and it quietly does nothing.
//
// Grouping and filtering are separate whitelists on purpose: campaigns can be
// grouped by message_status, which is served from pre-aggregated counters and
// is not a column anything can filter on.
func TestOfferedFieldsAreUsable(t *testing.T) {
	for source, fields := range widgetDataSources {
		allowed := allowedFilterFields[source]
		assert.NotNil(t, allowed, "%s has no filter whitelist", source)
		for _, f := range fields {
			assert.True(t, allowed[f] || allowedGroupByFields[f],
				"%s.%s is offered in the picker but can be neither filtered nor grouped", source, f)
		}
	}
}

// Each CRM source needs a table and a timestamp, or its count is always zero.
func TestCRMWidgetTablesCoverTheCRMSources(t *testing.T) {
	for _, source := range []string{"tasks", "deals", "conversations"} {
		spec, ok := crmWidgetTables[source]
		assert.True(t, ok, "%s has no table mapping", source)
		assert.NotEmpty(t, spec.Table)
		assert.NotEmpty(t, spec.TimeColumn)
	}
}
