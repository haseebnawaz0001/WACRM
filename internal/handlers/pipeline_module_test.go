package handlers_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/handlers"

	"github.com/stretchr/testify/assert"
)

// Plan 07: the pipeline module can be switched off per organization.
//
// The gate is by path so a board endpoint added later is covered by the same
// switch, rather than each handler having to remember to ask.
func TestIsPipelinePath(t *testing.T) {
	inModule := []string{
		"/api/pipelines",
		"/api/pipelines/abc",
		"/api/pipelines/abc/board",
		"/api/pipelines/abc/stages",
		"/api/pipeline-stages/abc",
		"/api/deals",
		"/api/deals/abc/move",
		"/api/reports/pipeline-funnel",
		"/api/reports/pipeline-forecast",
		"/api/contacts/abc/deals",
	}
	for _, p := range inModule {
		assert.True(t, handlers.IsPipelinePath(p), "%s belongs to the pipeline module", p)
	}

	outside := []string{
		"/api/contacts",
		"/api/contacts/abc",
		"/api/contacts/abc/timeline",
		"/api/tasks",
		"/api/segments",
		"/api/reports/tasks-by-agent",
		"/api/reports/lifecycle-funnel",
		"/api/inbox",
		"/health",
	}
	for _, p := range outside {
		assert.False(t, handlers.IsPipelinePath(p), "%s must not be gated by the pipeline module", p)
	}
}

// Absent means enabled: a module has to be switched off deliberately, or every
// organization created before the setting existed would lose its board.
func TestPipelinesEnabledDefaultsToOn(t *testing.T) {
	app := newTestApp(t)

	// A random org id has no cached settings at all.
	assert.True(t, app.PipelinesEnabled(uuid.New()))
}
