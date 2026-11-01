package step_based_workflow

import (
	"context"
	"fmt"
	"strings"
)

// Input sets share the run directory with its own payload. Use the same folder
// normalization as execution so names cannot collide after sanitization.
func ValidateInputSetName(name string) error {
	clean := inputSetFolderName(name)
	if clean == "execution" || clean == "logs" || strings.HasPrefix(strings.TrimSpace(name), ".") {
		return fmt.Errorf("input-set name %q is reserved for run files; choose a different name", name)
	}
	return nil
}

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
		if err := ValidateInputSetName(group.Name); err != nil {
			return nil, err
		}
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

// Workshop tools run one input set at a time. Share the controller's omitted
// versus empty selection rules, and refresh from disk so deleted/malformed
// inputs cannot silently reuse the previous turn's selection.
func (hcpo *StepBasedWorkflowOrchestrator) resolveWorkshopInputGroup(ctx context.Context, requested string, selection []string) (string, error) {
	_, manifest, err := NewVariableManager(hcpo.BaseOrchestrator).checkExistingVariables(ctx, "variables/variables.json")
	if err != nil {
		return "", err
	}
	hcpo.variablesManifest = manifest
	if requested != "" {
		if manifest != nil {
			exact := false
			for _, group := range manifest.Groups {
				if group.Name == requested {
					exact = true
					break
				}
			}
			if !exact {
				alias := ""
				for _, group := range manifest.Groups {
					if inputSetFolderName(group.Name) == requested {
						if alias != "" {
							return "", fmt.Errorf("input-set alias %q matches several names; use an exact saved name", requested)
						}
						alias = group.Name
					}
				}
				if alias != "" {
					requested = alias
				}
			}
		}
		selection = []string{requested}
	}
	groups, err := ResolveExecutionInputs(manifest, selection)
	if err != nil {
		return "", err
	}
	if len(groups) == 0 {
		return "", fmt.Errorf("no input sets selected; choose an enabled input set")
	}
	if len(groups) != 1 {
		return "", fmt.Errorf("group_name is required when several input sets are enabled; choose one from variables/variables.json")
	}
	return groups[0].Name, nil
}
