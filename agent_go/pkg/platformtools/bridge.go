package platformtools

import mcpagent "github.com/manishiitg/mcpagent/agent"

// BridgeRuntime exposes exactly the projected, already-admitted native catalog
// to coding CLIs. Source names retired by projection are never bridge aliases.
func BridgeRuntime(projected []mcpagent.ToolDefinition, runtime mcpagent.RuntimeConfig) mcpagent.RuntimeConfig {
	bridge := []string{}
	seen := map[string]bool{}
	for _, tool := range projected {
		if !seen[tool.Name] {
			bridge = append(bridge, tool.Name)
			seen[tool.Name] = true
		}
	}
	for _, name := range runtime.Tools.AdditionalBridge {
		name = PublicName(name)
		if IsFamily(name) && !seen[name] {
			continue
		}
		if !seen[name] {
			bridge = append(bridge, name)
			seen[name] = true
		}
	}
	runtime.Tools.AdditionalBridge = bridge
	admit := runtime.Coding.BridgeToolAdmit
	runtime.Coding.BridgeToolAdmit = func(name string) bool {
		if IsFamily(name) || IsMember(name) {
			return seen[name]
		}
		return admit == nil || admit(name)
	}
	return runtime
}
