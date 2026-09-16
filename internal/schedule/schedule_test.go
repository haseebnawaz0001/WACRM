package schedule_test

import (
	"testing"
	"time"

	"github.com/shridarpatil/whatomate/internal/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mondayAt builds a Monday instant in UTC.
func mondayAt(hour, minute int) time.Time {
	// 2026-09-14 is a Monday.
	return time.Date(2026, 9, 14, hour, minute, 0, 0, time.UTC)
}

func weekdaySchedule(start, end string) []any {
	return []any{
		map[string]any{"day": "monday", "enabled": true, "start_time": start, "end_time": end},
	}
}

// The chatbot stored the weekday as a number, the flow and IVR nodes as a name.
// One evaluator has to read both or migrating a call site changes behaviour.
func TestParse_AcceptsBothStoredDayFormats(t *testing.T) {
	byName := schedule.Parse([]any{
		map[string]any{"day": "Monday", "enabled": true, "start_time": "09:00", "end_time": "17:00"},
	})
	byNumber := schedule.Parse([]any{
		map[string]any{"day": float64(1), "enabled": true, "start_time": "09:00", "end_time": "17:00"},
	})

	require.Len(t, byName, 1)
	require.Len(t, byNumber, 1)
	assert.Equal(t, time.Monday, byName[0].Weekday)
	assert.Equal(t, time.Monday, byNumber[0].Weekday)
	assert.Equal(t, byName[0], byNumber[0], "both shapes must produce the same entry")
}

func TestIsOpen_WithinHours(t *testing.T) {
	entries := schedule.Parse(weekdaySchedule("09:00", "17:00"))
	assert.True(t, schedule.IsOpen(mondayAt(12, 0), entries, time.UTC))
}

// The boundary rule is inclusive start, exclusive end. The chatbot used to
// treat the end as inclusive, so 17:00 counted as open there but closed in a
// flow — the same customer got different routing depending on the path.
func TestIsOpen_StartInclusiveEndExclusive(t *testing.T) {
	entries := schedule.Parse(weekdaySchedule("09:00", "17:00"))

	assert.True(t, schedule.IsOpen(mondayAt(9, 0), entries, time.UTC),
		"the opening minute is inside business hours")
	assert.False(t, schedule.IsOpen(mondayAt(17, 0), entries, time.UTC),
		"the closing minute is outside business hours")
	assert.True(t, schedule.IsOpen(mondayAt(16, 59), entries, time.UTC))
	assert.False(t, schedule.IsOpen(mondayAt(8, 59), entries, time.UTC))
}

func TestIsOpen_DisabledDayIsClosed(t *testing.T) {
	entries := schedule.Parse([]any{
		map[string]any{"day": "monday", "enabled": false, "start_time": "09:00", "end_time": "17:00"},
	})
	assert.False(t, schedule.IsOpen(mondayAt(12, 0), entries, time.UTC))
}

// Hours are the organisation's, not the server's. A server in UTC evaluating a
// business in Karachi was the concrete failure: 08:00 UTC is 13:00 there.
func TestIsOpen_EvaluatesInTheGivenTimezone(t *testing.T) {
	entries := schedule.Parse(weekdaySchedule("09:00", "17:00"))
	karachi := schedule.Location("Asia/Karachi") // UTC+5

	instant := mondayAt(8, 0) // 13:00 in Karachi, 08:00 UTC

	assert.False(t, schedule.IsOpen(instant, entries, time.UTC),
		"08:00 UTC is before opening when read as UTC")
	assert.True(t, schedule.IsOpen(instant, entries, karachi),
		"the same instant is mid-afternoon in the business's own timezone")
}

// A schedule states when the business is open, so a day it does not mention is
// closed. All three implementations this package replaces behaved that way.
func TestIsOpen_UnlistedDayIsClosed(t *testing.T) {
	entries := schedule.Parse(weekdaySchedule("09:00", "17:00"))
	// 2026-09-15 is a Tuesday; the schedule says nothing about it.
	tuesday := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	assert.False(t, schedule.IsOpen(tuesday, entries, time.UTC))
}

// A day that is switched on but has unreadable hours is closed, which is what
// the flow and IVR nodes already did with an unparseable time.
func TestIsOpen_EnabledDayWithBrokenHoursIsClosed(t *testing.T) {
	entries := schedule.Parse([]any{
		map[string]any{"day": "monday", "enabled": true, "start_time": "not-a-time", "end_time": "17:00"},
	})
	assert.False(t, schedule.IsOpen(mondayAt(12, 0), entries, time.UTC))
}

// One malformed row must not close the business on the days that are fine.
func TestParse_SkipsUnreadableEntries(t *testing.T) {
	entries := schedule.Parse([]any{
		"not an object",
		map[string]any{"day": "nonsense", "enabled": true},
		map[string]any{"day": "monday", "enabled": true, "start_time": "09:00", "end_time": "17:00"},
	})
	require.Len(t, entries, 1)
	assert.Equal(t, time.Monday, entries[0].Weekday)
	assert.True(t, schedule.IsOpen(mondayAt(12, 0), entries, time.UTC),
		"the readable day still works")
}

func TestLocation_FallsBackToUTC(t *testing.T) {
	assert.Equal(t, time.UTC, schedule.Location(""))
	assert.Equal(t, time.UTC, schedule.Location("Not/AZone"),
		"an unknown zone must not silently fall back to the server's own zone")

	loc := schedule.Location("Asia/Karachi")
	require.NotNil(t, loc)
	assert.Equal(t, "Asia/Karachi", loc.String())
}

func TestParse_ClockBounds(t *testing.T) {
	entries := schedule.Parse([]any{
		map[string]any{"day": "monday", "enabled": true, "start_time": "25:00", "end_time": "17:00"},
	})
	require.Len(t, entries, 1)
	assert.False(t, schedule.IsOpen(mondayAt(12, 0), entries, time.UTC),
		"an out-of-range hour is not a usable schedule")
}
