package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/knowledgebaseproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type externalTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	sparkquill  bool
	mutates     bool
	executes    bool
	plan        bool
	validator   *jsonschema.Schema
	// hidden: an alias of a merged tool's action, callable but not listed.
	hidden       bool
	internalOnly bool
	// actions maps a merged tool's action to the member tool it runs.
	actions     map[string]string
	actionOrder []string
}

// Discovery exposes model-compatible fields; dispatch keeps the full schema.
func (tool externalTool) MarshalJSON() ([]byte, error) {
	type fields externalTool
	advertised := fields(tool)
	advertised.InputSchema = llmtypes.ModelToolSchema(tool.InputSchema)
	return json.Marshal(advertised)
}

var externalCatalogOnce sync.Once
var externalCatalog []externalTool
var externalCatalogErr error

func externalString(description string) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": 1}
}
func externalInteger(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}
func externalTools() ([]externalTool, error) {
	externalCatalogOnce.Do(func() {
		var defined []externalTool
		add := func(name, description string, write, scoped bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			if scoped {
				props["workflow_id"] = externalString("Workflow ID returned by workflow action=list. Never a filesystem path.")
				required = append(required, "workflow_id")
			}
			req := make([]any, len(required))
			for i, r := range required {
				req[i] = r
			}
			defined = append(defined, externalTool{Name: name, Description: description, mutates: write, InputSchema: map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}})
		}
		page := func() map[string]any {
			return map[string]any{"limit": externalInteger(1, 200), "offset": externalInteger(0, 10000)}
		}
		p := page()
		p["query"] = externalString("Filter workflow labels and IDs.")
		p["compact"] = map[string]any{"type": "boolean", "description": "Default true: id, label, owners, schedule counts and schedule counts only. Pass false for every workflow's full manifest (tens of thousands of characters); workflow action=get returns one manifest."}
		externalAgentMessageDefinition(add)
		add("chat_preferences", "Read your personal chat display, or set show_context_indicator to show or hide context and plan use in the chat box of all your chats (Show in chat box). Off by default. Changes only your display; never changes a project or provider account.", true, false, map[string]any{"show_context_indicator": map[string]any{"type": "boolean"}})
		externalServerProductDefinitions(add)
		add("list_workflows", "List workflows visible to the signed-in user (compact by default; compact: false adds every manifest).", false, false, p)
		add("get_workflow", "Read one workflow manifest and the caller's access level.", false, true, nil)
		for _, name := range []string{"list_files", "search_files"} {
			p = page()
			p["path"] = map[string]any{"type": "string", "description": "Workflow-relative directory. Omit, or pass \"\" or \".\", for the workflow root."}
			p["depth"] = externalInteger(1, 8)
			p["glob"] = externalString("Optional file path glob relative to path, e.g. **/*.py. ** matches directories recursively. Filters results before pagination and content search.")
			required := []string{}
			if name == "search_files" {
				p["query"] = externalString("Text to find; case-insensitive literal text unless match_case or regex is set.")
				externalTextSearchSchema(p)
				required = append(required, "query")
			}
			add(name, "Browse or search workflow files. Search matches file contents and file or folder names (case-insensitive), and says how many files it read and whether folders were skipped for depth. Private paths and symbolic links are excluded. Results are bounded and paginated.", false, true, p, required...)
		}
		p = page()
		p["step_id"] = externalString("Optional plan step ID; limits the inventory to its saved code directory.")
		p["glob"] = externalString("Optional path glob inside the code directory; defaults to **/*.py.")
		add("list_step_code", "List saved Python code by workflow step, with plan step IDs and titles where available. Uses code/<step-id>/ for current workflows and learnings/<step-id>/ for legacy workflows.", false, true, p)
		add("get_file_link", "Get an existing workflow, Flow or Crew file/folder's authenticated browser preview URL. Files include a download URL, size and content type. Pass exactly one of workflow_id (files:read) or crew_id (crews:read); existing visibility, project bounds and file guards apply. Links contain no credentials and grant no access; recipients must sign in with their own current project access.", false, false, map[string]any{"path": externalString("Canonical project-relative file or folder path."), "workflow_id": externalString("Workflow or Flow ID."), "crew_id": externalString("Crew ID returned by crew list.")}, "path")
		defined[len(defined)-1].InputSchema["oneOf"] = []any{map[string]any{"required": []any{"workflow_id"}}, map[string]any{"required": []any{"crew_id"}}}
		add("read_file", "Read a workflow file: text up to 2 MiB, smaller binary content as base64. Larger files up to 11 MiB return size and revision metadata without content, for guarded replacement or authenticated download.", false, true, map[string]any{"path": externalString("Workflow-relative file path.")}, "path")
		add("write_file", "Write UTF-8 source or documentation (content), or a binary file such as an image, PDF or spreadsheet (content_base64, at most 11 MiB), within your current owner/editor role and existing workflow or Flow authoring consent. Read first for expected_revision (missing for new files); use a unique request_id and reuse it only for identical retries. Planning, workflow configuration, databases and private paths are protected.", true, true, map[string]any{"path": externalString("Workflow-relative file path."), "content": externalString("UTF-8 content, at most 2 MiB."), "content_base64": externalString("Base64 of a binary file, at most 11 MiB decoded. Pass this or content."), "expected_revision": externalString("Revision returned by files action=read, or missing."), "request_id": externalString("Unique durable mutation ID (at most 128 characters).")}, "path", "expected_revision", "request_id")
		// Product declarations admit tools; current roles and existing connection
		// consent separately authorize reads, execution and direct authoring.
		// Catalog membership is admitted by product.yaml (chat.run
		// external_tools plus the run.tools proxy surface); these definitions
		// are implementations only.
		add("get_plan", "Read the plan and configuration. A large plan can be several hundred thousand characters: pass view=outline first (each step's id, type, title and a short description, plus the size of every top-level plan section), then step_id to read one step's plan entry and its configuration. Without either you get everything (plan and step configuration).", false, true, map[string]any{
			"view":    map[string]any{"type": "string", "enum": []any{"full", "outline"}, "description": "outline: step list and section sizes only. Default full."},
			"step_id": externalString("Read only this step: its plan entry and its configuration."),
		})
		add("get_agent_context", "Describe this connection for an external agent: token capabilities, available tools, and guidance version. No workflow required; pass workflow_id for the caller's role on it.", false, false, map[string]any{"workflow_id": map[string]any{"type": "string", "description": "Optional workflow ID to report the caller's role on."}})
		add("list_guidance_topics", "List the server-owned external guidance topics and their descriptions.", false, false, nil)
		add("get_guidance_topic", "Read one relevant guidance topic or one of its bundled reference files. Each topic has its own version.", false, false, map[string]any{"topic": externalString("Topic name from help action=topics."), "path": externalString("Optional bundled reference path from this topic. Omit to read the main guide; an explicit path always returns that file."), "known_version": externalString("Optional version already held for the main guide; skips unchanged text when path is omitted.")}, "topic")
		add("get_guidance_skills", "Discover versioned server guidance skill files. By default returns names, descriptions, versions and file paths only. Pass topics to fetch selected files; known_versions omits unchanged bodies. Uses this connection's existing access; no workflow required.", false, false, map[string]any{
			"topics":         map[string]any{"type": "array", "maxItems": 64, "uniqueItems": true, "items": externalString("Topic name returned in skills.")},
			"known_versions": map[string]any{"type": "object", "maxProperties": 64, "additionalProperties": externalString("Previously saved topic version.")},
		})
		add("get_skill", "Return the AgentWorks skill (SKILL.md) for this server so you can install it: save `content` to <your skills folder>/agentworks/SKILL.md (Claude Code: ~/.claude/skills/agentworks/SKILL.md). Read-only; no workflow required.", false, false, nil)
		add("list_workflow_knowledge", "List a workflow's knowledge base, local skills, legacy guide history where applicable, workspace skills, and skill wiring (workflow-selected skills plus per-step enabled_skills). Page the file inventories with limit and offset; has_more signals another page.", false, true, page())
		add("read_workflow_knowledge", "Read one workflow knowledge-base or local skill file, or a selected workspace skill. Old learning files remain readable as history.", false, true, map[string]any{"path": externalString("Knowledge path: knowledgebase/... or skills/<folder>/<file>; legacy learnings/... for history.")}, "path")
		relayVersion := externalString("Flows only: a published version from relay action=releases (e.g. v3) to read that version's production runs; omit for draft test runs. Needs owner or editor access.")
		p = page()
		p["version"] = relayVersion
		add("list_runs", "List saved run folders and their metadata files. Use runs action=get for a chosen run. For a Flow, pass version to see a published version's production runs.", false, true, p)
		for _, name := range []string{"get_run", "get_logs"} {
			p = page()
			p["version"] = relayVersion
			p["run_folder"] = externalString("Run directory relative to runs/, e.g. iteration-0/group-name.")
			description := "Inspect a saved run's files or log files; use files action=read to retrieve selected content."
			if name == "get_run" {
				description = "Inspect a saved run's files and webhook deploy metadata (commit_sha, component, env, deployed_at) when present. Use files action=read to retrieve selected content."
			}
			add(name, description, false, true, p, "run_folder")
		}
		// JSON-direct run operations. The four run.tools names keep these
		// native implementations instead of the generic proxy below;
		// run_status has no chat equivalent and is admitted by external_tools.
		addRun := func(name, description string, executes bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			props["workflow_id"] = externalString("Workflow ID returned by workflow action=list. Never a filesystem path.")
			required = append(required, "workflow_id")
			req := make([]any, len(required))
			for i, r := range required {
				req[i] = r
			}
			defined = append(defined, externalTool{Name: name, Description: description, executes: executes, InputSchema: map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}})
		}
		p = page()
		p["session_id"] = externalString("Run session ID returned by a previous run call.")
		p["since_index"] = externalInteger(-1, 1000000000)
		p["compact"] = map[string]any{"type": "boolean", "description": "Default true: turn_status (running, waiting_for_input, idle), any pending question, final_answer once idle and executions, without events. Pass false for a size-bounded page of events."}
		addRun("run_status", "Poll a run session started externally: turn_status, final_answer once the turn is done, pending inputs and its active executions, without events by default (compact: false adds a size-bounded event page).", false, p, "session_id")
		workflowPublishedFunctionDefinitions(func(name, description string, write bool, props map[string]any, required ...string) {
			if props == nil {
				props = map[string]any{}
			}
			props["project"] = externalString("Project ID or name; status and reply infer it from call_id.")
			if name != "project_functions_get_call" && name != "project_functions_reply" {
				required = append(required, "project")
			}
			add(name, description, name == "project_functions_define" || name == "project_functions_delete", false, props, required...)
		})
		addRun("suggest_workflow_change", "Suggest a change to a workflow you can use: what it should do differently. It goes to the workflow owner's decisions panel for review; nothing changes until they act. Available to read-only users.", false, map[string]any{
			"suggestion": map[string]any{"type": "string", "description": "The requested change in plain words.", "maxLength": 4000},
			"reason":     map[string]any{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"step_id":    map[string]any{"type": "string", "description": "Optional related workflow step ID.", "maxLength": 200},
		}, "suggestion")
		addRun("list_executions", "List the workflow's active executions: execution and session IDs, step, status, and run folder.", false, nil)
		addRun("list_schedules", "List the workflow's schedules plus current scheduler_state: global/product pause flags, individual blockers, observed time and relevant pause/resume history. Historical skipped runs are not current pause evidence; failed reads are unknown.", false, nil)
		p = page()
		p["schedule_id"] = externalString("Schedule ID from list_schedules.")
		p["offset"] = externalInteger(0, 1000000)
		p["compact"] = map[string]any{"type": "boolean", "description": "Default true: id, status, times, duration, error and run folder per run. Pass false for group lists, final responses and usage."}
		addRun("get_schedule_runs", "Page a schedule's retained run history with limit and offset, including after schedule deletion: status, duration, run folder, errors, and webhook deploy metadata when present. Workflow runs are kept for at least 90 days.", false, p, "schedule_id")
		addRun("trigger_schedule", "Trigger a schedule to run immediately, outside its normal timing. Requires the runs:execute scope.", true, map[string]any{"schedule_id": externalString("Schedule ID from list_schedules.")}, "schedule_id")
		addRun("chat", "Send a conversational message to the workflow assistant. Returns an inbox delivery receipt, without call_id or automatic answer capture. The agent explicitly chooses whether and what to send back; read messages action=read with inbox_id. Requires runs:execute.", true, map[string]any{"message": externalString("Message to send."), "inbox_id": externalString("Inbox from an earlier send to continue this external conversation."), "submission_id": externalString("Unique message submission ID; reuse after uncertain delivery.")}, "message")
		addRun("run_reply_input", "Answer a pending human-input request in a run session (see runs action=status pending_inputs). Requires the runs:execute scope.", true, map[string]any{"session_id": externalString("Run session ID from runs action=status."), "request_id": externalString("Pending input request ID from runs action=status."), "response": externalString("The answer to submit.")}, "session_id", "request_id", "response")
		// Stop commands execute directly instead of through the assistant
		// proxy: halting the wrong execution (or none) is not acceptable.
		addRun("stop_step", "Stop one running step or background execution by its execution ID from runs action=step, runs action=step_status, or runs action=active. You must have started its run, here or in the app.", true, map[string]any{"execution_id": externalString("Execution ID returned by a run tool or runs action=step_status."), "session_id": map[string]any{"type": "string", "description": "Optional run session ID; the execution must belong to it."}}, "execution_id")
		addRun("stop_all_executions", "Stop all running executions owned by this connection in the workflow, or one session you started (here or in the app) when session_id is given. Executes directly.", true, map[string]any{"session_id": map[string]any{"type": "string", "description": "Optional run session ID to stop instead of every owned session."}})
		// Crews: bounded by the token's Crew list; crews:read.
		crewID := func(props map[string]any) map[string]any {
			if props == nil {
				props = map[string]any{}
			}
			props["crew_id"] = externalString("Crew ID returned by workspace action=list.")
			return props
		}
		add("list_crews", "List the Crews this connection may use: ID, name, identity, owner. Requires crews:read.", false, false, map[string]any{"query": externalString("Filter by Crew name, identity, or ID.")})
		externalCrewCostsDefinition(add)
		add("get_crew", "Describe one Crew: identity, description, model, and its functions (typed entry points other Crews and connections can call). Requires crews:read.", false, false, crewID(nil), "crew_id")
		crewFiles := func(search bool) map[string]any {
			p := page()
			p["agent_id"] = externalString("Optional published agent ID from workspace get. Reads that agent's source and permitted results; imported chats stay private.")
			// An empty path is allowed (it means the Crew root); minLength 1 refused it and the root could not be listed.
			p["path"] = map[string]any{"type": "string", "description": "Crew-relative directory to start from. Omit, or pass \"\" or \".\", for the Crew root."}
			p["depth"] = externalInteger(1, 8)
			p["glob"] = externalString("Optional path glob relative to path; use it to find files by name, e.g. **/*ForgotPassword*. ** matches directories recursively.")
			if search {
				p["query"] = externalString("Text to find inside files; case-insensitive literal text unless match_case or regex is set.")
				externalTextSearchSchema(p)
			}
			return crewID(p)
		}
		add("list_crew_files", "List a Crew's project files (crew-relative paths), paginated. Start from a folder with path, go deeper with depth (up to 8), and find files by name with glob (e.g. **/*Login*). Private areas (chat transcripts under builder/, db/, the Crew's manifests, hidden folders) are never listed. Requires crews:read.", false, false, crewFiles(false), "crew_id")
		add("search_crew_files", "Search the text inside a Crew's project files for query (case-insensitive by default; match_case, whole_word and regex options), optionally limited to path and glob. Returns matching files and lines, paginated. Private areas are never searched. Requires crews:read.", false, false, crewFiles(true), "crew_id", "query")
		add("list_crew_snapshots", "List saved file comparisons for this Crew's turns. Pass session_id to list this authorized chat’s saved replies. history_scope=project returns separate checkout histories and exact snapshot IDs. Project entries include message/automation and agent labels only for chats you can read; incomplete captures are marked. Requires crews:read.", false, false, crewID(map[string]any{"history_scope": externalString("Set to project for shared file history across chats in this Crew you own; source labels require separate chat access."), "checkout_id": externalString("Optional checkout id returned by project history; project-files selects files outside Git. Restricts comparison or revert to that checkout."), "session_id": externalString("Optional chat session to list its saved replies.")}), "crew_id")
		add("compare_crew_snapshot", "Compare saved Studio file changes. Pass session_id for changes since the last review, or the whole chat before any review; snapshot_id selects one reply. Without session_id, snapshot_id selects one Crew turn. Missing initial capture is reported as unavailable. Optional file returns a diff; side (before/after) returns saved bytes. With session_id, file, side and line, return a validated historical line context for an ordinary message to that same chat. through_snapshot_id pins the shown end boundary. Use a returned change_key with file to open one exact saved diff when that file has several. Current chat and file access apply. No undo or GitHub approval.", false, false, crewID(map[string]any{"history_scope": externalString("Set to project for shared file history across chats in this Crew you own; source labels require separate chat access."), "checkout_id": externalString("Optional checkout id returned by project history; project-files selects files outside Git. Restricts comparison or revert to that checkout."), "group_by": externalString("Set to repository (with snapshot_id) to group one reply's changes by Git checkout, with branch, edited files and pull request status."), "view": externalString("Set to turn with history_scope=project and snapshot_id to read the exact original turn; rechecks chat and file access. Does not resume it."), "after_sequence": externalString("Optional next_sequence returned by view=turn."), "snapshot_id": externalString("Optional saved reply ID."), "session_id": externalString("Chat session to compare; omit snapshot_id for all saved changes."), "through_snapshot_id": externalString("Optional end snapshot ID returned by this chat comparison."), "baseline_snapshot_id": externalString("Pinned baseline_snapshot_id returned by a chat comparison, including start."), "review_id": externalString("Optional review point ID to see changes accepted at that review."), "offset": externalString("Optional next_offset returned by a paged chat comparison."), "file": externalString("Optional path returned by this comparison."), "change_key": externalString("Exact saved diff key returned with a file or one of its edits."), "side": externalString("before or after, with file, to read its saved contents or line."), "line": externalString("Optional positive line number, with session_id, file and side, to validate a comment anchor.")}), "crew_id")
		add("review_crew_snapshot", "Mark the displayed saved reply as reviewed in a Studio chat you can write in. Future changes compare from that exact saved state. No Git commit. Requires crews:run and current chat control.", true, false, crewID(map[string]any{"session_id": externalString("Chat session from the comparison."), "snapshot_id": externalString("Exact displayed snapshot_id to mark as reviewed.")}), "crew_id", "session_id", "snapshot_id")
		add("undo_crew_reply", "Revert a saved project comparison to its captured before state. Other chats or editors may have contributed. With checkout_id, only that checkout is affected. Call without apply to preview (restore list and skipped files). Files changed again after the reply are skipped, never overwritten; added files are deleted only if unchanged since, deleted files are recreated. apply=true does it and saves the undo as a new reviewable change. No Git commit. Requires crews:write and being the Crew's owner.", true, false, crewID(map[string]any{"history_scope": externalString("Set to project for shared file history across chats in this Crew you own; includes no chat messages."), "checkout_id": externalString("Optional checkout id returned by project history; project-files selects files outside Git. Restricts comparison or revert to that checkout."), "session_id": externalString("Your current writable chat session, where the revert is recorded. Required also for project history."), "snapshot_id": externalString("Saved reply ID from list_crew_snapshots."), "apply": map[string]any{"type": "boolean", "description": "true to undo; omit or false to preview."}}), "crew_id", "session_id", "snapshot_id")
		gitRepo := externalString("Repository folder from files action=git_status (its root); empty or omitted for a repository at the Crew root.")
		add("crew_git_status", "List the Git repositories in a Crew's files with their branch and the files not committed yet (staged, unstaged and new). Read only; never sets up Git. Requires crews:read.", false, false, crewID(nil), "crew_id")
		add("crew_git_diff", "Show one file's changes not committed yet, compared with the repository's latest commit (a new file shows in full). Requires crews:read.", false, false, crewID(map[string]any{"repo": gitRepo, "file": externalString("File path inside the repository, from git_status.")}), "crew_id", "file")
		add("crew_git_branch_compare", "List what the current branch adds on top of its base, like a pull request's changed files. Without base the server compares with the pull request's target branch, else the default branch. Returns base_sha and head_sha to pass to git_branch_diff, and how many commits are not pushed yet. Requires crews:read.", false, false, crewID(map[string]any{"repo": gitRepo, "base": externalString("Optional branch to compare with, e.g. main or origin/main.")}), "crew_id")
		add("crew_git_branch_diff", "Show one file's changes between the base_sha and head_sha returned by git_branch_compare. Requires crews:read.", false, false, crewID(map[string]any{"repo": gitRepo, "file": externalString("File path inside the repository, from git_branch_compare."), "base_sha": externalString("base_sha from git_branch_compare."), "head_sha": externalString("head_sha from git_branch_compare.")}), "crew_id", "file", "base_sha", "head_sha")
		add("read_crew_file", "Read a Crew project file: text up to 2 MiB returns content and smaller binary files return content_base64. Larger files up to 11 MiB return size/revision metadata without content, for guarded replacement or authenticated download. Private areas and symlinks are refused. Requires crews:read.", false, false, crewID(map[string]any{"path": externalString("Crew-relative file path from files action=list."), "agent_id": externalString("Optional published agent ID from workspace get.")}), "crew_id", "path")
		// crews:run — the call runs as a turn in this user's own continuing
		// conversation with the Crew, never its main chat.
		submission := map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for one intended call; reuse it on an uncertain retry to get the original call_id, even after completion or restart."}
		add("ask_crew", "Send a conversational message to a Crew. "+crewAskModelSentence+" Returns inbox_id, conversation_id and message_id, not call_id. The Crew explicitly decides whether to reply; read messages action=read with inbox_id. Agent messaging off refuses incoming messages, including replies. Requires crews:run.", false, false, crewID(map[string]any{"chat_id": externalString("Optional chat_id from messages action=list_agents. Omit for the main Builder chat."), "message": externalString("Conversational message."), "inbox_id": externalString("Inbox from an earlier send; omit for a fresh external conversation."), "submission_id": submission}), "crew_id", "message")
		add("suggest_crew_change", "Suggest a change to a Crew you use but do not own (its purpose, instructions, skills, functions, schedules or output). The owner reviews it in the Crew's Suggestions view; nothing changes until they act. Requires crews:run.", false, false, crewID(map[string]any{
			"suggestion": map[string]any{"type": "string", "description": "The requested change in plain words.", "maxLength": 4000},
			"reason":     map[string]any{"type": "string", "description": "Optional short reason or example.", "maxLength": 4000},
			"about":      map[string]any{"type": "string", "description": "Optional part of the Crew it concerns, e.g. a function or schedule name.", "maxLength": 200},
		}), "crew_id", "suggestion")
		// Crew authoring (crews:write; owner-only edits). One spec shape
		// serves create_crew, export_crew and import_crew.
		specProps, fnSpec, scheduleSpec := externalCrewSpecSchemas()
		add("create_crew", "Create a Crew you own from a spec: name, icon, purpose (what the Crew is for; its standing instructions), skills, functions, schedules, project files. Returns the new Crew as workspace action=get does. Requires crews:write on a connection covering all your Crews.", false, false, specProps, "name", "purpose")
		strList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		add("update_crew", "Edit a Crew you own. Every field is optional; omitted parts stay as they are. skills: set/add/remove names. functions: upsert specs / delete names. schedules: add specs (an add whose name already exists counts as already added), update specs by id, remove ids (an id that is already gone counts as removed), so a retry after a dropped connection is safe. files: write project files; remove_files deletes them. Requires crews:write.", false, false, crewID(map[string]any{
			"name": map[string]any{"type": "string", "maxLength": 60}, "icon": map[string]any{"type": "string", "maxLength": 8},
			"purpose":      map[string]any{"type": "string", "maxLength": crewPurposeLimit},
			"skills":       map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"set": strList, "add": strList, "remove": strList}},
			"functions":    map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"upsert": map[string]any{"type": "array", "items": fnSpec}, "delete": strList}},
			"schedules":    map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"add": map[string]any{"type": "array", "items": scheduleSpec}, "update": map[string]any{"type": "array", "items": scheduleSpec}, "remove": strList}},
			"files":        specProps["files"],
			"remove_files": strList,
			"return_spec":  map[string]any{"type": "boolean", "description": "Return the whole updated Crew (every function's instructions and schemas) instead of the compact summary. Default false."},
		}), "crew_id")
		add("write_crew_file", "Write one project file of a Crew: UTF-8 text (content) or a binary file such as an image, PDF or spreadsheet (content_base64, at most 11 MiB). Replaces the file unless expected_revision is given; request_id makes a retry safe. The Crew's manifests, functions.json, chats, databases and hidden folders are refused. With the agentworks CLI, crews put uploads a local file without pasting it into the call. The Crew's owner may write any editable path (needs crews:write outside shared/<your id>/); anyone else who can run the Crew (crews:run) may write only under shared/<your id>/ (at most 5 MiB a file, 50 MiB and 200 files in all), where the Crew and everyone with access to it can read it.", false, false, crewID(map[string]any{"path": externalString("Crew-relative file path, e.g. scripts/report.py."), "content": map[string]any{"type": "string", "description": "UTF-8 text, at most 2 MiB."}, "content_base64": externalString("Base64 of a binary file, at most 11 MiB decoded. Pass this or content."), "expected_revision": externalString("Optional: the file's revision (or missing for a new file); the write is refused if it changed."), "request_id": externalString("Optional unique ID (at most 128 characters); reuse it only to retry the identical write.")}), "crew_id", "path")
		add("export_crew", "Export a Crew as a portable spec (identity, skills with their project-local skill files, functions and schedules). Chats, memory, databases, secrets, and model connections are never included. Pass the result to workspace action=import on any AgentWorks server. Requires crews:read.", false, false, crewID(nil), "crew_id")
		importSpec := map[string]any{"type": "object", "properties": specProps, "required": []any{"name"}, "description": "A spec from workspace action=export (or a Crew Agent Playbook catalog entry)."}
		add("import_crew", "Create a Crew you own from a spec produced by workspace action=export. Schedules arrive disabled unless enable_schedules is true. Requires crews:write on a connection covering all your Crews.", false, false, map[string]any{"spec": importSpec, "enable_schedules": map[string]any{"type": "boolean"}}, "spec")
		// Vault management reuses the administrator builder and UI handlers.
		externalVaultDefinitions(add)
		// Code review (crews:review; admins and Code reviewers only).
		externalCodeReviewDefinitions(add)
		// Run your own Code (code:run; never a default scope).
		externalWatcherDefinitions(add)
		externalAgentEmailDefinitions(add)
		externalWorkspaceDefinitions(add)
		copyDef := projectCopyDefinition()
		defined = append(defined, copyDef)
		for i := range defined {
			if defined[i].Name == "workspace" {
				creation := projectCreateDefinition().InputSchema
				props := creation["properties"].(map[string]any)
				props["action"] = map[string]any{"type": "string", "enum": []any{"create"}}
				creation["required"] = append(creation["required"].([]any), "action")
				defined[i].InputSchema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"action": map[string]any{"const": "create"}}}, "then": creation}}
			}
		}
		externalLaptopAccessDefinition(add)
		externalComputerUseDefinition(add)
		externalMyVaultsDefinition(add)
		for i := range defined {
			if defined[i].Name == "manage_laptop_access" || isExternalLaptopFileTool(defined[i].Name) {
				defined[i].InputSchema["oneOf"] = []any{map[string]any{"required": []any{"workspace_id"}}, map[string]any{"required": []any{"crew_id"}}, map[string]any{"required": []any{"workflow_id"}}}
				if defined[i].Name == "manage_laptop_access" {
					defined[i].InputSchema["oneOf"] = append(defined[i].InputSchema["oneOf"].([]any), map[string]any{"properties": map[string]any{"action": map[string]any{"const": "devices"}}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []any{"workspace_id"}}, map[string]any{"required": []any{"crew_id"}}, map[string]any{"required": []any{"workflow_id"}}}}})
				}
			}
		}
		// Shared-account token limits (get: crews:review or users:manage; set: users:manage, admins).
		externalTokenLimitDefinitions(add)
		externalUsersDefinition(add)
		externalSignInAppsDefinition(add)
		externalCostsDefinition(add)
		externalBullpenAdminDefinition(add)
		externalBrainAdminDefinition(add)
		externalBuilderDefinitions(add)
		externalPlanMutationDefinition(add)
		if err := externalWorkflowCrewDefinition(add); err != nil {
			externalCatalogErr = err
			return
		}
		externalContractUpgradeDefinition(add)
		externalSettingsDefinitions(add)
		if err := externalWorkflowAccountDefinitions(add); err != nil {
			externalCatalogErr = err
			return
		}
		externalBrowserConnectionsDefinition(add)
		externalScheduleDefinitions(add)
		externalWorkflowTeamDefinition(add)
		externalTriggerDefinitions(add)
		dashboardToolDefinitions(add)
		externalDashboardAppDefinition(add)
		externalNeedsYouDefinitions(add)
		externalProjectDefinitions(add)
		externalCrewChatDefinitions(add)
		if err := externalCrewCommandDefinition(add); err != nil {
			externalCatalogErr = err
			return
		}
		if err := externalCrewCapabilityDefinitions(add); err != nil {
			externalCatalogErr = err
			return
		}
		externalCrewFilesDefinition(add)
		externalCrewDiagnosticsDefinition(add)
		externalCrewOwnersDefinition(add)
		if err := externalCrewExecutionDefinition(add); err != nil {
			externalCatalogErr = err
			return
		}
		externalDatabaseDefinitions(add)
		externalAfterRunDefinitions(add)
		externalMessagingDefinitions(add)
		creatorSchema := workflowCreatorToolSchema()
		// Normalize Go slices to JSON values for the schema compiler.
		creatorJSON, err := json.Marshal(creatorSchema)
		if err != nil {
			externalCatalogErr = err
			return
		}
		var creatorInput map[string]any
		if err := json.Unmarshal(creatorJSON, &creatorInput); err != nil {
			externalCatalogErr = err
			return
		}
		creatorInput["additionalProperties"] = false
		defined = append(defined, externalTool{Name: "create_workflow", Description: externalWorkflowCreatorDescription, InputSchema: creatorInput, mutates: true})
		externalRelayDefinitions(add)
		// Dispatch validates against the full external surface; discovery narrows
		// it per connection and live action/Owner/admin checks still authorize it.
		// A content-only schema here would reject advertised setup/access/Git
		// actions before they reach those permission checks.
		for _, def := range knowledgebase.ExternalConnectionToolDefinitions(true, true, true) {
			// The compiler accepts JSON values, rather than Go-specific slices.
			encoded, err := json.Marshal(def.InputSchema)
			if err != nil {
				externalCatalogErr = err
				return
			}
			var schema map[string]any
			if err = json.Unmarshal(encoded, &schema); err != nil {
				externalCatalogErr = err
				return
			}
			if schema["required"] == nil {
				delete(schema, "required")
			}
			defined = append(defined, externalTool{Name: def.Name, Description: def.Description, InputSchema: schema, mutates: def.Mutates})
		}
		// Membership comes from product.yaml's run mode: external_tools
		// first, in yaml order, then every run.tools name (the single
		// source of truth for the run surface) that has no native
		// implementation above, proxied to a pinned Run-mode session in
		// yaml order. Go defines implementations (schemas, dispatch);
		// yaml admits them. Both mismatch directions fail here so drift
		// between the two can never ship silently.
		for i := range defined {
			if defined[i].Name == "project_functions_get_call" {
				props := defined[i].InputSchema["properties"].(map[string]any)
				props["after"] = externalInteger(-1, 1000000000)
				props["after_event"] = externalInteger(-1, 1000000000)
				props["message_limit"] = externalInteger(1, 100)
				props["file"] = externalString("Private output file name listed on this call; reads return a bounded file page.")
				props["offset"] = externalInteger(0, 1000000000)
				props["limit"] = externalInteger(1, 2<<20)
			}
		}
		byName := make(map[string]externalTool, len(defined))
		for _, tool := range defined {
			byName[tool.Name] = tool
		}
		denied := make(map[string]bool)
		for _, name := range agentworksproduct.RunExternalDenylist() {
			denied[name] = true
		}
		relayTools, err := relayproduct.BuilderExternalTools()
		if err != nil {
			externalCatalogErr = err
			return
		}
		admitted := append(agentworksproduct.RunExternalTools(), agentworksproduct.BuilderExternalTools()...)
		admitted = append(admitted, relayTools...)
		crewTools, err := workproduct.BuilderExternalTools()
		if err != nil {
			externalCatalogErr = err
			return
		}
		admitted = append(admitted, crewTools...)
		kbTools, err := knowledgebaseproduct.ExternalTools()
		if err != nil {
			externalCatalogErr = err
			return
		}
		admitted = append(admitted, kbTools...)
		admitted = append(admitted, caplayerproduct.ExternalTools()...)
		seen := make(map[string]bool, len(admitted))
		for _, name := range admitted {
			if seen[name] {
				continue
			}
			if denied[name] {
				externalCatalogErr = fmt.Errorf("product.yaml both admits and withholds external tool %q", name)
				return
			}
			tool, ok := byName[name]
			if !ok {
				externalCatalogErr = fmt.Errorf("product.yaml admits unknown external tool %q", name)
				return
			}
			seen[name] = true
			externalCatalog = append(externalCatalog, tool)
		}
		runNames := agentworksproduct.RunTools()
		runSet := make(map[string]bool, len(runNames))
		for _, name := range runNames {
			runSet[name] = true
			if denied[name] {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			if native, ok := byName[name]; ok {
				externalCatalog = append(externalCatalog, native)
				continue
			}
			externalCatalog = append(externalCatalog, externalRunProxyTool(name))
		}
		for _, tool := range defined {
			if !seen[tool.Name] && !runSet[tool.Name] {
				externalCatalogErr = fmt.Errorf("external tool %q is implemented but admitted by neither product.yaml list", tool.Name)
				return
			}
		}
		if externalCatalog, err = mergeExternalTools(externalCatalog); err != nil {
			externalCatalogErr = err
			return
		}
		for i := range externalCatalog {
			tool := &externalCatalog[i]
			compiler := jsonschema.NewCompiler()
			uri := "https://agentworks.invalid/schemas/" + tool.Name
			if err := compiler.AddResource(uri, tool.InputSchema); err != nil {
				externalCatalogErr = err
				return
			}
			validator, err := compiler.Compile(uri)
			if err != nil {
				externalCatalogErr = err
				return
			}
			tool.validator = validator
		}
	})
	return externalCatalog, externalCatalogErr
}
func externalError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func externalJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (api *StreamingAPI) handleExternalTools(w http.ResponseWriter, r *http.Request) {
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	catalog, err := externalToolsForClaims(GetUserFromContext(r.Context()))
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	allowed := make([]externalTool, 0, len(catalog))
	for _, tool := range catalog {
		if externalTokenAllows(GetUserFromContext(r.Context()), tool) {
			allowed = append(allowed, externalMessageToolForRequest(r, knowledgebaseToolForClaims(GetUserFromContext(r.Context()), tool)))
		}
	}
	if names := r.URL.Query()["names"]; len(names) > 0 {
		result := externalMCPAPISpec(map[string]any{"names": names}, externalListedTools(GetUserFromContext(r.Context()), allowed), func(value map[string]any, names []string) {
			api.externalPrepareGuidance(r, value, r.URL.Query().Get("guidance_session_id"))
		})
		if result.IsError {
			externalError(w, 400, "invalid_arguments", "Requested schemas are not available to this connection.")
			return
		}
		externalJSON(w, result.StructuredContent)
		return
	}
	externalJSON(w, map[string]any{"tools": externalListedTools(GetUserFromContext(r.Context()), allowed)})
}

// externalCallRecorder keeps the status and the start of an error body, so a
// refused call leaves one log line naming the tool, never its arguments.
type externalCallRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
	size   int
}

func (rec *externalCallRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *externalCallRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status >= 400 {
		rec.size += len(b)
		if len(rec.body) < 512 {
			rec.body = append(rec.body, b[:min(len(b), 512-len(rec.body))]...)
		}
	}
	return rec.ResponseWriter.Write(b)
}

func (rec *externalCallRecorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (api *StreamingAPI) handleExternalCall(w http.ResponseWriter, r *http.Request) {
	api.dispatchExternalCall(w, r, false)
}

func (api *StreamingAPI) dispatchExternalCall(w http.ResponseWriter, r *http.Request, translated bool) {
	rec := &externalCallRecorder{ResponseWriter: w}
	w = rec
	var logTool, logAction string
	defer func() {
		if rec.status < 400 {
			return
		}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		// Only our own error envelope is logged; any other body (a tool or upstream
		// error could echo an argument back) is reported by size alone.
		if json.Unmarshal(rec.body, &envelope) != nil || envelope.Error.Code == "" {
			logfWithContext(api.httpRequestLogContext(r), "[EXTERNAL_CALL] tool=%s action=%s status=%d error=<non-envelope, %d bytes>", logTool, logAction, rec.status, rec.size)
			return
		}
		message := strings.Join(strings.Fields(envelope.Error.Message), " ")
		if len(message) > 240 {
			message = message[:240] + "…"
		}
		logfWithContext(api.httpRequestLogContext(r), "[EXTERNAL_CALL] tool=%s action=%s status=%d code=%s error=%q", logTool, logAction, rec.status, envelope.Error.Code, message)
	}()
	if GetUserFromContext(r.Context()) == nil {
		externalError(w, 401, "unauthorized", "Sign in to AgentWorks.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	var call struct {
		Name              string         `json:"name"`
		Arguments         map[string]any `json:"arguments"`
		GuidanceSessionID string         `json:"guidance_session_id,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		externalError(w, 400, "invalid_arguments", "Expected one JSON object.")
		return
	}
	if call.Arguments == nil {
		call.Arguments = map[string]any{}
	}
	logTool, logAction = call.Name, externalArg(call.Arguments, "action")
	if len(logTool) > 80 {
		logTool = logTool[:80]
	}
	if len(logAction) > 40 {
		logAction = logAction[:40]
	}
	if call.Name == "messages" {
		if err := authorizeAgentMailRequest(r, call.Arguments); err != nil {
			externalError(w, 403, "cli_required", err.Error())
			return
		}
	}
	if token := GetUserFromContext(r.Context()).AccessToken; token != nil && token.BoundAgentID != "" {
		if call.Name != "messages" || !slices.Contains([]string{"list_agents", "send", "read", "ack", "update_purpose", "ping_person", "get_file", "count", "delete", "clear"}, externalArg(call.Arguments, "action")) || externalArg(call.Arguments, "agent_name") != token.BoundAgentName {
			externalError(w, 403, "insufficient_scope", "This connection only supports messaging for its attached agent.")
			return
		}
	}
	catalog, err := externalToolsForClaims(GetUserFromContext(r.Context()))
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	var tool *externalTool
	for i := range catalog {
		if catalog[i].Name == call.Name {
			tool = &catalog[i]
			break
		}
	}
	if tool == nil || tool.internalOnly && !translated {
		externalError(w, 404, "unknown_tool", "Refresh the tool catalog with get_api_spec and choose its current action tool.")
		return
	}
	if tool.actions != nil {
		member, args, err := externalResolveMerged(*tool, call.Arguments)
		if err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		tool, call.Name, call.Arguments = &member, member.Name, args
	}
	if !externalTokenAllows(GetUserFromContext(r.Context()), *tool) {
		externalError(w, 403, "insufficient_scope", "This access token does not allow this operation."+externalMissingScopeHint(GetUserFromContext(r.Context()), *tool))
		return
	}
	guidanceTool, guidanceArgs := agentSourceCall(*tool, call.Arguments)
	if isExternalKnowledgebaseTool(guidanceTool.Name) && !knowledgebaseConnectionAllowsAction(GetUserFromContext(r.Context()), guidanceTool.Name, guidanceArgs) {
		externalError(w, 403, "insufficient_scope", "This connection does not allow the requested Brain action.")
		return
	}
	handle := call.GuidanceSessionID
	if handle == "" {
		handle, _ = r.Context().Value(externalGuidanceHandleKey{}).(string)
	}
	if len(handle) > 256 {
		handle = ""
	} // malformed receipt is missing, not authorization
	if externalOperationHasGuidance(guidanceTool, guidanceArgs) || externalProjectSkillFetch(guidanceTool.Name, guidanceArgs) || tool.Name == "get_guidance_topic" || tool.Name == "get_guidance_skills" {
		decorated, finish := api.externalGuidanceResponse(w, r, guidanceTool.Name, handle, guidanceArgs)
		w = decorated
		defer finish()
	}
	if err = tool.validator.Validate(call.Arguments); err != nil {
		externalError(w, 400, "invalid_arguments", err.Error())
		return
	}
	if tool.sparkquill {
		api.externalSparkQuillCall(w, r, tool.Name, call.Arguments)
		return
	}
	if _, ok := agentSourceAction(tool.Name); ok {
		resolved, args := agentSourceCall(*tool, call.Arguments)
		tool, call.Name, call.Arguments = &resolved, resolved.Name, args
	}
	if crewIntegrationMembers[tool.Name] {
		params := map[string]any{}
		for key, value := range call.Arguments {
			if key != "crew_id" {
				params[key] = value
			}
		}
		api.externalCrewExecutionCall(w, r, "crew_integrations", map[string]any{"crew_id": call.Arguments["crew_id"], "action": tool.Name, "arguments": params})
		return
	}
	if native := crewExecutionGroupedMembers[tool.Name]; native != "" {
		params := map[string]any{}
		for key, value := range call.Arguments {
			if key != "crew_id" {
				params[key] = value
			}
		}
		api.externalCrewExecutionCall(w, r, "crew_execution", map[string]any{"crew_id": call.Arguments["crew_id"], "action": native, "arguments": params})
		return
	}
	if source, operation := dataSourceAction(tool.Name); source != "" {
		resolved := *tool
		resolved.Name = source
		tool, call.Name = &resolved, source
		call.Arguments["action"] = operation
	}
	if tool.Name == "browser_connections" {
		api.externalBrowserConnectionsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "chat_preferences" {
		var update *bool
		if show, ok := call.Arguments["show_context_indicator"].(bool); ok {
			update = &show
		}
		api.chatPreferences(w, r, update)
		return
	}
	if tool.Name == "get_cost_breakdown" || tool.Name == "get_cost_detail" {
		call.Arguments["action"] = strings.TrimPrefix(tool.Name, "get_cost_")
		api.externalCostsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "messages" {
		api.externalAgentMessages(w, r, call.Arguments)
		return
	}
	if tool.Name == serverProductManagementTool {
		api.externalServerProductCall(w, r, call.Arguments)
		return
	}
	if isExternalVaultTool(tool.Name) {
		api.externalVaultCall(w, r, tool.Name, call.Arguments)
		return
	}
	if isExternalLaptopFileTool(tool.Name) {
		api.externalLaptopToolCall(w, r, tool.Name, call.Arguments)
		return
	}
	if tool.Name == "manage_laptop_access" {
		api.externalLaptopAccessCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "computer_use" {
		api.externalComputerUse(w, r, call.Arguments)
		return
	}
	if tool.Name == "manage_my_vaults" {
		api.externalMyVaultsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "workspace" {
		api.externalWorkspaceCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "manage_schedules" || tool.Name == "manage_triggers" || tool.Name == "manage_condition_triggers" {
		claims := GetUserFromContext(r.Context())
		target, err := resolveTriggerTarget(r.Context(), claims, externalArg(call.Arguments, "project"))
		if err != nil {
			externalError(w, 404, "not_found", "Project not found or not allowed for this connection.")
			return
		}
		if target.Kind == triggerCallerCrew {
			call.Arguments["crew_id"] = target.CrewID
		} else {
			call.Arguments["workflow_id"] = target.Manifest.ID
		}
	}
	if projectFunctionTool(tool.Name) {
		api.externalProjectFunctionCall(w, r, tool.Name, call.Arguments)
		return
	}
	if isExternalCrewTool(tool.Name) {
		api.externalCrewCall(w, r, tool.Name, call.Arguments)
		return
	}
	if tool.Name == "get_file_link" {
		if externalArg(call.Arguments, "crew_id") != "" {
			api.externalCrewAssetLink(w, r, call.Arguments)
			return
		}
		if token := GetUserFromContext(r.Context()).AccessToken; token != nil && !token.Allows("files:read") {
			externalError(w, 403, "insufficient_scope", "File read permission is required for workflow links.")
			return
		}
	}
	if tool.Name == "query_database" && externalArg(call.Arguments, "crew_id") != "" {
		claims := GetUserFromContext(r.Context())
		crewID := externalArg(call.Arguments, "crew_id")
		if externalArg(call.Arguments, "workflow_id") != "" {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		if t := claims.AccessToken; t != nil && (!t.Allows("crews:read") || !t.AllowsCrew(crewID)) {
			externalError(w, 403, "insufficient_scope", "This connection cannot read this Crew.")
			return
		}
		crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, crewID)
		if !ok {
			externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
			return
		}
		api.externalQueryDatabase(w, r, call.Arguments, crew.Binding.WorkspacePath)
		return
	}
	if tool.Name == "manage_messaging" && externalArg(call.Arguments, "crew_id") != "" {
		claims := GetUserFromContext(r.Context())
		crewID, action := externalArg(call.Arguments, "crew_id"), externalArg(call.Arguments, "action")
		if externalArg(call.Arguments, "workflow_id") != "" {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		if t := claims.AccessToken; t != nil {
			scope := "crews:write"
			if action == "status" || action == "whatsapp_link" {
				scope = "crews:read"
			}
			if !t.Allows(scope) || !t.AllowsCrew(crewID) {
				externalError(w, 403, "insufficient_scope", "This connection does not allow "+action+" on this Crew.")
				return
			}
		}
		crew, _, summary, ok := api.externalCrewResolve(r.Context(), claims, crewID)
		if !ok {
			externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
			return
		}
		label, _ := summary["name"].(string)
		api.externalMessagingCall(w, r, call.Arguments, externalMessagingTarget{kind: "crew", id: crewID, label: label, path: crew.Binding.WorkspacePath, profile: "work"})
		return
	}
	if action := tabModelMemberAction(tool.Name); action != "" {
		call.Arguments["action"] = action
		api.externalCrewChatsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "manage_crew_chats" {
		api.externalCrewChatsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "manage_project" {
		hasCrew, hasWorkflow := externalArg(call.Arguments, "crew_id") != "", externalArg(call.Arguments, "workflow_id") != ""
		if hasCrew == hasWorkflow {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		if hasCrew {
			api.externalCrewProjectCall(w, r, call.Arguments, externalArg(call.Arguments, "crew_id"))
			return
		}
	}
	if tool.Name == "manage_triggers" && externalArg(call.Arguments, "crew_id") != "" {
		if externalArg(call.Arguments, "workflow_id") != "" {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		api.externalCrewTriggerCall(w, r, call.Arguments, externalArg(call.Arguments, "crew_id"))
		return
	}
	if tool.Name == "manage_schedules" {
		hasCrew, hasWorkflow := externalArg(call.Arguments, "crew_id") != "", externalArg(call.Arguments, "workflow_id") != ""
		if hasCrew == hasWorkflow {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		if hasCrew {
			if _, _, _, ok := api.externalCrewResolve(r.Context(), GetUserFromContext(r.Context()), externalArg(call.Arguments, "crew_id")); !ok {
				externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
				return
			}
			api.externalScheduleCall(w, r, call.Arguments, externalScheduleTarget{crewID: externalArg(call.Arguments, "crew_id")})
			return
		}
	}
	if tool.Name == "open_dashboard" {
		result := api.openDashboardApp(r, call.Arguments)
		if result.IsError {
			externalError(w, 404, "dashboard_unavailable", "This dashboard is unavailable. Open an accessible shared dashboard.")
			return
		}
		externalJSON(w, result.StructuredContent)
		return
	}
	if isDashboardTool(tool.Name) {
		api.externalDashboardCall(w, r, tool.Name, call.Arguments)
		return
	}
	if isExternalSettingsTool(tool.Name) {

		hasCrew, hasWorkflow := externalArg(call.Arguments, "crew_id") != "", externalArg(call.Arguments, "workflow_id") != ""
		if hasCrew == hasWorkflow {
			externalError(w, 400, "invalid_arguments", "Pass exactly one of workflow_id or crew_id.")
			return
		}
		if hasCrew {
			api.externalCrewSettingsCall(w, r, tool.Name, call.Arguments)
			return
		}
	}
	if tool.Name == brainAdminTool {
		api.externalBrainAdminCall(w, r, call.Arguments)
		return
	}
	if isExternalKnowledgebaseTool(tool.Name) {
		api.externalKnowledgebaseCall(w, r, tool.Name, call.Arguments)
		return
	}
	if isExternalCodeReviewTool(tool.Name) {
		api.externalCodeReviewCall(w, r, tool.Name, call.Arguments)
		return
	}
	if tool.Name == "agent_inbox" {
		api.externalAgentEmailCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "manage_condition_triggers" {
		api.externalWatcherCall(w, r, call.Arguments)
		return
	}
	if isExternalTokenLimitTool(tool.Name) {
		api.externalTokenLimitCall(w, r, tool.Name, call.Arguments)
		return
	}
	if tool.Name == externalSignInAppsTool {
		api.externalSignInAppsCall(w, r, call.Arguments)
		return
	}
	if tool.Name == externalUsersTool {
		api.externalUsersCall(w, r, call.Arguments)
		return
	}
	if tool.Name == bullpenAdminTool {
		api.externalBullpenAdminCall(w, r, call.Arguments)
		return
	}
	if tool.Name == "copy_project_files" {
		api.externalCopyProjectFiles(w, r, call.Arguments)
		return
	}
	if tool.Name == "create_workflow" {
		api.externalCreateWorkflow(w, r, call.Arguments)
		return
	}
	if tool.Name == "create_relay" {
		api.externalCreateRelay(w, r, call.Arguments)
		return
	}

	discovered, err := discoverWorkflowManifests(r.Context(), tool.Name != "get_secret_sources")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", err.Error())
		return
	}
	visible := filterWorkflowManifestsForUser(GetUserFromContext(r.Context()), discovered)
	if token := GetUserFromContext(r.Context()).AccessToken; token != nil {
		filtered := make([]DiscoveredWorkflow, 0, len(visible))
		for _, workflow := range visible {
			if workflow.Manifest != nil && token.AllowsWorkflow(workflow.Manifest.ID) {
				filtered = append(filtered, workflow)
			}
		}
		visible = filtered
	}
	args := call.Arguments
	// Global tools need no workflow. They run before workflow resolution so a
	// restricted token can still obtain guidance and context.
	switch tool.Name {
	case "list_needs_you", "answer_needs_you", "dismiss_needs_you":
		api.externalNeedsYou(w, r, tool.Name, args, visible)
		return
	case "get_agent_context":
		api.externalAgentContext(w, r, args, visible)
		return
	case "list_guidance_topics":
		api.externalGuidanceTopicList(w, r)
		return
	case "get_guidance_skills":
		api.externalGuidanceSkills(w, r, args)
		return
	case "get_guidance_topic":
		api.externalGuidanceTopicBody(w, r, args)
		return
	case "get_skill":
		externalJSON(w, map[string]any{"name": "agentworks", "install_path": "agentworks/SKILL.md", "content": buildHostedSkillMarkdown(getBaseURL(r))})
		return
	}
	if tool.Name == "list_workflows" {
		matches := make([]DiscoveredWorkflow, 0)
		query := strings.ToLower(externalArg(args, "query"))
		for _, item := range visible {
			if item.Manifest != nil && (query == "" || strings.Contains(strings.ToLower(item.Manifest.Label+" "+item.Manifest.ID), query)) {
				matches = append(matches, item)
			}
		}
		sort.Slice(matches, func(i, j int) bool { return matches[i].Manifest.ID < matches[j].Manifest.ID })
		start := min(externalInt(args, "offset", 0), len(matches))
		end := min(start+externalInt(args, "limit", 100), len(matches))
		if externalCompact(args) {
			rows := make([]map[string]any, 0, end-start)
			for _, item := range matches[start:end] {
				rows = append(rows, externalCompactWorkflow(item))
			}
			externalJSON(w, map[string]any{"workflows": rows, "total": len(matches), "next_offset": end, "has_more": end < len(matches), "note": "Compact list: use workflow action=get for one workflow's full manifest."})
			return
		}
		page := make([]DiscoveredWorkflow, 0, end-start)
		for _, item := range matches[start:end] {
			page = append(page, externalWorkflowView(item))
		}
		externalJSON(w, map[string]any{"workflows": page, "total": len(matches), "next_offset": end, "has_more": end < len(matches)})
		return
	}
	var selected *DiscoveredWorkflow
	for i := range visible {
		if visible[i].Manifest != nil && visible[i].Manifest.ID == externalArg(args, "workflow_id") {
			if selected != nil {
				externalError(w, 409, "ambiguous_workflow", "Duplicate workflow IDs must be resolved in AgentWorks.")
				return
			}
			selected = &visible[i]
		}
	}
	if selected == nil {
		externalError(w, 404, "workflow_not_found", "Workflow does not exist or is not accessible.")
		return
	}
	access := workflowAccessForManifest(GetUserFromContext(r.Context()), selected.Manifest)
	if tool.Name == "workflow_team" {
		api.externalWorkflowTeamCall(w, r, args, *selected)
		return
	}
	if tool.Name == "manage_triggers" {
		api.externalWorkflowTriggerCall(w, r, args, *selected, access)
		return
	}
	if tool.Name == "manage_project" {
		api.externalWorkflowProjectCall(w, r, args, *selected)
		return
	}
	if tool.Name == "manage_messaging" {
		if t := GetUserFromContext(r.Context()).AccessToken; t != nil && !t.Allows("workflows:read") && !t.Allows("runs:execute") {
			externalError(w, 403, "insufficient_scope", "This connection was not granted workflow access.")
			return
		}
		kind := "workflow"
		if selected.Manifest.Kind == "relay" {
			kind = "relay"
		}
		api.externalMessagingCall(w, r, args, externalMessagingTarget{kind: kind, id: selected.Manifest.ID, label: selected.Manifest.Label, path: selected.WorkspacePath, workflow: selected, manifest: selected.Manifest})
		return
	}
	if tool.Name == "query_database" {
		if t := GetUserFromContext(r.Context()).AccessToken; t != nil && !t.Allows("workflows:read") && !t.Allows("runs:execute") {
			externalError(w, 403, "insufficient_scope", "This connection cannot read workflows.")
			return
		}
		api.externalQueryDatabase(w, r, args, selected.WorkspacePath)
		return
	}

	if tool.Name == "manage_schedules" {
		if selected.Manifest.Kind == "relay" {
			externalError(w, 400, "relay_api_only", "Flows have no schedules; they run through function triggers.")
			return
		}
		api.externalScheduleCall(w, r, args, externalScheduleTarget{workflow: selected})
		return
	}
	if tool.mutates && access != WorkflowAccessOwner && access != WorkflowAccessWrite {
		externalError(w, 403, "forbidden", "Workflow write access is required.")
		return
	}
	if tool.Name == "run_after_run" {
		api.externalRunAfterRun(w, r, args, *selected)
		return
	}
	if isExternalWorkflowAccountTool(tool.Name) {
		api.externalWorkflowAccountCall(w, r, tool.Name, args, *selected)
		return
	}
	if isExternalSettingsTool(tool.Name) {
		api.externalSettingsCall(w, r, tool.Name, args, *selected, access)
		return
	}
	if isExternalRelayTool(tool.Name) {
		api.externalRelayCall(w, r, tool.Name, args, *selected)
		return
	}
	if selected.Manifest.Kind == "relay" {
		switch tool.Name {
		case "list_schedules", "create_schedule", "create_calendar_schedule", "update_schedule", "delete_schedule", "trigger_schedule", "get_schedule_runs":
			externalError(w, 400, "relay_api_only", "Flows use API function triggers. Use relay action=test or relay action=run and relay action=get_run.")
			return
		}
	}
	// Flow chat is Builder-only; direct API tools handle published execution.
	if selected.Manifest.Kind == "relay" && (tool.Name == "chat" || tool.Name == "project_functions_call" && externalArg(args, "function") == "ask") {
		externalError(w, 400, "relay_builder_only", "Use guarded files to edit Flow source, relay action=test for draft tests, or relay action=run for published versions. Builder chat can delegate edits when requested.")
		return
	}
	// Conversation access follows the builder runtime: workflow readers may
	// chat with its existing read-only tool policy, and control their own turns.
	if strings.HasPrefix(tool.Name, "builder_") {
		api.externalBuilderOperationCall(w, r, tool.Name, args, *selected)
		return
	}
	if tool.Name == "workflow_crews" {
		api.externalWorkflowCrewCall(w, r, args, *selected)
		return
	}
	if tool.Name == "plan" {
		api.externalPlanMutationCall(w, r, args, *selected)
		return
	}
	if tool.Name == "workflow_upgrade" {
		api.externalContractUpgradeCall(w, r, args, *selected)
		return
	}
	if tool.Name == "get_file_link" {
		api.externalAssetLink(w, r, *selected, externalArg(args, "path"))
		return
	}
	if tool.Name == "list_workflow_knowledge" {
		api.externalListKnowledge(w, r, *selected, args)
		return
	}
	if tool.Name == "read_workflow_knowledge" {
		api.externalReadKnowledge(w, r, *selected, args)
		return
	}
	if tool.Name == "get_workflow" {
		externalJSON(w, externalWorkflowView(*selected))
		return
	}
	if tool.Name == "list_step_code" {
		api.externalListStepCode(w, r, *selected, args)
		return
	}
	if tool.Name == "suggest_workflow_change" {
		str := func(key string) string { value, _ := args[key].(string); return value }
		input, err := submitWorkflowSuggestion(r.Context(), GetUserFromContext(r.Context()), selected.WorkspacePath, "", str("suggestion"), str("reason"), str("step_id"))
		if err != nil {
			externalError(w, 400, "suggestion_refused", err.Error())
			return
		}
		externalJSON(w, map[string]any{"status": "submitted_for_owner_review", "workflow_id": selected.Manifest.ID, "suggestion_id": input.ID})
		return
	}
	switch tool.Name {
	case "project_functions_list", "project_functions_call", "project_functions_get_call", "project_functions_reply":
		api.externalWorkflowFunctionCall(w, r, tool.Name, args, *selected, access)
		return
	}
	// Run operations dispatch before the workflow lock: proxy turns forward
	// to the asynchronous query runtime (which must never run under this
	// lock), and the status and schedule readers need no lock.
	switch tool.Name {
	case "run_status", "list_executions", "list_schedules", "get_schedule_runs", "trigger_schedule", "chat", "run_reply_input", "stop_step", "stop_all_executions":
		api.externalRunCall(w, r, tool.Name, args, *selected)
		return
	}
	if tool.executes {
		if selected.Manifest.Kind == "relay" {
			externalError(w, 400, "relay_builder_only", "Use relay action=test or relay action=run; Flow does not expose Run chat tools.")
			return
		}
		api.externalRunProxy(w, r, tool.Name, args, *selected)
		return
	}
	if tool.Name == "get_plan" {
		api.externalPlanCall(w, r, *selected, args)
		return
	}
	api.externalFileCall(w, r, tool.Name, args, *selected)
}
func externalArg(args map[string]any, name string) string { s, _ := args[name].(string); return s }
func externalInt(args map[string]any, name string, fallback int) int {
	switch n := args[name].(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return fallback
}

type externalUpstreamError struct {
	status  int
	message string
}

func (e *externalUpstreamError) Error() string { return e.message }
func externalFailure(w http.ResponseWriter, err error) {
	status := 502
	code := "workspace_error"
	var upstream *externalUpstreamError
	if errors.As(err, &upstream) {
		status = upstream.status
		switch status {
		case 409:
			code = "revision_conflict"
		case 403:
			code = "protected_path"
		case 400:
			code = "invalid_arguments"
		case 404:
			code = "not_found"
		case 413:
			code = "too_large"
		}
	} else if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
		code = "not_found"
	}
	externalError(w, status, code, err.Error())
}
func (api *StreamingAPI) externalFileCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, workflow DiscoveredWorkflow) {
	req := wf.Request{Root: workflow.WorkspacePath, Path: externalArg(args, "path"), Query: externalArg(args, "query"), Glob: externalArg(args, "glob"), Offset: externalInt(args, "offset", 0), Limit: externalInt(args, "limit", 100), Depth: externalInt(args, "depth", 0)}
	switch name {
	case "write_file":
		api.externalWriteFile(w, r, args, workflow)
		return
	case "read_file":
		req.Operation = "read"
	case "list_files":
		req.Operation = "list"
	case "search_files":
		req.Operation = "search"
		externalTextSearchArgs(&req, args)
	case "list_runs":
		req.Operation = "list"
		req.Path = "runs"
		req.Depth = 2
	case "get_run", "get_logs":
		folder, e := wf.CleanRelative(externalArg(args, "run_folder"))
		if e != nil {
			externalError(w, 400, "invalid_arguments", e.Error())
			return
		}
		req.Operation = "list"
		req.Path = path.Join("runs", folder)
		req.Depth = 4
		if name == "get_logs" {
			req.Path = path.Join(req.Path, "logs")
		}
	}
	// A Flow's published version keeps its production runs in its own
	// release folder, the one the app's version picker reads.
	if version := externalArg(args, "version"); version != "" {
		if workflow.Manifest.Kind != "relay" {
			externalError(w, 400, "invalid_arguments", "version applies to Flows only.")
			return
		}
		if access := workflowAccessForManifest(GetUserFromContext(r.Context()), workflow.Manifest); access != WorkflowAccessOwner && access != WorkflowAccessWrite {
			externalError(w, 403, "forbidden", "Production runs of a Flow need owner or editor access.")
			return
		}
		root := relayReleaseWorkspace(workflow.WorkspacePath, version)
		if draft, err := relayDraftWorkspaceForRelease(r.Context(), root); err != nil || draft != workflow.WorkspacePath {
			externalError(w, 404, "version_not_found", "No published version "+version+"; see relay action=releases.")
			return
		}
		req.Root = root
	}
	result, err := externalFileRequest(r.Context(), req)
	if err != nil {
		externalFailure(w, err)
		return
	}
	// File reads include the existing Share file viewer URL. get_run
	// also adds the matching schedule run's delivery metadata.
	if (req.Operation == "read" && result.Exists) || name == "get_run" {
		raw, _ := json.Marshal(result)
		var linked map[string]any
		_ = json.Unmarshal(raw, &linked)
		linked["workflow_id"] = workflow.Manifest.ID
		if req.Operation == "read" && result.Exists {
			linked["preview_url"] = sharedAssetURL(r, path.Join(workflow.WorkspacePath, result.Path))
		}
		if name == "get_run" {
			folder := strings.Split(externalArg(args, "run_folder"), "/")[0]
			if webhookFolderPattern.MatchString(folder) {
				runs, err := ReadScheduleRuns(r.Context(), req.Root)
				if err != nil {
					externalError(w, http.StatusBadGateway, "workspace_unavailable", "Webhook run history is unavailable.")
					return
				}
				for _, run := range runs {
					if run.RunFolder == folder && run.Webhook != nil {
						linked["schedule_run_id"] = run.ID
						linked["webhook"] = run.Webhook
						break
					}
				}
			}
		}
		externalJSON(w, linked)
		return
	}
	externalJSON(w, result)
}

var externalPlanPaths = []string{"planning/plan.json", "planning/step_config.json"}

func (api *StreamingAPI) externalPlanCall(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, args map[string]any) {
	artifacts := map[string]any{}
	var revisions strings.Builder
	for _, p := range externalPlanPaths {
		result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: p})
		if err != nil {
			externalFailure(w, err)
			return
		}
		revisions.WriteString(p + ":" + result.Revision + "\n")
		var value any
		if result.Exists {
			if err := json.Unmarshal([]byte(result.Content), &value); err != nil {
				externalFailure(w, fmt.Errorf("invalid JSON in %s: %w", p, err))
				return
			}
		}
		artifacts[p] = value
	}
	revision := wf.Revision([]byte(revisions.String()))
	plan, config := artifacts["planning/plan.json"], artifacts["planning/step_config.json"]
	if stepID := externalArg(args, "step_id"); stepID != "" {
		step := externalPlanFindStep(plan, stepID)
		if step == nil {
			externalError(w, 404, "not_found", "No step "+stepID+" in the plan; call workflow action=plan with view=outline for the step ids.")
			return
		}
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "revision": revision, "step_id": stepID, "step": step, "config": externalPlanConfigFor(config, stepID)})
		return
	}
	if externalArg(args, "view") == "outline" {
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "revision": revision, "outline": externalPlanOutline(plan, config)})
		return
	}
	// The plan is returned once: it used to be repeated inside artifacts, doubling the reply.
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "revision": revision, "plan": plan, "artifacts": map[string]any{"planning/step_config.json": config}})
}

// externalPlanFindStep returns the plan entry of a step: the first object with this id that also has a type.
func externalPlanFindStep(node any, id string) map[string]any {
	switch value := node.(type) {
	case map[string]any:
		if got, _ := value["id"].(string); got == id {
			if _, isStep := value["type"].(string); isStep {
				return value
			}
		}
		for _, child := range value {
			if found := externalPlanFindStep(child, id); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range value {
			if found := externalPlanFindStep(child, id); found != nil {
				return found
			}
		}
	}
	return nil
}

// externalPlanConfigFor returns a step's entry of step_config.json: the value under its id key, or the first object
// whose id is this step's. Nil when the configuration has none.
func externalPlanConfigFor(node any, id string) any {
	switch value := node.(type) {
	case map[string]any:
		if entry, ok := value[id]; ok {
			return entry
		}
		if got, _ := value["id"].(string); got == id {
			return value
		}
		for _, child := range value {
			if found := externalPlanConfigFor(child, id); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range value {
			if found := externalPlanConfigFor(child, id); found != nil {
				return found
			}
		}
	}
	return nil
}

// externalPlanOutline is the cheap view of a plan: every step's id, type, title and a short description, and the size
// in characters of each top-level section of the plan and of the step configuration.
func externalPlanOutline(plan, config any) map[string]any {
	steps := []map[string]any{}
	var walk func(node any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if id, _ := value["id"].(string); id != "" {
				if kind, isStep := value["type"].(string); isStep {
					entry := map[string]any{"id": id, "type": kind, "title": value["title"]}
					if description, _ := value["description"].(string); description != "" {
						if runes := []rune(description); len(runes) > 160 {
							description = string(runes[:160]) + "…"
						}
						entry["description"] = description
					}
					steps = append(steps, entry)
				}
			}
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(value[key])
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(plan)
	sizes := func(node any) map[string]int {
		out := map[string]int{}
		if object, ok := node.(map[string]any); ok {
			for key, child := range object {
				encoded, _ := json.Marshal(child)
				out[key] = len(encoded)
			}
		}
		return out
	}
	return map[string]any{"steps": steps, "plan_sections": sizes(plan), "config_sections": sizes(config),
		"next": "workflow action=plan with step_id=<id> reads one step and its configuration; without view or step_id returns everything."}
}

// externalWorkflowView is a workflow as an external client may see it: the manifest without the stored webhook
// secrets. They are encrypted, but the ciphertext is only ever needed by the server itself. The input is not modified.
func externalWorkflowView(item DiscoveredWorkflow) DiscoveredWorkflow {
	if item.Manifest == nil {
		return item
	}
	manifest := *item.Manifest
	manifest.Schedules = make([]WorkflowSchedule, len(item.Manifest.Schedules))
	copy(manifest.Schedules, item.Manifest.Schedules)
	for i := range manifest.Schedules {
		if webhook := manifest.Schedules[i].Webhook; webhook != nil {
			clean := *webhook
			clean.EncryptedSecret = ""
			manifest.Schedules[i].Webhook = &clean
		}
	}
	item.Manifest = &manifest
	return item
}
