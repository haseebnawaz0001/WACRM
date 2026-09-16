// Package templating renders the `{{variable}}`, `{{if}}` and `{{for}}` syntax
// used across the product.
//
// It lived inside the handlers package, which meant the automation engine and
// the shared action library could not use it without an import cycle — and a
// second implementation of the same syntax would have been two subtly
// different template languages in one product.
package templating

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Template syntax patterns
var (
	// {{for item in items}}...{{endfor}}
	forLoopPattern = regexp.MustCompile(`\{\{for\s+(\w+)\s+in\s+(\w+(?:\.\w+)*)\}\}([\s\S]*?)\{\{endfor\}\}`)

	// {{if condition}}...{{else}}...{{endif}} or {{if condition}}...{{endif}}
	ifElsePattern = regexp.MustCompile(`\{\{if\s+([^}]+)\}\}([\s\S]*?)\{\{endif\}\}`)

	// {{variable}} / {{ variable }} / {{object.nested.path}} / {{array[0].field}}.
	// Whitespace inside the braces is tolerated to match the rest of the
	// industry (`{{ name }}` is the common form authors expect).
	variablePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*|\[\d+\])*)\s*\}\}`)

	// Condition parsing: variable, variable == 'value', variable > 100, etc.
	conditionPattern = regexp.MustCompile(`^(\w+(?:\.\w+)*)\s*(==|!=|>=|<=|>|<)?\s*(.*)$`)
)

const maxLoopIterations = 50

// Process processes a template string with variables, conditionals, and loops
func Process(template string, data map[string]any) string {
	if data == nil {
		data = make(map[string]any)
	}

	result := template

	// 1. Process for loops first (they may contain if blocks and variables)
	result = ProcessForLoops(result, data)

	// 2. Process if/else conditionals
	result = ProcessConditionals(result, data)

	// 3. Process remaining variable replacements
	result = ProcessVariables(result, data)

	return result
}

// ProcessForLoops handles {{for item in items}}...{{endfor}} blocks
func ProcessForLoops(template string, data map[string]any) string {
	result := template

	for {
		match := forLoopPattern.FindStringSubmatchIndex(result)
		if match == nil {
			break
		}

		// Extract loop parts
		fullMatch := result[match[0]:match[1]]
		itemVar := result[match[2]:match[3]]
		arrayPath := result[match[4]:match[5]]
		loopBody := result[match[6]:match[7]]

		// Get the array from data
		arrayValue := NestedValue(data, arrayPath)

		var output strings.Builder

		// Process each item in the array
		switch arr := arrayValue.(type) {
		case []any:
			iterations := len(arr)
			if iterations > maxLoopIterations {
				iterations = maxLoopIterations
			}
			for i := 0; i < iterations; i++ {
				// Create a new data context with the loop variable
				loopData := CopyMap(data)
				loopData[itemVar] = arr[i]
				loopData[itemVar+"_index"] = i

				// Process the loop body with the loop context
				processedBody := ProcessConditionals(loopBody, loopData)
				processedBody = ProcessVariables(processedBody, loopData)
				output.WriteString(processedBody)
			}

		case []map[string]any:
			iterations := len(arr)
			if iterations > maxLoopIterations {
				iterations = maxLoopIterations
			}
			for i := 0; i < iterations; i++ {
				loopData := CopyMap(data)
				loopData[itemVar] = arr[i]
				loopData[itemVar+"_index"] = i

				processedBody := ProcessConditionals(loopBody, loopData)
				processedBody = ProcessVariables(processedBody, loopData)
				output.WriteString(processedBody)
			}
		}

		// Replace the for block with the output
		result = result[:match[0]] + output.String() + result[match[1]:]

		// If no output was generated (empty or non-array), the block is just removed
		_ = fullMatch // used for debugging
	}

	return result
}

// ProcessConditionals handles {{if condition}}...{{else}}...{{endif}} blocks
func ProcessConditionals(template string, data map[string]any) string {
	result := template

	for {
		match := ifElsePattern.FindStringSubmatchIndex(result)
		if match == nil {
			break
		}

		// Extract condition and body
		condition := strings.TrimSpace(result[match[2]:match[3]])
		body := result[match[4]:match[5]]

		// Split body into if-part and else-part
		ifPart := body
		elsePart := ""

		elseIndex := strings.Index(body, "{{else}}")
		if elseIndex != -1 {
			ifPart = body[:elseIndex]
			elsePart = body[elseIndex+8:] // len("{{else}}") == 8
		}

		// Evaluate condition
		var output string
		if EvaluateCondition(condition, data) {
			output = ifPart
		} else {
			output = elsePart
		}

		// Replace the if block with the output
		result = result[:match[0]] + output + result[match[1]:]
	}

	return result
}

// ProcessVariables replaces {{variable}} and {{object.path}} with values
func ProcessVariables(template string, data map[string]any) string {
	return variablePattern.ReplaceAllStringFunc(template, func(match string) string {
		// Remove {{ and }}
		path := match[2 : len(match)-2]

		value := NestedValue(data, path)
		return FormatValue(value)
	})
}

// NestedValue extracts a value from nested maps/arrays using dot notation
// Supports: "name", "user.profile.name", "items[0].name", "data.items[2].value"
func NestedValue(data map[string]any, path string) any {
	if data == nil || path == "" {
		return nil
	}

	parts := SplitPath(path)
	var current any = data

	for _, part := range parts {
		if current == nil {
			return nil
		}

		// Check for array index: field[0]
		if idx := strings.Index(part, "["); idx != -1 {
			field := part[:idx]
			indexStr := part[idx+1 : len(part)-1]
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				return nil
			}

			// Get the field first
			if field != "" {
				switch v := current.(type) {
				case map[string]any:
					current = v[field]
				default:
					return nil
				}
			}

			// Then index into the array
			switch arr := current.(type) {
			case []any:
				if index >= 0 && index < len(arr) {
					current = arr[index]
				} else {
					return nil
				}
			case []map[string]any:
				if index >= 0 && index < len(arr) {
					current = arr[index]
				} else {
					return nil
				}
			default:
				return nil
			}
		} else {
			// Regular field access
			switch v := current.(type) {
			case map[string]any:
				current = v[part]
			default:
				return nil
			}
		}
	}

	return current
}

// SplitPath splits a path like "user.profile.name" or "items[0].name" into parts
func SplitPath(path string) []string {
	var parts []string
	var current strings.Builder

	for i := 0; i < len(path); i++ {
		ch := path[i]

		switch ch {
		case '.':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		case '[':
			// Include [index] with the current field name
			if current.Len() > 0 {
				current.WriteByte(ch)
				// Read until ]
				for i++; i < len(path) && path[i] != ']'; i++ {
					current.WriteByte(path[i])
				}
				if i < len(path) {
					current.WriteByte(']')
				}
			}
		default:
			current.WriteByte(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// EvaluateCondition evaluates a condition string against data
// Supports: "variable" (truthy), "variable == 'value'", "variable != 'value'",
// "variable > 100", "variable < 100", "variable >= 100", "variable <= 100"
func EvaluateCondition(condition string, data map[string]any) bool {
	condition = strings.TrimSpace(condition)

	// Parse the condition
	matches := conditionPattern.FindStringSubmatch(condition)
	if matches == nil {
		return false
	}

	varPath := matches[1]
	operator := matches[2]
	compareValue := strings.TrimSpace(matches[3])

	// Get the variable value
	value := NestedValue(data, varPath)

	// Simple truthy check if no operator
	if operator == "" {
		return IsTruthy(value)
	}

	// Remove quotes from compare value if present
	if len(compareValue) >= 2 {
		if (compareValue[0] == '\'' && compareValue[len(compareValue)-1] == '\'') ||
			(compareValue[0] == '"' && compareValue[len(compareValue)-1] == '"') {
			compareValue = compareValue[1 : len(compareValue)-1]
		}
	}

	// Perform comparison
	switch operator {
	case "==":
		return CompareEqual(value, compareValue)
	case "!=":
		return !CompareEqual(value, compareValue)
	case ">":
		return CompareNumeric(value, compareValue) > 0
	case "<":
		return CompareNumeric(value, compareValue) < 0
	case ">=":
		return CompareNumeric(value, compareValue) >= 0
	case "<=":
		return CompareNumeric(value, compareValue) <= 0
	}

	return false
}

// IsTruthy checks if a value is "truthy"
func IsTruthy(value any) bool {
	if value == nil {
		return false
	}

	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false" && v != "0"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case []map[string]any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}

	return true
}

// CompareEqual compares a value with a string for equality
func CompareEqual(value any, compareValue string) bool {
	if value == nil {
		return compareValue == "" || compareValue == "null" || compareValue == "nil"
	}

	switch v := value.(type) {
	case string:
		return v == compareValue
	case bool:
		return fmt.Sprintf("%v", v) == compareValue
	case int:
		return fmt.Sprintf("%d", v) == compareValue
	case int64:
		return fmt.Sprintf("%d", v) == compareValue
	case float64:
		// Try integer comparison first
		if float64(int64(v)) == v {
			return fmt.Sprintf("%d", int64(v)) == compareValue
		}
		return fmt.Sprintf("%v", v) == compareValue
	}

	return fmt.Sprintf("%v", value) == compareValue
}

// CompareNumeric compares a value with a string numerically
// Returns: -1 if value < compare, 0 if equal, 1 if value > compare
func CompareNumeric(value any, compareValue string) int {
	// Convert value to float64
	var numValue float64
	switch v := value.(type) {
	case int:
		numValue = float64(v)
	case int64:
		numValue = float64(v)
	case float64:
		numValue = v
	case string:
		var err error
		numValue, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
	default:
		return 0
	}

	// Convert compare value to float64
	numCompare, err := strconv.ParseFloat(compareValue, 64)
	if err != nil {
		return 0
	}

	if numValue < numCompare {
		return -1
	} else if numValue > numCompare {
		return 1
	}
	return 0
}

// FormatValue converts a value to a string for template output
func FormatValue(value any) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		// Format without trailing zeros
		if float64(int64(v)) == v {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// CopyMap creates a shallow copy of a map
func CopyMap(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// ExtractResponseMapping extracts values from API response and maps them to session variables
func ExtractResponseMapping(responseData map[string]any, mapping map[string]string) map[string]any {
	result := make(map[string]any)

	for varName, jsonPath := range mapping {
		value := NestedValue(responseData, jsonPath)
		if value != nil {
			result[varName] = value
		}
	}

	return result
}

// Resolved is the outcome of looking one {{path}} up.
type Resolved struct {
	// Value is the raw value, formatted for substitution.
	Value string
	// Raw is the placeholder exactly as written, e.g. "{{contact.name}}". A
	// caller that leaves unresolvable references visible substitutes this.
	Raw string
	// Found is false when the path does not exist in the data at all, which a
	// caller may want to treat differently from a path that exists and is
	// empty — leaving the placeholder visible rather than blanking it.
	Found bool
}

// ProcessVariablesWith renders {{path}} references, handing each resolution to
// encode before it is substituted (plan 10, S6).
//
// Four places in the product grew their own {{...}} renderer: the chatbot, the
// CRM action library, custom actions and the IVR http_callback node. They
// disagreed about dotted paths, about arrays and — the part that mattered —
// about whether a value was escaped for the document it was being written
// into. This is the one renderer; the differences that are real (how to encode
// for JSON, a URL or a header) are the caller's, expressed through encode.
func ProcessVariablesWith(template string, data map[string]any, encode func(Resolved) string) string {
	if encode == nil {
		return ProcessVariables(template, data)
	}
	return variablePattern.ReplaceAllStringFunc(template, func(match string) string {
		path := strings.TrimSpace(match[2 : len(match)-2])
		value, found := LookupNested(data, path)
		return encode(Resolved{Value: FormatValue(value), Raw: match, Found: found})
	})
}

// LookupNested is NestedValue with a reported miss.
//
// NestedValue returns nil both for "no such path" and for "the value is nil".
// Found distinguishes them: it is false only when the walk breaks — a segment
// before the last one is absent or is not an object, so the path cannot be
// resolved at all. A path that resolves to a missing final key is Found with an
// empty value, because the object it names does exist.
//
// The distinction is not academic: it is what lets a caller blank a field that
// is simply unset while leaving a mistyped path visible.
func LookupNested(data map[string]any, path string) (any, bool) {
	if data == nil || path == "" {
		return nil, false
	}
	if strings.Contains(path, "[") {
		// Array indexing is NestedValue's business.
		v := NestedValue(data, path)
		return v, v != nil
	}

	parts := SplitPath(path)
	var current any = data
	for i, part := range parts {
		container, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current = container[part]
		if i == len(parts)-1 {
			return current, true
		}
	}
	return current, true
}

// Escape modes say how a substituted value must be encoded for the place it is
// written into (plan 10, S6/X9).
//
// A value interpolated into a request is attacker-influenced far more often
// than it looks: a contact's profile name is chosen by whoever is messaging.
// Substituted raw, a name containing a quote closes the JSON string and adds
// fields to the payload, and a name containing CR/LF splits a header.
type EscapeMode int

const (
	// EscapeRaw performs no encoding. Only for plain text destinations.
	EscapeRaw EscapeMode = iota
	// EscapeJSON encodes for the inside of a JSON string literal.
	EscapeJSON
	// EscapeQuery encodes for a URL query component.
	EscapeQuery
	// EscapeHeader strips the characters that would split a header.
	EscapeHeader
)

var headerStripper = strings.NewReplacer("\r", "", "\n", "")

// Encode applies the encoding a mode requires.
func Encode(mode EscapeMode, raw string) string {
	switch mode {
	case EscapeJSON:
		// Marshal produces a quoted JSON string; the template supplies the
		// surrounding quotes, so they are trimmed off.
		b, err := json.Marshal(raw)
		if err != nil {
			return ""
		}
		return string(b[1 : len(b)-1])
	case EscapeQuery:
		return url.QueryEscape(raw)
	case EscapeHeader:
		return headerStripper.Replace(raw)
	}
	return raw
}

// RenderEscaped renders {{path}} references and encodes each value for mode.
// Unresolvable paths are left as written so a mistyped reference is visible.
func RenderEscaped(template string, data map[string]any, mode EscapeMode) string {
	return ProcessVariablesWith(template, data, func(r Resolved) string {
		if !r.Found {
			return r.Raw
		}
		return Encode(mode, r.Value)
	})
}

// RenderEscapedStrings is RenderEscaped for a flat string map, which is the
// shape the IVR and call-transfer hooks carry their variables in.
func RenderEscapedStrings(template string, vars map[string]string, mode EscapeMode) string {
	data := make(map[string]any, len(vars))
	for k, v := range vars {
		data[k] = v
	}
	return RenderEscaped(template, data, mode)
}
