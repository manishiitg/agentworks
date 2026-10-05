package instructions

// GetSpecialWorkspaceToolsInstructions returns the cheat-sheet section
// for the active workspace LLM tools (text generation) and capability
// discovery). The full reference — signatures, parameters, defaults,
// provider-setup discipline — lives in the workspace-media-tools skill
// loaded on demand via read_skill(skills=[{"name":"builder-reference","path":"references/workspace-media-tools.md"}]).
//
// Used by both chat agents and workflow-builder agents.
func GetSpecialWorkspaceToolsInstructions() string {
	return `## Special Workspace Tools (cheat sheet)

Provider-backed text generation uses the configured ` + "`tier`" + ` (low, medium, high). Do not pass provider/model overrides to text generation. File-path arguments must be absolute paths under the workspace docs root.

Available tools:
- **Discovery + auth**: ` + "`list_llm_capabilities`" + `, ` + "`set_provider_auth`" + ` (always use this for API keys — never paste into shell, scripts, or config files).
- **Text**: ` + "`generate_text_llm(user_message, tier)`" + `.

Provider-setup essentials (do not hand-edit provider-auth storage — it's encrypted and managed via ` + "`set_provider_auth`" + `; audio/video/image/music providers are workspace **tool** capabilities, not published-LLM entries — call ` + "`list_llm_capabilities(capability=\"...\")`" + ` for the authoritative availability answer).

**For the full reference on this active tool — use cases, tier selection, parameters, provider routing, scripted MCP-bridge calls, and common-mistake gotchas — call:** ` + "`read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/workspace-media-tools.md\"}])`" + `. In scripted/code-execution mode, also read ` + "`references/mcp-bridge.md`" + ` before writing the bridge call; never invoke a provider directly or put credentials in a script.`
}

// GetSpecialWorkspaceToolsPointer returns the compact skill-first form of the
// workspace media/search guidance. Coding CLIs receive the complete
// workspace-media-tools reference through the projected builder-reference
// skill, so repeating the full catalog in CLAUDE.md/AGENTS.md wastes the
// provider's fixed instruction budget.
func GetSpecialWorkspaceToolsPointer() string {
	return `## Special Workspace Tools

Use the current runtime's admitted tools: native API sessions use supplied schemas; coding CLI sessions follow the declared discovery and bridge routing. Before using ` + "`generate_text_llm`" + `, read the attached ` + "`builder-reference`" + ` skill's ` + "`references/workspace-media-tools.md`" + ` (or call ` + "`read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/workspace-media-tools.md\"}])`" + `) for use cases, tier selection, parameters, provider routing, and known gotchas. In scripted/code-execution mode, also read ` + "`references/mcp-bridge.md`" + ` and invoke only the granted tool through the authenticated MCP bridge — never invoke a provider directly or put credentials in a script. Use ` + "`list_llm_capabilities`" + ` as the authoritative availability source and ` + "`set_provider_auth`" + ` for credentials; never place credentials in shell commands or files.`
}
