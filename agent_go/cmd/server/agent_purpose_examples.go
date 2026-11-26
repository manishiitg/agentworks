package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Examples are display/search metadata, never instructions or an access grant.
func agentPurposeExamples(args map[string]any) ([]string, bool, error) {
	value, present := args["examples"]
	if !present {
		return nil, false, nil
	}
	raw, err := json.Marshal(value)
	var examples []string
	if err != nil || string(raw) == "null" || json.Unmarshal(raw, &examples) != nil || len(examples) < 3 || len(examples) > 5 {
		return nil, true, fmt.Errorf("examples must contain 3 to 5 short requests")
	}
	seen := map[string]bool{}
	for i, text := range examples {
		clean := cleanAgentPurpose(text)
		if strings.ContainsAny(text, "\r\n") || clean == "" || utf8.RuneCountInString(clean) > 160 || seen[strings.ToLower(clean)] {
			return nil, true, fmt.Errorf("each example must be a distinct single-line request of at most 160 characters")
		}
		examples[i] = clean
		seen[strings.ToLower(clean)] = true
	}
	return examples, true, nil
}

func agentPurposeExamplesSchema() map[string]any {
	return map[string]any{"type": "array", "minItems": 3, "maxItems": 5, "uniqueItems": true, "items": map[string]any{"type": "string", "maxLength": 160}, "description": "3 to 5 short Ask me to requests from this agent's real work. Display/search metadata, not instructions. Omit to keep existing examples."}
}
