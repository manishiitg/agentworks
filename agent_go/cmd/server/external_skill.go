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
const hostedSkillDescription = "Read and run AgentWorks workflows and Crews over MCP (list workflows, read files, plans, runs, guidance, and knowledge; execute steps, workflows, and schedules; ask Crews and call their functions). Use when the task touches an AgentWorks workflow or when agentworks tools are available."

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

You are connected to an AgentWorks server at %s via MCP. This connection exposes exactly two tools and reads and runs through them: tools read, and run-mode tools execute in pinned Run-mode sessions. Nothing creates, edits, or authors. Workflows below are identified by workflow ID, never by filesystem path.

## First step

Call `+"`get_api_spec`"+` with no arguments to list every available tool. Call `+"`get_api_spec`"+` again with names for their JSON schemas. Execute everything with `+"`call_tool`"+`, passing the tool name and its arguments — never call a listed tool directly, only these two tools exist. Discover workflow IDs with `+"`list_workflows`"+` first — IDs are never filesystem paths. Call `+"`get_agent_context`"+` for token capabilities and the guidance version.

## Guidance per task

List topics with `+"`list_guidance_topics`"+` and load only relevant ones via `+"`get_guidance_topic`"+`. Inspect workflow knowledge with `+"`list_workflow_knowledge`"+` / `+"`read_workflow_knowledge`"+` (learnings, knowledgebase notes, workspace skills, skill wiring). Use `+"`get_file_link`"+` for preview/download URLs.

## Run

To run: call a run-mode tool such as `+"`execute_step`"+` — the reply carries `+"`session_id`"+` — then poll `+"`run_status`"+` for completion. Steer live work with `+"`send_step_message`"+`, stop it with `+"`stop_step`"+` / `+"`stop_all_executions`"+`, and read run evidence with `+"`list_runs`"+`, `+"`get_run`"+`, and `+"`get_logs`"+`. Schedules: `+"`list_schedules`"+`, `+"`get_schedule_runs`"+`, `+"`trigger_schedule`"+`.

## Chat

`+"`chat`"+` asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `+"`session_id`"+` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `+"`run_status`"+`, and answer waiting human-input steps with `+"`run_reply_input`"+`.

## Crews

Workflows expose typed functions (their Builder defines them): `+"`list_workflow_functions`"+` shows each one's inputs, and `+"`call_workflow_function`"+` runs it. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. Poll `+"`get_workflow_function_call`"+` for longer runs.

Crews are persistent AgentWorks agents. Discover them with `+"`list_crews`"+` (IDs, never paths); `+"`get_crew`"+` shows identity, model, and functions. Read project files with `+"`list_crew_files`"+` / `+"`read_crew_file`"+`. Call a Crew's typed functions with `+"`call_crew_function`"+` (arguments must match `+"`list_crew_functions`"+`), or ask anything with `+"`ask_crew`"+`. Both run in your own continuing conversation with that Crew (one per AgentWorks user; never the Crew's main chat), so repeated `+"`ask_crew`"+` calls are a chat: the Crew remembers your earlier asks. Functions are agentic and usually take minutes: a call returns at once with `+"`status: running`"+` and a `+"`call_id`"+`, then poll `+"`get_crew_function_call`"+` for progress and the result (pass `+"`wait_seconds`"+`, max 25, only for a quick one). Never call again for the same work: repeating an identical call while it runs returns the same `+"`call_id`"+`. To author, `+"`create_crew`"+` makes a Crew you own from a spec, `+"`update_crew`"+` edits one you own, and `+"`export_crew`"+` / `+"`import_crew`"+` move a Crew between accounts or servers as a portable spec (`+"`crews:write`"+`; only the owner edits).

## Answer from reading

If the task needs a change, say so instead of attempting one — authoring is not exposed.
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
