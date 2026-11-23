package mcpagent

import "strings"

const runtimeDiscoveryInstructions = "Find authorized tools with search_tools, load their exact input schemas with get_api_spec, then run them with call_tool. Discovery and schema lookup never grant permission."

// Runtime mechanics only. Product procedures belong to caller-owned skills.
// Names come from the same admission predicate as the direct bridge manifest.
func bridgeRoutingExplicitInstructions(admits func(name string) bool, additionalBridgeTools ...string) string {
	return bridgeRoutingInstructions(admits, false, additionalBridgeTools...)
}

func bridgeRoutingInstructions(admits func(name string) bool, skillFirst bool, additionalBridgeTools ...string) string {
	allow := func(name string) bool { return admits == nil || admits(name) }
	names := []string{"search_tools", "get_api_spec", "call_tool"}
	for _, name := range []string{"execute_shell_command", "diff_patch_workspace_file", "agent_browser"} {
		if allow(name) {
			names = append(names, name)
		}
	}
	// Every native bridge tool is a direct tool. The caller passes only names that
	// are really exposed (registered and allowed), so a tool the model has natively
	// is never described as something to find through search_tools and HTTP: a
	// session with no shell would otherwise hit a dead end.
	listed := map[string]bool{}
	for _, name := range names {
		listed[name] = true
	}
	for _, name := range additionalBridgeTools {
		if name != "" && !listed[name] && allow(name) {
			listed[name] = true
			names = append(names, name)
		}
	}
	text := "IMPORTANT — bridge tool routing\nDirect runtime tools: " + strings.Join(names, ", ") + ". Use the exact identifiers in your provider's declared tool list.\n"
	for _, name := range names {
		switch name {
		case "read_skill":
			text += "read_skill is intrinsic; never call it over HTTP.\n"
		case "read_image":
			text += "Use read_image for platform image analysis.\n"
		}
	}
	text += "Find other tools with search_tools; get_api_spec(tool_name=...) returns their schema and route. A tool absent from your direct tool list may still be available through search_tools. If a focused search finds no match, check the relevant group or current inventory once. If it is absent there too, explain that it is unavailable in this chat; do not keep searching, invent a tool name or claim it ran. Only declared runtime tools are direct calls; run any other tool with call_tool(tool_name, arguments), never a direct tool call by its bare name. Discovery is live and does not grant permission.\n"
	text += "Prefer calling search_tools directly when declared. It also supports the authenticated HTTP bridge: get_api_spec(tool_name=\"search_tools\") returns its session-scoped virtual route and schema. Use that returned route, not a custom-tool route.\n"
	if allow("execute_shell_command") {
		if skillFirst {
			text += "Before HTTP execution, read the attached runtime-http-tools skill. Use the authorized shell route and current schema; never print credentials or ignore a failed response.\n"
		} else {
			text += runtimeHTTPInstructions
		}
	}
	return text
}
