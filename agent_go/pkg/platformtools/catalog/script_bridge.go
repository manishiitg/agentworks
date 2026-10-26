package catalog

import (
	"context"
	"fmt"
	"strings"
)

// ScriptExecutor is one tool a saved script reaches through the step bridge.
type ScriptExecutor = func(context.Context, map[string]interface{}) (string, error)

// ScriptFamilies gives a saved script the public family names of the member tools its step already has: a call to
// the family with action=<name> runs that member with the remaining arguments. Nothing beyond the step's own
// members becomes reachable.
func ScriptFamilies(members map[string]ScriptExecutor) map[string]ScriptExecutor {
	byFamily := map[string]map[string]ScriptExecutor{}
	for _, action := range Actions {
		if action.Scope != "" {
			continue
		}
		fn, ok := members[action.Member]
		if !ok && action.Source != "" {
			if source, admitted := members[action.Source]; admitted {
				parameter, selected := action.SourceParameter(), action.SourceAction
				fn = func(ctx context.Context, args map[string]interface{}) (string, error) {
					args[parameter] = selected
					return source(ctx, args)
				}
				ok = true
			}
		}
		if !ok {
			continue
		}
		if byFamily[action.Family] == nil {
			byFamily[action.Family] = map[string]ScriptExecutor{}
		}
		if _, resolved := byFamily[action.Family][action.Name]; !resolved {
			byFamily[action.Family][action.Name] = fn
		}
	}
	out := make(map[string]ScriptExecutor, len(byFamily))
	for family, actions := range byFamily {
		family, actions := family, actions
		out[family] = func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["action"].(string)
			fn, ok := actions[strings.TrimSpace(name)]
			if !ok {
				return "", fmt.Errorf("%s has no action %q for this step", family, name)
			}
			input := make(map[string]interface{}, len(args))
			for key, value := range args {
				if key != "action" {
					input[key] = value
				}
			}
			return fn(ctx, input)
		}
	}
	return out
}
