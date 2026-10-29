package platformtools

import mcpagent "github.com/manishiitg/mcpagent/agent"

// coreBridgeFamilies are the projected tools every coding CLI gets natively:
// the working tools a chat uses on most turns. Every other admitted platform
// tool stays in the session catalog: search_tools / get_api_spec find it and
// the bridge's call_tool runs it on its usual route, so its schema is not resent with every model call
// (PLAT-825-130 had published all of them, about 26K tokens per call).
var coreBridgeFamilies = map[string]bool{"terminal": true, "browser": true, "files": true}

// BridgeRuntime exposes the core projected tools plus the caller's explicit
// bridge tools to coding CLIs. Source names retired by projection are never
// bridge aliases.
func BridgeRuntime(projected []mcpagent.ToolDefinition, runtime mcpagent.RuntimeConfig) mcpagent.RuntimeConfig {
	inCatalog := map[string]bool{}
	for _, tool := range projected {
		inCatalog[tool.Name] = true
	}
	bridge := []string{}
	seen := map[string]bool{}
	for _, tool := range projected {
		if coreBridgeFamilies[tool.Name] && !seen[tool.Name] {
			bridge = append(bridge, tool.Name)
			seen[tool.Name] = true
		}
	}
	for _, name := range runtime.Tools.AdditionalBridge {
		name = PublicName(name)
		if IsFamily(name) && !inCatalog[name] {
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
