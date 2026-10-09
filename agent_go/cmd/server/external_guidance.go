package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// externalGuidanceVersion identifies the served guidance profile. It is
// computed from the topic allowlist, mapping notes, and rendered canonical
// content, so cached clients detect a stale profile as soon as canonical
// guidance changes — no manual bump needed.
var externalGuidanceVersion = externalComputeGuidanceVersion()

func externalComputeGuidanceVersion() string {
	h := sha256.New()
	for _, topic := range externalGuidanceTopics {
		h.Write([]byte(topic.Name + "\n" + topic.Description + "\n" + topic.ExternalNote + "\n"))
		if _, body, err := externalGuidanceContent(topic.Name); err == nil {
			h.Write([]byte(body))
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type externalGuidanceTopic struct {
	Name         string
	Description  string
	ExternalNote string
}

// externalGuidanceTopics is the allowlist of builder-reference topics served
// to external agents. Every entry must name a referenceKinds topic whose
// content applies to the external tool surface; topics that document internal
// adapter tools (e.g. plan-editing-tools, which names create_plan/add_step
// rather than the external typed plan tools) are excluded, and topics that
// mix internal and external tools carry an ExternalNote with the mapping.
var externalGuidanceTopics = []externalGuidanceTopic{
	{Name: "dashboard-authoring", Description: "Dashboard MCP operations, revisioned bundles, live HTML data APIs, Python/JS scripts, validation, preview and authenticated team links."},
	{Name: "plan-change-impact", Description: "Plan-change impact analysis: trace and reconcile the blast radius across downstream steps, measurement, reports, db, learnings, and KB. Load before treating a plan change as done.", ExternalNote: "External mapping: read_skill, get_goal_metrics, mark_changelog_artifact_reviewed, and review-artifact-drift are not in your catalog. Do the combined compatibility check yourself with search_files/read_file and report dispositions in your reply. read_skill pointers to builder-reference files outside this topic list (measurement-plan, reporting-policy, stores) have no external equivalent; describe the needed change in your reply instead of attempting it. Never edit changelog files directly."},
	{Name: "plan-design", Description: "Plan-design playbook: step boundaries, step-type selection, context flow, validation/failure design, anti-patterns. Load when designing a new plan or restructuring one.", ExternalNote: "External mapping: read_skill, add_step, update_step, and update_step_config are not in your catalog; the readable reference surface is list_guidance_topics/get_guidance_topic. execute_step and run_full_workflow ARE in your catalog when the token allows runs:execute: validate a design by running it and polling run_status. If builder_chat is available, delegate plan edits to it and poll builder_status; otherwise describe or suggest the change."},
	{Name: "planning-steps", Description: "Workshop plan composition: take-action-by-default discipline, step-type selection, validation_schema requirements, forward-only context flow. Load before adding or editing plan steps.", ExternalNote: "External mapping: read_skill is not in your catalog; the readable reference surface is list_guidance_topics/get_guidance_topic."},
	{Name: "step-description", Description: "How to write an optimized step description and validation_schema: earn every word, let the schema name the output shape. Load before writing or editing any step description.", ExternalNote: "External mapping: get_step_prompts is not in your catalog; verify saved-run prompts through get_run/get_logs and file reads instead."},
	{Name: "step-config", Description: "Per-step config reference: store-access modes, locks, execution mode, model selection, validation_schema, skills, clearing fields. Load before tuning a step.", ExternalNote: "External mapping: update_step_config, add_step, update_step, change_step_type, and update_validation_schema are not direct external tools. Delegate edits through builder_chat only when it is available. read_skill, query_workflow_db, mutate_workflow_db, and other config tools named here are not either; describe the needed change in your reply instead of attempting it."},
	{Name: "skill-management", Description: "Skill lifecycle and attachment model: workflow-selected skills are discovery context only, per-step enabled_skills is the runtime attachment, learnings/_global/SKILL.md is shared know-how. Load before reasoning about skills.", ExternalNote: "External mapping: install_skill, import_skill, uninstall_skill, update_workflow_config, and update_step_config are not in your catalog. list_skills and search_skills ARE in your catalog when the token allows runs:execute; use list_workflow_knowledge to inspect wiring. Installs and changes are not exposed, so say so instead of attempting them."},
	{Name: "file-layout", Description: "Workspace file layout reference and path discipline."},
	{Name: "secure-share-links", Description: "Share existing workflow files and folders with authenticated links: path rules and the difference between access-controlled sharing and public publishing.", ExternalNote: "External mapping: get_file_link IS in your catalog; the dashboard tool (action=link) requires dashboards:read and returns authenticated URLs without starting a Run chat. manage_internet_share is not available. Use files download for local copies."},
}

// externalToolMutates reports whether a catalog tool performs mutations.
func externalToolMutates(name string, catalog []externalTool) bool {
	for i := range catalog {
		if catalog[i].Name == name {
			return catalog[i].mutates
		}
	}
	return false
}

func externalGuidanceTopicByName(name string) *externalGuidanceTopic {
	for i := range externalGuidanceTopics {
		if externalGuidanceTopics[i].Name == name {
			return &externalGuidanceTopics[i]
		}
	}
	return nil
}

// externalGuidanceContent renders one allowlisted topic through the canonical
// builder-reference renderer. It reuses the server's own materialization, so
// external guidance can never drift from the builder's copy.
func externalGuidanceContent(topic string) (description, body string, err error) {
	skills, err := guidance.MaterializeReferenceKindsAsSkills("workshop", []string{topic})
	if err != nil {
		return "", "", err
	}
	if len(skills) != 1 {
		return "", "", fmt.Errorf("topic %q not available in workshop mode", topic)
	}
	return skills[0].Description, skills[0].Content, nil
}

// externalAgentContext serves get_agent_context. It is a global tool: no
// workflow is required, but when workflow_id names a visible workflow the
// caller's access level on it is included.
func (api *StreamingAPI) externalAgentContext(w http.ResponseWriter, r *http.Request, args map[string]any, visible []DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	catalog, err := externalTools()
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	available := make([]string, 0, len(catalog))
	for _, tool := range catalog {
		if !tool.hidden && externalTokenAllows(claims, tool) {
			available = append(available, tool.Name)
		}
	}
	sort.Strings(available)
	out := map[string]any{
		"guidance_version": externalGuidanceVersion,
		"available_tools":  available,
		"preparation":      externalPreparation(claims),
	}
	if token := claims.AccessToken; token != nil {
		caps := map[string]any{"scopes": token.Scopes, "expires_at": token.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")}
		if token.AllWorkflows {
			caps["workflows"] = "all"
		} else {
			caps["workflows"] = token.WorkflowIDs
		}
		out["token_capabilities"] = caps
	} else {
		out["token_capabilities"] = "app_session"
	}
	if id := externalArg(args, "workflow_id"); id != "" {
		for i := range visible {
			if visible[i].Manifest != nil && visible[i].Manifest.ID == id {
				out["workflow_id"] = id
				role := workflowAccessForManifest(claims, visible[i].Manifest)
				out["role"] = string(role)
				// available_tools reflects token scopes only; effective_tools
				// additionally reflects the caller's role on this workflow, so
				// readers are not shown mutations the dispatch guard would deny.
				// Run tools stay: readers may run, which is what Run mode is for.
				if role != WorkflowAccessOwner && role != WorkflowAccessWrite {
					effective := make([]string, 0, len(available))
					for _, name := range available {
						if !externalToolMutates(name, catalog) {
							effective = append(effective, name)
						}
					}
					out["effective_tools"] = effective
				} else {
					out["effective_tools"] = available
				}
				break
			}
		}
		if _, ok := out["role"]; !ok {
			externalError(w, 404, "workflow_not_found", "Workflow does not exist or is not accessible.")
			return
		}
	}
	externalJSON(w, out)
}

// externalPreparation returns the checklist for this connection. The agent
// reads workflows, files, plans, runs, guidance, and knowledge, and answers
// from what it finds; Builder authoring requires a separate explicit grant. A token with runs:execute may
// additionally operate the run-mode tools in pinned Run-mode sessions.
func externalPreparation(claims *UserClaims) []string {
	steps := []string{
		"This connection reads and runs: tools read, and run-mode tools execute in pinned Run-mode sessions. Nothing creates, edits, or authors.",
		"Call list_workflows to discover workflow IDs; IDs are never filesystem paths.",
		"Load only the guidance topics relevant to the task; topic list via list_guidance_topics.",
		"Use get_file_link for preview/download URLs; links identify a file and never grant permission.",
		"Use files download (not read_file) for a local copy; downloads refuse to overwrite existing files.",
	}
	if claims != nil && claims.AccessToken != nil && claims.AccessToken.Allows("dashboards:read") {
		steps[0] = "Use only tools present in this connection’s catalog. Dashboards: dashboard action=list discovers accessible projects and published documents; action=get reads the selected source. Links require recipients to have current project access."
		if claims.AccessToken.Allows("dashboards:write") {
			steps = append(steps, "Create or update a dashboard draft, validate its revision, preview it when runs:execute is granted, then dashboard action=publish using expected_revision. action=restore republishes a previous published revision. Use {{dashboard_assets}} and {{dashboard_scripts}} for revision-scoped assets and scripts; styling follows the user’s preferences.")
		}
	}
	canRun := claims == nil || claims.AccessToken == nil || claims.AccessToken.Allows("runs:execute")
	if canRun {
		steps = append(steps, "To run: call a run-mode tool such as execute_step (its reply carries session_id), then poll run_status for completion. Read run evidence with list_runs, get_run, and get_logs.")
		steps = append(steps, "To chat: send questions with the chat tool and read replies with run_status; pass session_id to continue a conversation, and answer waiting human-input steps with run_reply_input.")
	}
	if claims != nil && externalTokenAllows(claims, externalTool{Name: "builder_chat"}) {
		steps[0] = "This connection can build selected workflows where you currently have write access: builder_chat continues your existing workflow chat. Poll builder_status and reply with builder_reply_input using the operation_id. Native shell/account tools are unavailable. Changes use the configured Builder model."
	}
	if claims != nil && claims.AccessToken != nil {
		steps = append(steps, "For Relays (kind=relay), use builder_chat for editing, test_relay for draft testing, publish_relay for an immutable version, and run_relay/get_relay_run for published execution. There is no Relay Run chat. create_relay requires explicit relays:write consent covering all accessible workflows.")
		steps = append(steps, "This token is restricted: unavailable tools are omitted from the tools list.")
	}
	return steps
}

// externalGuidanceTopicList serves list_guidance_topics.
func (api *StreamingAPI) externalGuidanceTopicList(w http.ResponseWriter, r *http.Request) {
	topics := make([]map[string]string, 0, len(externalGuidanceTopics))
	for _, topic := range externalGuidanceTopics {
		topics = append(topics, map[string]string{"name": topic.Name, "description": topic.Description})
	}
	externalJSON(w, map[string]any{"guidance_version": externalGuidanceVersion, "topics": topics})
}

// externalGuidanceTopicBody serves get_guidance_topic.
func (api *StreamingAPI) externalGuidanceTopicBody(w http.ResponseWriter, r *http.Request, args map[string]any) {
	name := externalArg(args, "topic")
	allowed := externalGuidanceTopicByName(name)
	if allowed == nil {
		externalError(w, 404, "unknown_topic", "Topic is not part of the external guidance profile. Use list_guidance_topics.")
		return
	}
	description, body, err := externalGuidanceContent(name)
	if err != nil {
		externalError(w, 500, "guidance_unavailable", err.Error())
		return
	}
	out := map[string]any{"guidance_version": externalGuidanceVersion, "topic": name, "description": description, "content": body}
	if allowed.ExternalNote != "" {
		out["external_note"] = allowed.ExternalNote
	}
	externalJSON(w, out)
}

// externalKnowledgePrefixes are the workflow-root-relative path prefixes
// servable through read_workflow_knowledge. Everything else — plan files,
// run state, ownership, private paths — stays behind read_file or typed
// tools. The workspace service enforces its own confinement on top.
var externalKnowledgePrefixes = []string{"learnings/", "knowledgebase/"}

// externalListKnowledge serves list_workflow_knowledge: learnings and KB
// inventory, workspace skill folders, and the skill wiring (workflow-selected
// skills plus per-step enabled_skills) for one workflow.
func (api *StreamingAPI) externalListKnowledge(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, args map[string]any) {
	out := map[string]any{"workflow_id": workflow.Manifest.ID}
	limit := externalInt(args, "limit", 200)
	offset := externalInt(args, "offset", 0)
	hasMore := false
	truncated := false
	for _, dir := range []string{"learnings", "knowledgebase"} {
		result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "list", Path: dir, Depth: 4, Limit: limit, Offset: offset})
		if err != nil {
			var upstream *externalUpstreamError
			if errors.As(err, &upstream) && upstream.status == 404 {
				out[dir] = []any{}
				continue
			}
			externalFailure(w, err)
			return
		}
		out[dir] = result.Entries
		hasMore = hasMore || result.NextOffset > 0
		truncated = truncated || result.Truncated
	}
	out["has_more"] = hasMore
	out["truncated"] = truncated
	if hasMore {
		out["next_offset"] = offset + limit
	}
	out["selected_skills"] = workflow.Manifest.Capabilities.SelectedSkills
	stepSkills := externalStepSkills(r.Context(), workflow)
	out["step_skills"] = stepSkills
	allowed := externalAllowedSkillFolders(workflow, stepSkills)
	folders, err := externalWorkspaceSkillFolders(r)
	if err != nil {
		// An unavailable skill catalog must not look like an empty workspace.
		out["warnings"] = []string{"skill catalog unavailable: " + err.Error()}
		folders = []string{}
	}
	visible := make([]string, 0, len(folders))
	for _, folder := range folders {
		if allowed[folder] {
			visible = append(visible, folder)
		}
	}
	out["workspace_skills"] = visible
	externalJSON(w, out)
}

// externalAllowedSkillFolders returns the workspace skill folders this
// workflow may expose: its selected skills plus every per-step enabled skill.
// Global skills are shared across workflows, so workflow visibility alone must
// not grant the whole catalog — a token scoped to one workflow would otherwise
// read unrelated skills through that workflow's ticket.
func externalAllowedSkillFolders(workflow DiscoveredWorkflow, stepSkills map[string][]string) map[string]bool {
	allowed := map[string]bool{}
	if workflow.Manifest != nil {
		for _, name := range workflow.Manifest.Capabilities.SelectedSkills {
			if externalSkillFolderOK(name) {
				allowed[name] = true
			}
		}
	}
	for _, names := range stepSkills {
		for _, name := range names {
			if externalSkillFolderOK(name) {
				allowed[name] = true
			}
		}
	}
	return allowed
}

// externalStepSkills extracts per-step enabled_skills from the workflow's
// step config without mutating anything. It unmarshals into the production
// StepConfigFile shape ({"steps": [{id, agent_configs: {enabled_skills}}]})
// so discovery can never drift from what the runner reads; validation is
// skipped because a malformed file must hide skills, not fail the call.
func externalStepSkills(ctx context.Context, workflow DiscoveredWorkflow) map[string][]string {
	out := map[string][]string{}
	result, err := externalFileRequest(ctx, wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: "planning/step_config.json"})
	if err != nil || !result.Exists {
		return out
	}
	var configFile step_based_workflow.StepConfigFile
	if err := json.Unmarshal([]byte(result.Content), &configFile); err != nil {
		return out
	}
	for _, step := range configFile.Steps {
		if step.ID == "" || step.AgentConfigs == nil {
			continue
		}
		for _, name := range step.AgentConfigs.EnabledSkills {
			if name != "" {
				out[step.ID] = append(out[step.ID], name)
			}
		}
	}
	return out
}

// externalWorkspaceSkillFolders lists workspace-root skill folders through the
// service-token-only shared-assets endpoint. The agent server authorizes the
// root; folder contents are served per folder by read_workflow_knowledge.
func externalWorkspaceSkillFolders(r *http.Request) ([]string, error) {
	response, err := sharedAssetRequest(r, "skills", ".", "list")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("catalog status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			// Field name must match workspace sharedAssetEntry's filepath tag;
			// covered by the non-empty skill discovery test.
			Path string `json:"filepath"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	folders := []string{}
	for _, item := range body.Data {
		if item.Type == "folder" && externalSkillFolderOK(item.Path) {
			folders = append(folders, item.Path)
		}
	}
	sort.Strings(folders)
	return folders, nil
}

// externalSkillFolderOK strictly validates a workspace skill folder name so a
// crafted knowledge path can never address outside skills/.
func externalSkillFolderOK(name string) bool {
	if name == "" || name == "." || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, c := range name {
		if !(c == '-' || c == '_' || c == '.' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')) {
			return false
		}
	}
	return true
}

// externalReadKnowledge serves read_workflow_knowledge. Paths under
// learnings/ or knowledgebase/ are read from the workflow root; paths under
// skills/ are read from the workspace-root skill catalog through the
// service-token-only shared-assets endpoint. Nothing else is addressable.
func (api *StreamingAPI) externalReadKnowledge(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, args map[string]any) {
	raw := externalArg(args, "path")
	clean, err := wf.CleanRelative(raw)
	if err != nil || clean == "." || clean != raw {
		externalError(w, 400, "invalid_arguments", "Path must be a clean workflow-relative path.")
		return
	}
	allowed := false
	for _, prefix := range append(externalKnowledgePrefixes, "skills/") {
		if strings.HasPrefix(clean, prefix) && len(clean) > len(prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		externalError(w, 403, "protected_path", "Only learnings/, knowledgebase/, and skills/ paths are readable through this tool.")
		return
	}
	if strings.HasPrefix(clean, "knowledgebase/") && workflow.Manifest.KnowledgebaseMode == "shared" {
		externalError(w, 403, "protected_path", "This workflow uses shared Brain MCP tools; its local knowledge archive is unavailable to agents.")
		return
	}
	if strings.HasPrefix(clean, "skills/") {
		allowed := externalAllowedSkillFolders(workflow, externalStepSkills(r.Context(), workflow))
		api.externalReadSkillFile(w, r, workflow, strings.TrimPrefix(clean, "skills/"), allowed)
		return
	}
	result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: clean})
	if err != nil {
		externalFailure(w, err)
		return
	}
	rawFile, _ := json.Marshal(result)
	var linked map[string]any
	_ = json.Unmarshal(rawFile, &linked)
	linked["workflow_id"] = workflow.Manifest.ID
	externalJSON(w, linked)
}

// externalReadSkillFile serves one file from a workspace-root skill folder.
// The folder name is strictly validated and the read is confined to skills/
// by the workspace service, so a crafted path cannot escape the catalog.
func (api *StreamingAPI) externalReadSkillFile(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, rest string, allowed map[string]bool) {
	slash := strings.Index(rest, "/")
	if slash < 0 {
		externalError(w, 400, "invalid_arguments", "Skill path must be skills/<folder>/<file>.")
		return
	}
	folder, file := rest[:slash], rest[slash+1:]
	if !externalSkillFolderOK(folder) || file == "" || strings.HasSuffix(file, "/") {
		externalError(w, 400, "invalid_arguments", "Skill path must be skills/<folder>/<file>.")
		return
	}
	if !allowed[folder] {
		externalError(w, 403, "forbidden", "This workflow does not use that skill. Only its selected and step-enabled skills are readable.")
		return
	}
	response, err := sharedAssetRequest(r, "skills", folder+"/"+file, "read")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 404:
		externalError(w, 404, "not_found", "Skill file does not exist.")
		return
	case 403:
		externalError(w, 403, "protected_path", "Skill file is not accessible.")
		return
	case 200:
		break
	default:
		externalError(w, 502, "workspace_error", "Skill catalog request failed.")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, wf.MaxFileBytes+1))
	if err != nil {
		externalError(w, 502, "workspace_error", err.Error())
		return
	}
	if len(raw) > wf.MaxFileBytes {
		externalError(w, 413, "too_large", "Skill file exceeds the 2 MiB read limit.")
		return
	}
	out := map[string]any{"workflow_id": workflow.Manifest.ID, "path": "skills/" + folder + "/" + file, "exists": true}
	if utf8.Valid(raw) {
		out["encoding"] = "utf-8"
		out["content"] = string(raw)
	} else {
		out["encoding"] = "base64"
		out["content"] = base64.StdEncoding.EncodeToString(raw)
	}
	out["revision"] = wf.Revision(raw)
	externalJSON(w, out)
}
