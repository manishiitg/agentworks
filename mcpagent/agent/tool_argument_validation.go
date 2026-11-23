package mcpagent

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// validateDirectToolArguments makes the registered JSON schema authoritative
// for every transport. Direct provider calls already see the schema, and
// get_api_spec renders it for HTTP callers; this wrapper closes the remaining
// gap where the executor previously accepted an unchecked argument map.
//
// Tool argument objects are closed by default. A tool that intentionally
// accepts extension fields can opt out with additionalProperties=true (or an
// additionalProperties schema). This matches function-tool contracts: a typo
// is almost always an invocation error, not user data.
func validateDirectToolArguments(
	toolName string,
	schema map[string]interface{},
	next func(context.Context, map[string]interface{}) (string, error),
) func(context.Context, map[string]interface{}) (string, error) {
	if next == nil || len(schema) == 0 {
		return next
	}
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if err := validateToolArgumentObject(toolName, schema, args); err != nil {
			return "", err
		}
		return next(ctx, args)
	}
}

func validateToolArgumentObject(toolName string, schema map[string]interface{}, args map[string]interface{}) error {
	if args == nil {
		args = map[string]interface{}{}
	}

	properties, _ := schema["properties"].(map[string]interface{})
	required := schemaStringList(schema["required"])
	missing := missingToolFields(args, required)

	allowUnknown := false
	if additional, exists := schema["additionalProperties"]; exists {
		switch value := additional.(type) {
		case bool:
			allowUnknown = value
		case map[string]interface{}:
			allowUnknown = true
		}
	}
	unknown := make([]string, 0)
	if !allowUnknown {
		for name := range args {
			if _, exists := properties[name]; !exists {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)
	}

	problems := make([]string, 0, 3)
	if len(missing) > 0 {
		problems = append(problems, "missing required field(s): "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		problems = append(problems, "unknown field(s): "+strings.Join(unknown, ", "))
	}
	if len(missing) == 0 {
		if alternatives := requiredAlternatives(schema["anyOf"]); len(alternatives) > 0 && !matchesRequiredAlternative(args, alternatives) {
			formatted := make([]string, 0, len(alternatives))
			for _, fields := range alternatives {
				formatted = append(formatted, "["+strings.Join(fields, ", ")+"]")
			}
			problems = append(problems, "must provide at least one required field set: "+strings.Join(formatted, " or "))
		}
	}

	for name, value := range args {
		rawProperty, exists := properties[name]
		if !exists {
			continue
		}
		property, ok := rawProperty.(map[string]interface{})
		if !ok {
			continue
		}
		if problem := validateToolArgumentValue(name, value, property); problem != "" {
			problems = append(problems, problem)
		}
	}

	if len(problems) == 0 {
		return nil
	}
	expected := make([]string, 0, len(properties))
	for name := range properties {
		expected = append(expected, name)
	}
	sort.Strings(expected)
	return fmt.Errorf(
		"invalid arguments for tool %q: %s. Expected field(s): %s. Call get_api_spec(tool_name=%q) and use the published names exactly",
		toolName,
		strings.Join(problems, "; "),
		strings.Join(expected, ", "),
		toolName,
	)
}

func missingToolFields(args map[string]interface{}, required []string) []string {
	missing := make([]string, 0)
	for _, name := range required {
		if _, exists := args[name]; !exists {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func requiredAlternatives(raw interface{}) [][]string {
	items, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	result := make([][]string, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if fields := schemaStringList(entry["required"]); len(fields) > 0 {
			result = append(result, fields)
		}
	}
	return result
}

func matchesRequiredAlternative(args map[string]interface{}, alternatives [][]string) bool {
	for _, fields := range alternatives {
		if len(missingToolFields(args, fields)) == 0 {
			return true
		}
	}
	return false
}

func schemaStringList(raw interface{}) []string {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func validateToolArgumentValue(name string, value interface{}, schema map[string]interface{}) string {
	want, _ := schema["type"].(string)
	valid := true
	switch want {
	case "string":
		_, valid = value.(string)
	case "array":
		kind := reflect.TypeOf(value)
		valid = kind != nil && (kind.Kind() == reflect.Slice || kind.Kind() == reflect.Array)
	case "object":
		kind := reflect.TypeOf(value)
		valid = kind != nil && kind.Kind() == reflect.Map && kind.Key().Kind() == reflect.String
	case "boolean":
		_, valid = value.(bool)
	case "number":
		valid = isToolArgumentNumber(value)
	case "integer":
		valid = isToolArgumentInteger(value)
	}
	if !valid {
		return fmt.Sprintf("field %q must be %s", name, want)
	}
	if text, ok := value.(string); ok {
		if minimum, ok := schemaNumber(schema["minLength"]); ok && utf8.RuneCountInString(text) < int(minimum) {
			return fmt.Sprintf("field %q must contain at least %d character(s)", name, int(minimum))
		}
	}
	if rawEnum, ok := schema["enum"].([]interface{}); ok {
		matched := false
		for _, candidate := range rawEnum {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Sprintf("field %q must be one of %v", name, rawEnum)
		}
	}
	return ""
}

func isToolArgumentNumber(value interface{}) bool {
	if value == nil {
		return false
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func isToolArgumentInteger(value interface{}) bool {
	if value == nil {
		return false
	}
	switch reflect.TypeOf(value).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Float32, reflect.Float64:
		number := reflect.ValueOf(value).Convert(reflect.TypeOf(float64(0))).Float()
		return !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number
	default:
		return false
	}
}

func schemaNumber(raw interface{}) (float64, bool) {
	switch value := raw.(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}
