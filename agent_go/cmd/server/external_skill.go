package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Hosted skill endpoints. ChatGPT and Claude Cowork connect over remote MCP
// and have no skill directory, so the Connect tab offers the AgentWorks skill
// as a downloadable zip (Plugins/Skills upload) plus the same text for
// pasting into Custom Instructions / connector instructions on plans without
// Skills support. The zip follows the Agent Skills SKILL.md layout that
// ChatGPT, Claude, and Cowork all accept for upload.
//
// The skill carries the server origin but never a credential. Hosted clients
// obtain access through OAuth; skill text can be shared across workspaces.

// hostedSkillDescription is the SKILL.md frontmatter description: what the
// skill does and when to use it. Keep it under 1024 chars with no XML
// brackets (frontmatter constraints shared by the upload scanners).
const hostedSkillDescription = "Use AgentWorks workflows, Crews and shared Knowledge Base over MCP (list workflows, read files, plans, runs, guidance, and knowledge; execute steps, workflows, and schedules; ask Crews and call their functions). Use when the task touches an AgentWorks workflow or when agentworks tools are available."

// buildHostedSkillMarkdown renders the hosted SKILL.md. It must stay
// self-contained: ChatGPT delivers tools only (no MCP prompts, resources, or
// initialize instructions), so a hosted agent sees exactly this text plus
// tool schemas. It shares sections with
// agent_go/pkg/agentworksclient/skills/agentworks/SKILL.md, but the dispatch
// differs: the remote surface exposes only get_api_spec and call_tool, so
// every tool below runs through call_tool.
func buildHostedSkillMarkdown(origin string) string {
	server := strings.TrimRight(strings.TrimSpace(origin), "/")
	var body strings.Builder
	body.WriteString("---\nname: agentworks\ndescription: " + hostedSkillDescription + "\n---\n")
	fmt.Fprintf(&body, `
# AgentWorks

You are connected to an AgentWorks server at %s via MCP. This connection exposes exactly two tools and reads and runs through them: tools read, and run-mode tools execute in pinned Run-mode sessions. Workflow editing requires the separately authorized Builder operations described below. Workflows below are identified by workflow ID, never by filesystem path.

## First step

Call `+"`get_api_spec`"+` with no arguments to list every available tool. Call `+"`get_api_spec`"+` again with names for their JSON schemas. Execute everything with `+"`call_tool`"+`, passing the tool name and its arguments — never call a listed tool directly, only these two tools exist. Discover workflow IDs with `+"`list_workflows`"+` first — IDs are never filesystem paths. Call `+"`get_agent_context`"+` for token capabilities and the guidance version.

## Guidance per task

List topics with `+"`list_guidance_topics`"+` and load only relevant ones via `+"`get_guidance_topic`"+`. Inspect workflow knowledge with `+"`list_workflow_knowledge`"+` / `+"`read_workflow_knowledge`"+` (learnings, knowledgebase notes, workspace skills, skill wiring). Use `+"`get_file_link`"+` for preview/download URLs.

## Shared Knowledge Base

Use get_api_spec to discover schemas and call_tool to invoke browse_knowledgebase (folders/entries), read_knowledgebase (read/search), update_knowledgebase (create/update/delete/create_folder), backup_knowledgebase (status/commit/push/git), and manage_knowledgebase_access. Updates/deletes require the current expected_version and a stable request_id. Diff patches support large files; saves are readable immediately. Git is explicit: commit selected versions only when requested, then push the receipt with a different request ID. Unrestricted root Editors can also use backup_knowledgebase action=git with op for Files-style staging, commit/push, pull, branches and stashes. Repository history needs root Reader. Pull/checkout update live knowledge and require a clean tree; stash/discard also affect live content. Retry uncertain Git pushes with the original request ID. Managed/scoped content connections retain selected receipt backups.

Writable unrestricted external connections can inspect/list access, grant/revoke folder access and manage service accounts. Folder Owner authority is required for grants; service-account administration requires an administrator. Inspect first, then use expected_acl_version and stable request_id for grant/revoke. Changes apply directly after live permission checks. App access chat keeps its confirmation flow. Read-only, folder-scoped and managed workflow/Crew connections cannot administer access; project bindings stay in the app's access builder. Never infer access changes or a Git push from a request to edit content. Administrators can configure initial Git backup with manage_knowledgebase_access action=configure_backup, the user's exact HTTPS remote_url, username, optional pat, optional branch (default main), and stable request_id. The PAT is encrypted in KB's own private storage and never returned; no Vault dependency. Omit pat on subsequent calls to retain it, or set pat to an empty string to remove it. Setup does not create the repository or commit/push, and cannot redirect existing backups. SSH URLs remain supported without a PAT, using the host's SSH credentials. In app chat use the secure confirmation field for the PAT, never messages.

Local single-user MCP uses the fixed agentworks-local Bearer token from Connect, valid until removed. Hosted/multi-user servers use OAuth approval. Never include credentials in skill text, URLs or messages.

## Run

To run: call a run-mode tool such as `+"`execute_step`"+` — the reply carries `+"`session_id`"+` — then poll `+"`run_status`"+` for completion. Steer live work with `+"`send_step_message`"+`, stop it with `+"`stop_step`"+` / `+"`stop_all_executions`"+`, and read run evidence with `+"`list_runs`"+`, `+"`get_run`"+`, and `+"`get_logs`"+`. Schedules: `+"`list_schedules`"+`, `+"`get_schedule_runs`"+`, `+"`trigger_schedule`"+`.

## Chat

`+"`chat`"+` asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `+"`session_id`"+` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `+"`run_status`"+`, and answer waiting human-input steps with `+"`run_reply_input`"+`.

## Crews

Workflows expose typed functions (their Builder defines them): `+"`list_workflow_functions`"+` shows each one's inputs, and `+"`call_workflow_function`"+` runs it. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. To ask the workflow's owner for a change instead, use `+"`suggest_workflow_change`"+` (any user with access, including read-only); it changes nothing by itself. Poll `+"`get_workflow_function_call`"+` for longer runs. When that call returns pending_inputs, answer its request_id with `+"`reply_workflow_function_call`"+`.

Crews are persistent AgentWorks agents. Discover them with `+"`list_crews`"+` (IDs, never paths); `+"`get_crew`"+` shows identity, model, and functions. Read project files with `+"`list_crew_files`"+` / `+"`read_crew_file`"+`; find a file by name with `+"`list_crew_files`"+`' `+"`glob`"+` (e.g. `+"`**/*CHECKLIST*`"+`, with `+"`depth`"+` up to 8) or by content with `+"`search_crew_files`"+` instead of paging the whole listing. Call a Crew's typed functions with `+"`call_crew_function`"+` (arguments must match `+"`list_crew_functions`"+`), or ask anything with `+"`ask_crew`"+`. `+"`ask_crew`"+` is your own message in your own chat of that Crew, the same chat your web chat, Slack DMs and WhatsApp continue (for a Crew you own, its main chat), so repeated asks are a chat and you see them in the app. `+"`call_crew_function`"+` runs in a separate conversation for your calls. Functions are agentic and usually take minutes: a call returns at once with `+"`status: running`"+` and a `+"`call_id`"+`, then poll `+"`get_crew_function_call`"+` for progress and the result (pass `+"`wait_seconds`"+`, max 25, only for a quick one). Never call again for the same work: repeating an identical call while it runs returns the same `+"`call_id`"+`. To ask a Crew's owner for a change, use `+"`suggest_crew_change`"+` (`+"`crews:run`"+`); the owner reviews it in the Crew's Suggestions view. To author, `+"`create_crew`"+` makes a Crew you own from a spec, `+"`update_crew`"+` edits one you own, and `+"`export_crew`"+` / `+"`import_crew`"+` move a Crew between accounts or servers as a portable spec (`+"`crews:write`"+`; only the owner edits).

Pass a fresh `+"`submission_id`"+` for each new Crew ask or function call, and reuse it after an uncertain delivery. If `+"`get_crew_function_call`"+` returns `+"`pending_inputs`"+`, answer a listed `+"`request_id`"+` with `+"`reply_crew_function_call`"+`. A workflow function call uses the matching `+"`reply_workflow_function_call`"+` tool.

## Vault management

When manage_vault_access appears in get_api_spec, this administrator connection can manage Vault. Inspect the environment and exact tool schemas before connecting MCPs or saving live permissions. Regex conditions require a human-readable description. Use manage_vault_groups for groups and active platform members, and manage_vault_secret_access to list secret names or grant/revoke a group. No workflow_id is needed. Secret values are never returned. Upstream tools use the separate Vault MCP connection; this management grant does not bypass user/group runtime access.

## Builder

When builder_chat appears in get_api_spec, the connection can delegate plan/code edits to the workflow's configured Builder model on selected workflows where you have write access. Check get_agent_context for the workflow's effective tools. builder_chat continues your existing workflow chat (the owner's main chat); send a unique submission_id with each new request. Poll builder_status using operation_id, answer that operation's pending questions with builder_reply_input, and cancel only that operation with builder_cancel. Reuse the submission_id to retry uncertain delivery; do not resend the same edit with a new ID. Native shell and account tools are unavailable to this Builder mode.

If Builder is absent, describe or suggest the needed workflow change; do not attempt an unavailable authoring operation.
`, server)
	return body.String()
}

// handleExternalSkillMD serves the hosted SKILL.md text for pasting into
// Custom Instructions / connector instructions.
func (api *StreamingAPI) handleExternalSkillMD(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, buildHostedSkillMarkdown(getBaseURL(r)))
}

// handleExternalSkillZIP serves the hosted skill as a zip for the
// Plugins/Skills upload flow (ChatGPT, Cowork).
func (api *StreamingAPI) handleExternalSkillZIP(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	entry, err := archive.Create("agentworks/SKILL.md")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	if _, err := io.WriteString(entry, buildHostedSkillMarkdown(getBaseURL(r))); err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	if err := archive.Close(); err != nil {
		externalError(w, http.StatusInternalServerError, "skill_unavailable", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="agentworks-skill.zip"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// handleExternalPlugin packages the hosted skill with a Cowork remote MCP
// connector. The OAuth client discovers authorization from the public MCP URL;
// no credential or client secret belongs in the downloadable archive.
func (api *StreamingAPI) handleExternalPlugin(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, http.StatusUnauthorized, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	// mcpOAuthURLs also admits a loopback URL (local MCP sign-in); a plugin
	// is installed elsewhere, so it needs the public HTTPS URL.
	origin, resource, ok := mcpOAuthURLs()
	if !ok || !strings.HasPrefix(origin, "https://") {
		externalError(w, http.StatusServiceUnavailable, "plugin_unavailable", "The Cowork plugin requires a configured public HTTPS URL.")
		return
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"name": "agentworks", "version": "0.1.0",
		"description": "Read AgentWorks workflow knowledge and test code, inspect runs, and run permitted workflows.",
		"author":      map[string]string{"name": "AgentWorks"},
	}, "", "  ")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	connector, err := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"agentworks": map[string]string{"type": "http", "url": resource}},
	}, "", "  ")
	if err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	files := []struct{ name, content string }{
		{".claude-plugin/plugin.json", string(manifest) + "\n"},
		{".mcp.json", string(connector) + "\n"},
		{"skills/agentworks/SKILL.md", buildHostedSkillMarkdown(origin)},
		{"README.md", "# AgentWorks for Claude Cowork\n\nInstall this plugin in Customize > Plugins, then connect AgentWorks and approve access in your browser. The connector uses OAuth; this package contains no credential.\n"},
	}
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	for _, file := range files {
		entry, err := archive.Create(file.name)
		if err == nil {
			_, err = io.WriteString(entry, file.content)
		}
		if err != nil {
			externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
			return
		}
	}
	if err := archive.Close(); err != nil {
		externalError(w, http.StatusInternalServerError, "plugin_unavailable", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="agentworks.plugin"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
