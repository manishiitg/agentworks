package virtualtools

// BuildCLIToolEnvironmentPrompt returns additional instructions for CLI providers
// that explain how to call api-bridge tools and route human/custom tools through HTTP.
func BuildCLIToolEnvironmentPrompt(provider string) string {
	executeTool := "mcp_api-bridge_execute_shell_command"
	specTool := "mcp_api-bridge_get_api_spec"
	browserTool := "mcp_api-bridge_agent_browser"
	if provider == "claude-code" {
		executeTool = "mcp__api-bridge__execute_shell_command"
		specTool = "mcp__api-bridge__get_api_spec"
		browserTool = "mcp__api-bridge__agent_browser"
	}

	return `
## CLI Tool Environment (CRITICAL — Read Carefully)

Your native tools (Bash, Read, Write, etc.) are **disabled**. All tool access goes through the MCP api-bridge. Your available tools are:

| Tool name | Purpose |
|-----------|---------|
| ` + "`" + executeTool + "`" + ` | Run any shell command (replaces Bash/Read/Write) |
| ` + "`" + specTool + "`" + ` | Discover human/custom tools and their API specs |
| ` + "`" + browserTool + "`" + ` | Browser automation |
| ` + "`WebSearch`" + ` | Web search |

### Tool Name Mapping
Whenever the instructions above mention ` + "`execute_shell_command(...)`" + `, call ` + "`" + executeTool + "`" + ` instead. Example:
- Instructions say: execute_shell_command(command: "ls Chats/")
- You call: ` + executeTool + `(command: "ls Chats/")

### Calling Custom Tools via HTTP API
The following tools are NOT available as direct function calls — call them via curl through ` + "`" + executeTool + "`" + `:

- **Human tools**: send_email, send_slack_message
- **LLM config tools**: list_published_llms, list_provider_models, test_llm, save_published_llm, set_provider_auth, list_llm_capabilities

**Pattern:**
` + "```" + `bash
payload='{...parameters...}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/{tool_name}"
` + "```" + `

**Examples:**

list published chat LLMs:
` + "```" + `bash
payload='{}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/list_published_llms"
` + "```" + `

list provider models:
` + "```" + `bash
payload='{"provider": "claude-code"}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/list_provider_models"
` + "```" + `

email specific people (recipients are required):
` + "```" + `bash
payload='{"to": ["person@example.com"], "subject": "Report", "body": "Ready."}'
curl -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/send_email"
` + "```" + `

$MCP_CUSTOM and $MCP_AUTH are pre-set environment variables — use them as-is.

**Important:** Whenever instructions mention ` + "`send_email(...)`" + `, ` + "`send_slack_message(...)`" + `, or LLM config tools, translate to the curl pattern above. Do NOT call these as direct function calls.

Do **NOT** read or edit ` + "`config/`" + ` files for LLM/provider configuration. Use ` + "`list_published_llms`" + ` for the published set, ` + "`list_provider_models`" + ` for provider-supported models, ` + "`test_llm`" + ` for candidate validation, and ` + "`save_published_llm`" + ` for publishing.

`
}
