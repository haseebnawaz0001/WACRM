package crmactions

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/templating"
)

// Config readers are exported because rule triggers are stored in the same
// loose JSON shape as action configuration, and the matcher reads them too.
//
// Config readers. Rule configuration arrives as decoded JSON, so every number
// is a float64 and every missing key is nil. Reading it through these keeps
// that one awkwardness in one place instead of at every use.

func (c Config) Str(key string) string {
	s, _ := c[key].(string)
	return strings.TrimSpace(s)
}

func (c Config) Boolean(key string) bool {
	b, _ := c[key].(bool)
	return b
}

func (c Config) Number(key string) (float64, bool) {
	switch v := c[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func (c Config) Integer(key string) int {
	f, _ := c.Number(key)
	return int(f)
}

func (c Config) Strings(key string) []string {
	raw, ok := c[key].([]any)
	if !ok {
		if typed, isTyped := c[key].([]string); isTyped {
			return typed
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, isString := item.(string); isString && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func (c Config) UUID(key string) (uuid.UUID, bool) {
	parsed, err := uuid.Parse(c.Str(key))
	if err != nil {
		return uuid.Nil, false
	}
	return parsed, true
}

func (c Config) Object(key string) Config {
	switch v := c[key].(type) {
	case map[string]any:
		return Config(v)
	case Config:
		return v
	}
	return Config{}
}

// stringMap reads a flat {"k":"v"} object, used for webhook headers.
func (c Config) StringMap(key string) map[string]string {
	raw, ok := c[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, isString := v.(string); isString {
			out[k] = s
		}
	}
	return out
}

// Duration reads a {amount, unit} pair. Durations are expressed in the units
// people actually think in — "3 days", not 259200 seconds. It is exported
// because rule triggers store the same shape.
func (c Config) Duration(key string) (time.Duration, bool) {
	spec := c.Object(key)
	amount, ok := spec.Number("amount")
	if !ok || amount <= 0 {
		return 0, false
	}
	switch spec.Str("unit") {
	case "minutes":
		return time.Duration(amount) * time.Minute, true
	case "hours":
		return time.Duration(amount) * time.Hour, true
	case "days":
		return time.Duration(amount) * 24 * time.Hour, true
	}
	return 0, false
}

// render fills template variables into a configured string.
func render(value string, vars map[string]any) string {
	if !strings.Contains(value, "{{") {
		return value
	}
	return templating.Process(value, vars)
}

// requireText validates that a templated text field is present.
func requireText(cfg Config, key string) error {
	if cfg.Str(key) == "" {
		return fmt.Errorf("crmactions: %q is required", key)
	}
	return nil
}
