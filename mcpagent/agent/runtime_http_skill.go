package mcpagent

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const runtimeHTTPSkillName = "runtime-http-tools"

// One tutorial body serves legacy inline prompts and progressive skill loading.
const runtimeHTTPInstructions = "Call HTTP tools through execute_shell_command: curl --fail-with-body -sS --json '<payload>' -H \"$MCP_AUTH\" \"$MCP_CUSTOM/<tool>\" (real MCP: $MCP_MCP/<server>/<tool>). Custom groups are labels, not URL segments. MCP_AUTH is already the complete `Authorization: Bearer ...` header. --json already selects POST and Content-Type. Keep curl unpiped to preserve its nonzero HTTP-failure status. Encode complex payloads with a JSON encoder rather than hand-quoting. Check the response envelope's success/error before using result. The bridge environment is configured; report actual tool failures. Platform credentials exist only in the bridge shell: the CLI's own shell tool (bash, shell, Bash) has no MCP_AUTH or MCP_CUSTOM, so a platform curl there fails with \"missing or invalid Authorization header\". Do not look for tokens or retry there; run the same command through the bridge shell tool.\n"

// Runtime transport instructions are mcpagent-owned. This skill teaches HTTP
// mechanics without listing tools or granting authority; current schemas win.
func (a *Agent) ensureRuntimeHTTPSkill() error {
	if !a.useCodeExecutionMode {
		return nil
	}
	mechanics := runtimeHTTPInstructions
	if !a.admitsCoreBridgeTool("execute_shell_command") {
		// Native Crew sessions do not register the workflow bridge shell.
		// Keep the managed skill, replacing its old instructions on disk too.
		mechanics = runtimeDiscoveryInstructions + " Use the declared call_tool with tool_name and arguments for platform and connected-app calls. Use native shell/file tools only for work within the session's allowed folders, not to call the platform HTTP bridge. Native shells do not receive platform HTTP credentials. Do not search for tokens or switch to a laptop terminal to work around a missing tool. If current discovery and the inventory do not offer it, report that it is unavailable in this chat.\n"
	}
	names := "Discover exact admitted names through search_tools; broaden or enumerate a returned group/server when a query misses. Prefer the declared direct search_tools tool. For HTTP discovery, get_api_spec(tool_name=\"search_tools\") returns its authenticated session-scoped virtual route; use that route rather than $MCP_CUSTOM/search_tools."
	return a.attachSkill(&llmtypes.Skill{
		Name:        runtimeHTTPSkillName,
		Description: "Read before executing platform or connected-app tools over HTTP: current names, exact schemas/routes, auth headers, JSON quoting and response/error handling. Grants no tool or filesystem permission.",
		Content:     "# HTTP tool execution\n\n" + names + " Load get_api_spec for the selected names and use its current schema and route. Never guess a name or treat a prior catalog or discovery result as authority. Native declared tools use their provider's exact identifiers; read_skill is intrinsic, never HTTP.\n\n" + mechanics,
		Source:      llmtypes.SkillSource{Origin: "builtin"},
	})
}

func (a *Agent) hasRuntimeHTTPSkill() bool {
	for _, skill := range a.attachedSkills {
		if skill != nil && skill.Name == runtimeHTTPSkillName {
			return true
		}
	}
	return false
}
