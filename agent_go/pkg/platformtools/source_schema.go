package platformtools

import "fmt"

// Brain's real schemas use oneOf branches distinguished by the source action.
// Select those branches before removing the fixed selector. The remaining
// payload constraints still belong to the captured source, not to the family.
func specializeSourceSelector(schema map[string]any, key, value string) error {
	if condition, ok := schema["if"].(map[string]any); ok {
		props, _ := condition["properties"].(map[string]any)
		if _, uses := props[key]; uses {
			// Current access schemas condition only on their selector. Keep
			// payload-dependent conditions intact rather than guessing at them.
			if len(props) != 1 {
				return fmt.Errorf("cannot specialize %s with a payload-dependent source condition", key)
			}
			for keyword := range condition {
				if keyword != "properties" && keyword != "required" && keyword != "type" {
					return fmt.Errorf("unsupported %s source condition keyword %s", key, keyword)
				}
			}
			if required, ok := condition["required"].([]any); ok {
				for _, field := range required {
					if field != key {
						return fmt.Errorf("source condition requires payload field %v", field)
					}
				}
			}
			validator, err := compileSchema(condition)
			if err != nil {
				return err
			}
			branch := "then"
			if validator.Validate(map[string]any{key: value}) != nil {
				branch = "else"
			}
			selected, _ := schema[branch].(map[string]any)
			delete(schema, "if")
			delete(schema, "then")
			delete(schema, "else")
			if selected != nil {
				branches, _ := schema["allOf"].([]any)
				schema["allOf"] = append(branches, selected)
			}
		}
	}
	if branches, ok := schema["allOf"].([]any); ok {
		for _, raw := range branches {
			branch, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("allOf source branch must be an object")
			}
			if err := specializeSourceSelector(branch, key, value); err != nil {
				return err
			}
		}
	}
	for _, keyword := range []string{"oneOf", "anyOf"} {
		branches, ok := schema[keyword].([]any)
		if !ok {
			continue
		}
		selected := []any{}
		for _, raw := range branches {
			branch, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s source branch must be an object", keyword)
			}
			props, _ := branch["properties"].(map[string]any)
			selector, _ := props[key].(map[string]any)
			if fixed, exists := selector["const"]; exists && fixed != value {
				continue
			}
			if values, exists := selector["enum"]; exists {
				choices, ok := values.([]any)
				if !ok {
					return fmt.Errorf("%s source enum must be an array", key)
				}
				found := false
				for _, choice := range choices {
					if choice == value {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			if err := specializeSourceSelector(branch, key, value); err != nil {
				return err
			}
			selected = append(selected, branch)
		}
		if len(selected) == 0 {
			return fmt.Errorf("source has no %s branch for %s=%s", keyword, key, value)
		}
		schema[keyword] = selected
	}
	props, _ := schema["properties"].(map[string]any)
	delete(props, key)
	if required, ok := schema["required"].([]any); ok {
		kept := []any{}
		for _, field := range required {
			if field != key {
				kept = append(kept, field)
			}
		}
		schema["required"] = kept
	}
	return nil
}
