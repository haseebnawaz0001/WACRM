// Package contactquery turns a structured filter into parameterised SQL over
// contacts (plan 00, F6).
//
// The contacts list could only filter by a search string and a tag OR-list.
// Everything the CRM needs — segments, automation conditions, campaign
// audiences, reports — asks the same question in different words: "which
// contacts match these conditions?" Answering it once, here, is what stops
// five features each growing their own half-complete query builder.
//
// Two rules make this safe to expose over the API: column names never come from
// the request (only from the field registry), and every value is a bound
// parameter. A filter is data, not SQL.
package contactquery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Boolean group operators.
const (
	OpAnd = "and"
	OpOr  = "or"
)

// Limits on a filter. These are not arbitrary: a filter arrives from the
// network and compiles into SQL, so its size has to be bounded before it is
// planned, not after.
const (
	// MaxDepth caps how deeply groups may nest.
	MaxDepth = 3
	// MaxRules caps the total number of leaf rules in one filter.
	MaxRules = 50
	// MaxStringLen caps a single text value.
	MaxStringLen = 500
	// MaxListLen caps how many entries a list value may hold.
	MaxListLen = 200
)

// Node is one entry in a filter: either a boolean group (Op + Rules) or a leaf
// condition (Field + Operator + Value).
type Node struct {
	// Group form.
	Op    string `json:"op,omitempty"`
	Rules []Node `json:"rules,omitempty"`

	// Leaf form.
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// Filter is the root of a filter tree.
type Filter = Node

// IsGroup reports whether the node is a boolean group.
func (n Node) IsGroup() bool {
	return n.Op != "" || len(n.Rules) > 0
}

// IsEmpty reports whether the node selects everything.
func (n Node) IsEmpty() bool {
	return n.Field == "" && len(n.Rules) == 0
}

// ValidationError explains why a filter was rejected. The path locates the
// offending rule so a builder UI can point at it rather than saying "invalid".
type ValidationError struct {
	Path    string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Path == "" {
		return "contactquery: " + e.Message
	}
	return fmt.Sprintf("contactquery: %s: %s", e.Path, e.Message)
}

func invalid(path, format string, args ...any) *ValidationError {
	return &ValidationError{Path: path, Message: fmt.Sprintf(format, args...)}
}

// Validate checks a filter against the registry and the structural limits.
//
// It runs before anything is compiled, so a filter that is too large or names
// an unknown field never reaches the planner.
func Validate(r *Registry, f Filter) error {
	if f.IsEmpty() {
		return nil
	}
	count := 0
	return validateNode(r, f, "filter", 1, &count)
}

func validateNode(r *Registry, n Node, path string, depth int, count *int) error {
	if n.IsGroup() {
		if depth > MaxDepth {
			return invalid(path, "filter nests deeper than %d levels", MaxDepth)
		}
		switch strings.ToLower(n.Op) {
		case OpAnd, OpOr, "":
			// "" is treated as AND.
		default:
			return invalid(path, "unknown group operator %q", n.Op)
		}
		for i, child := range n.Rules {
			childPath := fmt.Sprintf("%s.rules[%d]", path, i)
			if err := validateNode(r, child, childPath, depth+1, count); err != nil {
				return err
			}
		}
		return nil
	}

	*count++
	if *count > MaxRules {
		return invalid(path, "filter has more than %d rules", MaxRules)
	}

	field, ok := r.Lookup(n.Field)
	if !ok {
		return invalid(path, "unknown field %q", n.Field)
	}
	if !field.Supports(n.Operator) {
		return invalid(path, "field %q does not support operator %q", n.Field, n.Operator)
	}
	return validateValue(path, field.Type, n.Operator, n.Value)
}

// validateValue checks the shape and size of a rule's value.
func validateValue(path string, t FieldType, operator string, value any) error {
	if !operatorNeedsValue(operator) {
		return nil
	}
	if value == nil {
		return invalid(path, "operator %q needs a value", operator)
	}

	// A segment reference is a single id, not a list: "in segment X" names one
	// segment, and the compiler inlines that segment's own filter.
	if t == TypeSegment {
		s, ok := value.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return invalid(path, "a segment rule needs a segment id")
		}
		return nil
	}

	switch operator {
	case OpIn, OpNotIn, OpContainsAny, OpContainsAll, OpContainsNone:
		list, ok := toList(value)
		if !ok {
			return invalid(path, "operator %q needs a list value", operator)
		}
		if len(list) == 0 {
			return invalid(path, "operator %q needs at least one value", operator)
		}
		if len(list) > MaxListLen {
			return invalid(path, "list has more than %d values", MaxListLen)
		}
		for _, item := range list {
			if s, isStr := item.(string); isStr && len(s) > MaxStringLen {
				return invalid(path, "value is longer than %d characters", MaxStringLen)
			}
		}
		return nil

	case OpBetween:
		list, ok := toList(value)
		if !ok || len(list) != 2 {
			return invalid(path, "operator %q needs exactly two values", operator)
		}
		return nil

	case OpWithinLast, OpMoreThanAgo:
		if _, err := parseDuration(value); err != nil {
			return invalid(path, "%s", err.Error())
		}
		return nil
	}

	if s, ok := value.(string); ok && len(s) > MaxStringLen {
		return invalid(path, "value is longer than %d characters", MaxStringLen)
	}

	// Date fields only accept something date-shaped.
	if t == TypeDate {
		if _, err := parseTime(value); err != nil {
			return invalid(path, "%s", err.Error())
		}
	}
	return nil
}

// operatorNeedsValue reports whether an operator takes a value at all.
func operatorNeedsValue(operator string) bool {
	switch operator {
	case OpIsEmpty, OpIsNotEmpty, OpIsTrue, OpIsFalse, OpIsMe, OpIsUnassigned:
		return false
	}
	return true
}

// toList normalises a value into a slice. JSON decoding yields []any, but a
// caller building a filter in Go may pass []string.
func toList(value any) ([]any, bool) {
	switch v := value.(type) {
	case []any:
		return v, true
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// ParseFilter decodes a filter from JSON.
func ParseFilter(raw []byte) (Filter, error) {
	if len(raw) == 0 {
		return Filter{}, nil
	}
	var f Filter
	if err := json.Unmarshal(raw, &f); err != nil {
		return Filter{}, fmt.Errorf("contactquery: invalid filter JSON: %w", err)
	}
	return f, nil
}
