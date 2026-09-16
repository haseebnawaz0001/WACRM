// Package schedule evaluates business hours (plan 10, S11).
//
// Opening hours were decided in four places — chatbot settings, the flow timing
// node, the IVR timing node and the transfer helpers — and no two agreed. The
// chatbot read the day as a number and treated the end time as inclusive; the
// flow and IVR nodes read the day as a name and treated it as exclusive. All of
// them used the server's local clock, so a deployment in a different region
// routed customers by the wrong hours entirely.
//
// This package is the single evaluator. The rule is: a day must be enabled, the
// start time is inclusive, the end time is exclusive, and the comparison
// happens in the organisation's timezone.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Entry is one day's opening hours.
type Entry struct {
	// Weekday is the day this entry covers.
	Weekday time.Weekday
	Enabled bool
	// Start and End are minutes from midnight.
	Start int
	End   int
}

// Parse reads the stored schedule JSON into entries.
//
// Both historical shapes are accepted: "day" as a weekday number (0 = Sunday,
// written by chatbot settings) and as a weekday name (written by the flow and
// IVR timing nodes). Entries that cannot be understood are skipped rather than
// treated as closed, so one malformed row cannot silently shut a business down.
func Parse(raw []any) []Entry {
	entries := make([]Entry, 0, len(raw))

	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		weekday, ok := parseWeekday(m["day"])
		if !ok {
			continue
		}

		enabled, _ := m["enabled"].(bool)

		entry := Entry{Weekday: weekday, Enabled: enabled, Start: -1, End: -1}
		if start, ok := parseClock(m["start_time"]); ok {
			entry.Start = start
		}
		if end, ok := parseClock(m["end_time"]); ok {
			entry.End = end
		}
		entries = append(entries, entry)
	}

	return entries
}

// IsOpen reports whether the instant falls inside the schedule, evaluated in
// loc. A nil location means UTC.
//
// A day the schedule does not mention is closed. All three implementations this
// package replaces agreed on that, so it is the behaviour callers already rely
// on: a schedule is the complete statement of when a business is open, not a
// list of exceptions.
func IsOpen(t time.Time, entries []Entry, loc *time.Location) bool {
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	minutes := local.Hour()*60 + local.Minute()

	for _, e := range entries {
		if e.Weekday != local.Weekday() {
			continue
		}
		if !e.Enabled {
			return false
		}
		if e.Start < 0 || e.End < 0 {
			// The day is enabled but its hours are unreadable. Treating
			// this as closed matches what the flow and IVR nodes did.
			return false
		}
		// Inclusive start, exclusive end: 09:00–17:00 means a message at
		// 17:00 is out of hours, and the next day's 09:00 is in hours.
		return minutes >= e.Start && minutes < e.End
	}

	return false
}

// Location resolves an IANA timezone name, falling back to UTC.
//
// An unknown name returns UTC rather than the server's local zone: the server's
// zone is an accident of where it happens to run, and silently using it is the
// bug this package exists to remove.
func Location(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// parseWeekday accepts a weekday number (0 = Sunday) or a name.
func parseWeekday(v any) (time.Weekday, bool) {
	switch day := v.(type) {
	case float64:
		return weekdayFromInt(int(day))
	case int:
		return weekdayFromInt(day)
	case string:
		trimmed := strings.TrimSpace(day)
		if n, err := strconv.Atoi(trimmed); err == nil {
			return weekdayFromInt(n)
		}
		lower := strings.ToLower(trimmed)
		for d := time.Sunday; d <= time.Saturday; d++ {
			if strings.ToLower(d.String()) == lower {
				return d, true
			}
		}
	}
	return 0, false
}

func weekdayFromInt(n int) (time.Weekday, bool) {
	if n < 0 || n > 6 {
		return 0, false
	}
	return time.Weekday(n), true
}

// parseClock reads "HH:MM" into minutes from midnight.
func parseClock(v any) (int, bool) {
	s, ok := v.(string)
	if !ok {
		return 0, false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}

	var hour, minute int
	if _, err := fmt.Sscanf(s, "%d:%d", &hour, &minute); err != nil {
		return 0, false
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}
