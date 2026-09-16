// Package customfields defines org-configurable contact fields and validates
// their values (plan 01).
//
// Contacts carried a free-form metadata JSONB: anything could be written under
// any key, nothing was validated, and nothing could be filtered or sorted
// because the database had no idea what was inside. Typing a field once, here,
// is what lets a value be checked on the way in and queried afterwards.
package customfields

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/phoneutil"
)

// keyPattern is the slug format for a field key. Keys appear in filters,
// templates and URLs, so they are restricted to a shape that needs no escaping
// in any of them.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// emailPattern is a deliberately permissive check. Full RFC 5322 validation
// rejects addresses that work in practice, and the only authoritative test is
// delivery; this catches typos without inventing rules.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s.]+\.[^@\s]+$`)

// ValidationError explains why a value was rejected, naming the field so the
// UI can mark the right input.
type ValidationError struct {
	FieldKey string
	Message  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("customfields: %s: %s", e.FieldKey, e.Message)
}

func reject(key, format string, args ...any) *ValidationError {
	return &ValidationError{FieldKey: key, Message: fmt.Sprintf(format, args...)}
}

// ValidKey reports whether a key is a usable slug.
func ValidKey(key string) bool {
	return keyPattern.MatchString(key)
}

// ValidType reports whether a type is one this package understands.
func ValidType(t string) bool {
	switch t {
	case models.FieldTypeText, models.FieldTypeNumber, models.FieldTypeDate,
		models.FieldTypeDropdown, models.FieldTypeEmail, models.FieldTypePhone:
		return true
	}
	return false
}

// ValidateDefinition checks a field definition before it is saved.
func ValidateDefinition(d *models.CustomFieldDefinition) error {
	if !ValidKey(d.Key) {
		return reject(d.Key, "key must start with a letter and contain only lowercase letters, digits and underscores")
	}
	if strings.TrimSpace(d.Label) == "" {
		return reject(d.Key, "label is required")
	}
	if !ValidType(d.Type) {
		return reject(d.Key, "unknown field type %q", d.Type)
	}
	if d.Type == models.FieldTypeDropdown {
		if len(d.Options) == 0 {
			return reject(d.Key, "a dropdown needs at least one option")
		}
		seen := map[string]bool{}
		for _, raw := range d.Options {
			opt, ok := raw.(map[string]any)
			if !ok {
				return reject(d.Key, "each option must be an object")
			}
			value, _ := opt["value"].(string)
			if strings.TrimSpace(value) == "" {
				return reject(d.Key, "every option needs a value")
			}
			if seen[value] {
				return reject(d.Key, "duplicate option value %q", value)
			}
			seen[value] = true
		}
	}
	return nil
}

// Value is a validated, typed value ready to be stored.
type Value struct {
	Text   *string
	Number *float64
	Date   *time.Time
	Option *string
}

// IsEmpty reports whether the value holds nothing.
func (v Value) IsEmpty() bool {
	return v.Text == nil && v.Number == nil && v.Date == nil && v.Option == nil
}

// Apply writes the value onto a row.
func (v Value) Apply(row *models.CustomFieldValue) {
	row.ValueText = v.Text
	row.ValueNumber = v.Number
	row.ValueDate = v.Date
	row.ValueOption = v.Option
}

// Coerce validates a raw value against a field definition and returns the typed
// form to store.
//
// A nil or blank input is a cleared field, not an error: clearing a value is a
// normal edit, and the only way to express it.
func Coerce(d models.CustomFieldDefinition, raw any) (Value, error) {
	if raw == nil {
		return Value{}, nil
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) == "" {
		return Value{}, nil
	}

	switch d.Type {
	case models.FieldTypeText:
		return coerceText(d, raw)
	case models.FieldTypeEmail:
		return coerceEmail(d, raw)
	case models.FieldTypePhone:
		return coercePhone(d, raw)
	case models.FieldTypeNumber:
		return coerceNumber(d, raw)
	case models.FieldTypeDate:
		return coerceDate(d, raw)
	case models.FieldTypeDropdown:
		return coerceDropdown(d, raw)
	}
	return Value{}, reject(d.Key, "unknown field type %q", d.Type)
}

func coerceText(d models.CustomFieldDefinition, raw any) (Value, error) {
	s := fmt.Sprint(raw)
	maxLen := intRule(d.Validation, "max_length", 10000)
	if len([]rune(s)) > maxLen {
		return Value{}, reject(d.Key, "must be at most %d characters", maxLen)
	}
	return Value{Text: &s}, nil
}

// coerceEmail lower-cases the address so two spellings of the same mailbox
// match each other in filters and duplicate detection.
func coerceEmail(d models.CustomFieldDefinition, raw any) (Value, error) {
	s := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
	if !emailPattern.MatchString(s) {
		return Value{}, reject(d.Key, "%q is not a valid email address", s)
	}
	return Value{Text: &s}, nil
}

// coercePhone stores the normalised digits, so a number typed with spaces or a
// "+" matches the same number stored from a webhook.
func coercePhone(d models.CustomFieldDefinition, raw any) (Value, error) {
	normalized := phoneutil.Normalize(fmt.Sprint(raw))
	if normalized == "" {
		return Value{}, reject(d.Key, "%q is not a valid phone number", raw)
	}
	return Value{Text: &normalized}, nil
}

func coerceNumber(d models.CustomFieldDefinition, raw any) (Value, error) {
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return Value{}, reject(d.Key, "%q is not a number", v)
		}
		n = parsed
	default:
		return Value{}, reject(d.Key, "expected a number, got %T", raw)
	}

	if min, ok := floatRule(d.Validation, "min"); ok && n < min {
		return Value{}, reject(d.Key, "must be at least %v", min)
	}
	if max, ok := floatRule(d.Validation, "max"); ok && n > max {
		return Value{}, reject(d.Key, "must be at most %v", max)
	}
	return Value{Number: &n}, nil
}

func coerceDate(d models.CustomFieldDefinition, raw any) (Value, error) {
	switch v := raw.(type) {
	case time.Time:
		return Value{Date: &v}, nil
	case string:
		for _, layout := range []string{"2006-01-02", time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
				// Stored as a date, so the time of day is dropped rather
				// than silently shifting the day across timezones.
				day := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
				return Value{Date: &day}, nil
			}
		}
		return Value{}, reject(d.Key, "%q is not a date", v)
	}
	return Value{}, reject(d.Key, "expected a date, got %T", raw)
}

func coerceDropdown(d models.CustomFieldDefinition, raw any) (Value, error) {
	s := fmt.Sprint(raw)
	for _, opt := range d.Options {
		m, ok := opt.(map[string]any)
		if !ok {
			continue
		}
		if value, _ := m["value"].(string); value == s {
			return Value{Option: &value}, nil
		}
	}
	return Value{}, reject(d.Key, "%q is not one of the allowed options", s)
}

func intRule(rules models.JSONB, key string, fallback int) int {
	if rules == nil {
		return fallback
	}
	switch v := rules[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return fallback
}

func floatRule(rules models.JSONB, key string) (float64, bool) {
	if rules == nil {
		return 0, false
	}
	switch v := rules[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	}
	return 0, false
}
