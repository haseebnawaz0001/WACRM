package handlers

// The template engine lives in internal/templating so the automation engine
// and the shared action library can render the same syntax (plan 08). These
// aliases keep the existing call sites reading the way they always have.

import "github.com/shridarpatil/whatomate/internal/templating"

func processTemplate(template string, data map[string]any) string {
	return templating.Process(template, data)
}

func processForLoops(template string, data map[string]any) string {
	return templating.ProcessForLoops(template, data)
}

func processConditionals(template string, data map[string]any) string {
	return templating.ProcessConditionals(template, data)
}

func processVariables(template string, data map[string]any) string {
	return templating.ProcessVariables(template, data)
}

func getNestedValue(data map[string]any, path string) any {
	return templating.NestedValue(data, path)
}

func splitPath(path string) []string { return templating.SplitPath(path) }

func evaluateCondition(condition string, data map[string]any) bool {
	return templating.EvaluateCondition(condition, data)
}

func isTruthy(value any) bool { return templating.IsTruthy(value) }

func compareEqual(value any, compareValue string) bool {
	return templating.CompareEqual(value, compareValue)
}

func compareNumeric(value any, compareValue string) int {
	return templating.CompareNumeric(value, compareValue)
}

func formatValue(value any) string { return templating.FormatValue(value) }

func copyMap(m map[string]any) map[string]any { return templating.CopyMap(m) }

func extractResponseMapping(responseData map[string]any, mapping map[string]string) map[string]any {
	return templating.ExtractResponseMapping(responseData, mapping)
}
