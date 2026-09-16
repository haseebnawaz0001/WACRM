package contactquery

import (
	"fmt"
	"time"
)

// parseTime reads a date value from a filter.
//
// Filters arrive as JSON, so a date is a string; a caller building one in Go
// may pass a time.Time directly. Both full timestamps and plain dates are
// accepted because a builder UI produces the latter.
func parseTime(v any) (time.Time, error) {
	switch value := v.(type) {
	case time.Time:
		return value, nil
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
			if t, err := time.Parse(layout, value); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("%q is not a date", value)
	}
	return time.Time{}, fmt.Errorf("expected a date, got %T", v)
}

// Duration units accepted by within_last / more_than_ago.
const (
	UnitMinutes = "minutes"
	UnitHours   = "hours"
	UnitDays    = "days"
	UnitWeeks   = "weeks"
	UnitMonths  = "months"
)

// parseDuration reads {"amount": 30, "unit": "days"}.
//
// Months are approximated as 30 days. A filter like "no reply in more than 3
// months" is a rough bucket, not a calendar calculation, and pretending
// otherwise would make the same filter return different counts depending on
// which months it spanned.
func parseDuration(v any) (time.Duration, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("expected {amount, unit}, got %T", v)
	}

	amount, err := asNumber(m["amount"])
	if err != nil {
		return 0, fmt.Errorf("duration amount: %w", err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("duration amount must be positive")
	}

	unit, _ := m["unit"].(string)
	switch unit {
	case UnitMinutes:
		return time.Duration(amount) * time.Minute, nil
	case UnitHours:
		return time.Duration(amount) * time.Hour, nil
	case UnitDays:
		return time.Duration(amount) * 24 * time.Hour, nil
	case UnitWeeks:
		return time.Duration(amount) * 7 * 24 * time.Hour, nil
	case UnitMonths:
		return time.Duration(amount) * 30 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("unknown duration unit %q", unit)
}

func asNumber(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case int64:
		return float64(n), nil
	}
	return 0, fmt.Errorf("expected a number, got %T", v)
}
