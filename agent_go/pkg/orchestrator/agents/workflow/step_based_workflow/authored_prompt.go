package step_based_workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var authoredPromptVariable = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
var authoredStepID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// renderAuthoredPrompt resolves Relay input paths before an agent turn starts.
// INPUT is the JSON object supplied to the existing workflow function trigger.
// Missing paths fail the run instead of silently sending unresolved placeholders.
func renderAuthoredPrompt(prompt string, variables map[string]string) (string, error) {
	return renderAuthoredPromptWithSteps(prompt, variables, nil)
}

func renderAuthoredPromptWithSteps(prompt string, variables map[string]string, loadStep func(string) (string, error)) (string, error) {
	var input any
	inputLoaded := false
	stepOutputs := map[string]any{}
	var renderErr error
	rendered := authoredPromptVariable.ReplaceAllStringFunc(prompt, func(match string) string {
		if renderErr != nil {
			return match
		}
		path := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}"))
		parts := strings.Split(path, ".")
		var current any
		keys := parts[1:]
		switch {
		case parts[0] == "input":
			if !inputLoaded {
				inputLoaded = true
				if raw := strings.TrimSpace(variables["INPUT"]); raw == "" {
					renderErr = fmt.Errorf("INPUT is missing")
					return match
				} else if err := json.Unmarshal([]byte(raw), &input); err != nil {
					renderErr = fmt.Errorf("INPUT is not valid JSON: %w", err)
					return match
				}
			}
			current = input
		case len(parts) >= 3 && parts[0] == "steps" && parts[2] == "output" && authoredStepID.MatchString(parts[1]):
			if loadStep == nil {
				renderErr = fmt.Errorf("step output %q is unavailable", parts[1])
				return match
			}
			var exists bool
			current, exists = stepOutputs[parts[1]]
			if !exists {
				raw, err := loadStep(parts[1])
				if err != nil {
					renderErr = fmt.Errorf("load step output %q: %w", parts[1], err)
					return match
				}
				if err := json.Unmarshal([]byte(raw), &current); err != nil {
					renderErr = fmt.Errorf("step output %q is not JSON: %w", parts[1], err)
					return match
				}
				stepOutputs[parts[1]] = current
			}
			keys = parts[3:]
		default:
			renderErr = fmt.Errorf("unsupported variable %q; use {{input}}, {{input.field}}, or {{steps.id.output.field}}", path)
			return match
		}
		for _, key := range keys {
			object, ok := current.(map[string]any)
			if !ok || key == "" {
				renderErr = fmt.Errorf("input path %q is missing", path)
				return match
			}
			value, exists := object[key]
			if !exists {
				renderErr = fmt.Errorf("input path %q is missing", path)
				return match
			}
			current = value
		}
		if value, ok := current.(string); ok {
			return value
		}
		encoded, err := json.Marshal(current)
		if err != nil {
			renderErr = fmt.Errorf("encode input path %q: %w", path, err)
			return match
		}
		return string(encoded)
	})
	return rendered, renderErr
}

func normalizeAuthoredJSONResult(answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	if !json.Valid([]byte(answer)) {
		return "", fmt.Errorf("final agent response must be valid JSON")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(answer)); err != nil {
		return "", fmt.Errorf("compact final JSON: %w", err)
	}
	if compact.Len() > 128*1024 {
		return "", fmt.Errorf("final JSON exceeds the 128 KiB Relay response limit")
	}
	return compact.String(), nil
}
