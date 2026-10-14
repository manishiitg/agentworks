package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// compactValidationSchema renders a step's validation schema as a short field list for the prompt. The raw JSON
// (indented, with null limits spelled out) repeated what the task's Output section already says and cost ~2.5 KB. When
// the JSON cannot be read it is returned unchanged.
func compactValidationSchema(raw string) string {
	var schema ValidationSchema
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &schema) != nil || (len(schema.Files) == 0 && len(schema.DB) == 0) {
		return raw
	}
	var b strings.Builder
	for _, file := range schema.Files {
		b.WriteString("- File `" + file.FileName + "`")
		if file.MustExist {
			b.WriteString(" (required)")
		}
		b.WriteString("\n")
		for _, check := range file.JSONChecks {
			b.WriteString("  - `" + check.Path + "`: " + describeJSONCheck(check) + "\n")
		}
	}
	for _, rule := range schema.DB {
		name := rule.Name
		if name == "" {
			name = "database check"
		}
		b.WriteString("- Database check " + name + ": `" + strings.Join(strings.Fields(rule.SQL), " ") + "`")
		if rule.MinRows != nil {
			b.WriteString(fmt.Sprintf(", at least %d row(s)", *rule.MinRows))
		}
		if rule.MaxRows != nil {
			b.WriteString(fmt.Sprintf(", at most %d row(s)", *rule.MaxRows))
		}
		b.WriteString("\n")
		for _, check := range rule.Checks {
			b.WriteString("  - first row `" + check.Path + "`: " + describeJSONCheck(check) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func describeJSONCheck(check JSONValidationCheck) string {
	var parts []string
	if check.ValueType != "" {
		parts = append(parts, check.ValueType)
	}
	if check.MustExist {
		parts = append(parts, "required")
	}
	if check.MinLength != nil {
		parts = append(parts, fmt.Sprintf("length >= %d", *check.MinLength))
	}
	if check.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("length <= %d", *check.MaxLength))
	}
	if check.MinValue != nil {
		parts = append(parts, fmt.Sprintf("value >= %v", *check.MinValue))
	}
	if check.MaxValue != nil {
		parts = append(parts, fmt.Sprintf("value <= %v", *check.MaxValue))
	}
	if check.Pattern != "" {
		parts = append(parts, "matches `"+check.Pattern+"`")
	}
	if check.ConsistencyCheck != nil {
		parts = append(parts, strings.ReplaceAll(check.ConsistencyCheck.Type, "_", " ")+" `"+check.ConsistencyCheck.CompareWithPath+"`")
	}
	if len(parts) == 0 {
		return "present"
	}
	return strings.Join(parts, ", ")
}
