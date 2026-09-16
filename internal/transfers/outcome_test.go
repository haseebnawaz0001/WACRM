package transfers_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/transfers"
	"github.com/stretchr/testify/assert"
)

// A caller needs to tell "it worked" from "it was declined for a reason".
// Reporting success for a transfer that was suppressed out of hours is exactly
// the bug the typed outcome exists to stop.
func TestOutcome_HandedDistinguishesSuccessFromARefusal(t *testing.T) {
	handed := []transfers.Outcome{transfers.Assigned, transfers.Queued}
	for _, outcome := range handed {
		assert.True(t, outcome.Handed(), "%s means a person will see it", outcome)
	}

	notHanded := []transfers.Outcome{
		transfers.SuppressedOutOfHours,
		transfers.AlreadyActive,
		transfers.Failed,
	}
	for _, outcome := range notHanded {
		assert.False(t, outcome.Handed(), "%s did not hand the conversation over", outcome)
	}
}

// The strings are stored in automation run records, so renaming one would make
// old runs unreadable.
func TestOutcome_StringsAreStable(t *testing.T) {
	assert.Equal(t, "assigned", transfers.Assigned.String())
	assert.Equal(t, "queued", transfers.Queued.String())
	assert.Equal(t, "suppressed_out_of_hours", transfers.SuppressedOutOfHours.String())
	assert.Equal(t, "already_active", transfers.AlreadyActive.String())
	assert.Equal(t, "failed", transfers.Failed.String())
}
