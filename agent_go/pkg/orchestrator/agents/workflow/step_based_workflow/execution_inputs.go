package step_based_workflow

import "fmt"

// ResolveExecutionInputs distinguishes omitted selection from an explicit empty
// batch. PLAT-746-265: missing groups means one invocation, not missing setup;
// disabled or unknown groups must never turn into an ungrouped invocation.
func ResolveExecutionInputs(manifest *VariablesManifest, selected []string) ([]VariableGroup, error) {
	groups := []VariableGroup{}
	if manifest != nil {
		groups = manifest.Groups
	}
	if selected != nil && len(selected) == 0 {
		return []VariableGroup{}, nil
	}
	if len(groups) == 0 {
		if len(selected) > 0 {
			return nil, fmt.Errorf("unknown input set %q: no input sets are configured", selected[0])
		}
		values := map[string]string{}
		if manifest != nil {
			values = manifest.GetVariableValues("")
		}
		return []VariableGroup{{Values: values, Enabled: true}}, nil
	}
	byName := map[string]VariableGroup{}
	for _, group := range groups {
		if group.Name == "" {
			return nil, fmt.Errorf("saved input sets need a name")
		}
		if _, found := byName[group.Name]; found {
			return nil, fmt.Errorf("duplicate input set %q", group.Name)
		}
		group.Values = MergeGroupWithDefaults(manifest, group.Values)
		byName[group.Name] = group
	}
	result := []VariableGroup{}
	if selected != nil {
		seen := map[string]bool{}
		for _, name := range selected {
			group, found := byName[name]
			if !found {
				return nil, fmt.Errorf("unknown input set %q", name)
			}
			// Explicit selection retains the existing manual selection policy:
			// Enabled controls automatic selection, not an explicit caller choice.
			if !seen[name] {
				result = append(result, group)
				seen[name] = true
			}
		}
		return result, nil
	}
	for _, group := range groups {
		if group.Enabled {
			result = append(result, byName[group.Name])
		}
	}
	return result, nil
}
