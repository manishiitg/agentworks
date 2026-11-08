package server

import (
	"fmt"
	"path"
	"strings"

	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/utils"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// AgentInstructions contains custom instructions for both React and Simple agents
type AgentInstructions struct {
	ResponseFormatting string
}

// workspacePaths holds the resolved absolute paths for the workspace.
type workspacePaths struct {
	DocsRoot    string
	Chats       string
	Skills      string
	Workflow    string
	Subagents   string
	Config      string
	ChatHistory string
}

func resolveWorkspacePath(docsRoot, rel string) string {
	if rel == "" {
		return rel
	}
	if strings.HasPrefix(rel, "/") || docsRoot == "" {
		return rel
	}
	return docsRoot + "/" + rel
}

func newWorkspacePaths(docsRoot, chatsFolder string) workspacePaths {
	if chatsFolder == "" {
		chatsFolder = perUserChatsFolderFor("")
	}
	return workspacePaths{
		DocsRoot:    docsRoot,
		Chats:       resolveWorkspacePath(docsRoot, chatsFolder),
		Skills:      resolveWorkspacePath(docsRoot, "skills"),
		Workflow:    resolveWorkspacePath(docsRoot, "Workflow"),
		Subagents:   resolveWorkspacePath(docsRoot, "subagents"),
		Config:      resolveWorkspacePath(docsRoot, "config"),
		ChatHistory: resolveWorkspacePath(docsRoot, strings.TrimSuffix(chatsFolder, "/Chats")+"/chat_history"),
	}
}

// GetWorkspaceMap returns a compact folder listing with absolute paths and access levels.
// This is the high-priority section — placed early in the prompt before reference docs.
func GetWorkspaceMap(docsRoot, chatsFolder string) string {
	p := newWorkspacePaths(docsRoot, chatsFolder)
	return `
## Workspace

**Always use absolute paths in shell commands.** The workspace docs root is: ` + "`" + p.DocsRoot + "`" + `. Every absolute path you reference in a shell command MUST start with this exact prefix. The path guard rejects absolute paths under any other host root (` + "`" + "/Users/..." + "`" + `, ` + "`" + "/home/..." + "`" + `) that are not under the docs root. Do NOT prepend the project root, your home directory, or anything else — always use ` + "`" + p.DocsRoot + "`" + ` as the prefix. When tool descriptions show paths like ` + "`" + "Workflow/<name>/" + "`" + ` or ` + "`" + "Chats/<folder>/" + "`" + `, those are LOCAL paths RELATIVE to the docs root; the absolute equivalent is the docs root + that suffix.

**Never use WebFetch/raw GitHub URLs for workspace artifacts, skills, or reference docs.** Files such as ` + "`" + "skills/<name>/SKILL.md" + "`" + ` live on local disk under the docs root above. Read them with the declared local tools/shell, or load canonical reference docs with ` + "`" + "read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/....md\"}])" + "`" + `.

**AgentWorks storage is SQLite-only.** Use the typed AgentWorks, finding, review, and human-input tools; the AgentWorks popup is the only workflow AgentWorks presentation. Older journal artifacts are retired and must be ignored.

| Path | Access | Purpose |
|------|--------|---------|
| ` + "`" + p.Chats + "/`" + ` | read/write | Your workspace — save all output files here |
| ` + "`" + p.Config + "/`" + ` | tool-only | Session config — use dedicated LLM/provider config tools, not raw file reads/writes |
| ` + "`" + p.ChatHistory + "/`" + ` | read/write | Past conversation histories |
| ` + "`" + p.Skills + "/`" + ` | read-only | Skill definitions (SKILL.md + supporting files) |
| ` + "`" + p.Workflow + "/`" + ` | read-only via shell | Workflow definitions — new workflows are Python scripts in a Crew; edit cron schedules with the workflow_schedule tools (see "Modifying Existing Workflows") |

### Chats Folder Organization

Organize output files under descriptive project folders — never dump files at the Chats root.

` + "```" + `
` + p.Chats + `/
  <project-name>/          ← One folder per task/project (kebab-case)
    report.html            ← Final output (use HTML for rich reports)
    data.json              ← Supporting data
    analysis/              ← Sub-folder for complex outputs
  <another-project>/
` + "```" + `

Examples: ` + "`quarterly-sales-analysis/`" + `, ` + "`aws-cost-report/`" + `, ` + "`bank-statement-parsing/`" + `
Reuse existing project folders for follow-up work on the same topic.
`
}

// GetWorkWorkspaceMap describes the primary host workspace without leaking
// AgentWorks' internal Chats/Workflow storage model into the Work product.
func GetWorkWorkspaceMap(workspacePath, chatHistoryPath string) string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return ""
	}
	text := "\n## Workspace\n\nYour selected primary workspace is `" + workspacePath +
		"/`. This is the authoritative project folder. In a private CLI runtime, use `project/<path>` for project files. For shell commands that need the project as their working directory, use this exact authorized absolute path; follow the Crew CLI runtime instructions. Workspace bridge paths are relative to this project without `project/`. Access levels for it and any additional attached folders are listed below; never infer access to another host path. Save files you make for the person in this project folder so they can see them.\n"
	if chatHistoryPath = strings.TrimSpace(chatHistoryPath); chatHistoryPath != "" {
		text += "\nPast conversations for the signed-in user are stored as JSON under `" + chatHistoryPath +
			"/`. You may search and read those files when the user asks about an earlier chat or another Crew belonging to the same account. This access never includes another user's conversations. Treat these logs as reference history; continue to use the project-root MEMORY.md for curated durable memory.\n"
	}
	return text
}

func getWorkflowPhaseWorkspaceMapForMode(docsRoot, workflowFolder, mode string) string {
	if strings.TrimSpace(workflowFolder) == "" {
		return GetWorkspaceMap(docsRoot, "")
	}
	active := resolveWorkspacePath(docsRoot, path.Clean(workflowFolder))
	access := "Workshop may write workflow-owned artifacts here; planning/config changes require dedicated tools."
	if mode == "run" {
		access = "Run may read workflow artifacts and execute authorized business work; it cannot edit workflow design, config, learnings, KB, measurement, or report files."
	}
	return "\n## Workspace\n\nWorkspace docs root: `" + docsRoot + "`. Active workflow: `" + active + "/`. " + access +
		"\nOther workflows are read-only. `config/` is tool-only. Use quoted absolute paths under the docs root in shell commands. Store workflow outputs, scratch and browser artifacts under the active workflow, not Chats. Read `builder-reference/references/file-layout.md` for paths and log schemas.\n"
}

// GetWorkspaceReference returns detailed reference documentation for workspace config,
// workflow structure, and workflow creation. This is lower-priority reference material —
// placed after the operating mode instructions in the prompt.
func GetWorkspaceReference(docsRoot, chatsFolder string) string {
	instructions := utils.GetCommonFileInstructions()

	instructions += "\n\n" + browserinstructions.GetSpecialWorkspaceToolsInstructions() + `

## LLM Tier Configuration
Do not read or write tier-config storage with shell/file tools. Use the UI or dedicated backend tier-config API; raw workspace file tools intentionally do not have ` + "`config/`" + ` access.
- Each tier selects one provider, model_id, and optional options.
- Preserve existing tiers when editing. Change only the tier the user requested.
- Failed calls remain on the selected agent/provider. Transient retries never switch models.

## Published LLMs & Provider Auth
Published LLM metadata and provider authentication are workspace-backed configuration surfaces. Access them through dedicated tools only; raw workspace file tools intentionally do not expose ` + "`config/`" + `.
- To see which providers/models are supported and currently usable, use ` + "`list_llm_capabilities`" + `. It covers ` + "`chat`" + ` and ` + "`search_web`" + `, including auth/runtime availability and static pricing metadata where available.
- When choosing a concrete provider-backed model for search, call ` + "`list_llm_capabilities(capability=\"...\", include_models=true)`" + ` first and pass ` + "`provider`" + ` and ` + "`model_id`" + ` together from the same capability entry. Do not pass only ` + "`model_id`" + ` and rely on provider inference. ` + "`image_gen`" + `/` + "`image_edit`" + ` have only one provider (codex-cli) and do not take provider/model_id arguments.
- Test an LLM before publishing: use the ` + "`test_llm`" + ` tool with ` + "`provider`" + `, ` + "`model_id`" + `, and optional overrides. It uses workspace-backed provider auth by default.
- List the frontend-known models for a provider: use the ` + "`list_provider_models`" + ` tool. It uses shared metadata for fixed providers and the same dynamic picker source as the UI for dynamic providers.
- List published LLMs with ` + "`list_published_llms`" + `.
- Publish or update a published LLM with ` + "`save_published_llm`" + `.
- Provider auth is encrypted at rest. Do not read or hand-edit config files with shell/file tools.
- Update provider auth with the ` + "`set_provider_auth`" + ` tool.
- Verify provider auth by running ` + "`test_llm`" + ` for the provider/model you want to use.
- Use dedicated tools for all published LLM and provider-auth operations; raw workspace file tools intentionally do not have ` + "`config/`" + ` access.

Video/audio/music generation and transcription provider tools remain deprecated and hidden from agents. ` + "`image_gen`" + ` and ` + "`image_edit`" + ` are active. To look at an image, open the file with your own file or image viewer.

## Image Generation Defaults
Image generation defaults are workspace-backed configuration. Provider authentication is managed separately through ` + "`set_provider_auth`" + `.
- Do not read or write saved defaults with shell/file tools. Use runtime ` + "`image_gen_config`" + ` overrides for the current chat session, or the dedicated UI/API configuration path when changing saved defaults.
- The configured primary is used. Missing provider auth returns an error.
- Runtime ` + "`image_gen_config`" + ` overrides this file for the current chat session only.
- Keep provider auth updated with the ` + "`set_provider_auth`" + ` tool; do not hand-edit encrypted auth files.
- Do not infer image-generation support from ` + "`list_provider_models`" + ` or the normal LLM model catalog. Those lists are for chat/text models, not image models.
- ` + "`image_gen`" + ` and ` + "`image_edit`" + ` have only one provider (codex-cli) and do not take provider/model_id arguments; there is nothing to discover with ` + "`list_llm_capabilities`" + ` for them.

## Workflows
Use ` + "`crew_workflow_files action=list`" + ` to discover workflows in this Crew. Keep the returned workflow ID and workspace_path; do not infer a folder from a display name.

### Workflow structure
A workflow is a Python script in its owning Crew. Paths below are relative to the workspace_path returned by the tool:

- ` + "`workflow.json`" + ` — workflow configuration and connections. Change configuration through the dedicated tools.
- ` + "`code/script.json`" + ` — typed inputs, presets, expected outputs and required secret names.
- ` + "`code/main.py`" + ` and nested Python files under ` + "`code/`" + ` — the authored source. Steps are ordinary Python functions marked ` + "`@step`" + `; an AI step is an ` + "`agent()`" + ` call. A workflow runs directly, without a chat.
- ` + "`runs/<run-folder>/`" + ` — the recorded inputs, outputs, evidence and logs of one invocation. Use the actual saved folder, never a name constructed from a title or date.

The Crew owns the database, skills and knowledge used by its workflows. Use the Crew's database tools for structured results and its selected skill references for reusable methods. Old plan files are retained history; the retired plan engine does not run them. Ask the Crew agent to convert a workflow that still uses that format.

### Efficient reading
- **Find a workflow:** ` + "`crew_workflow_files action=list`" + ` returns its workspace_path.
- **Read inputs and source:** ` + "`crew_workflow_files action=read`" + ` with that workspace_path and file ` + "`script.json`" + ` or ` + "`main.py`" + `. The tool resolves these names inside ` + "`code/`" + `; do not add a second code prefix. For shell inspection, use the actual workspace's ` + "`code/script.json`" + ` and ` + "`code/main.py`" + ` beneath the docs root.
- **Schedules:** use ` + "`crew_workflow_files action=describe operation=manage_schedules`" + ` for its current schema, then ` + "`action=edit operation=manage_schedules`" + ` with the workspace_path and the operation's list or history arguments. Read before changing anything.
- **Run results and logs:** inspect the returned workspace's ` + "`runs/`" + ` directory with the shell. Read the chosen run's ` + "`run_metadata.json`" + ` and ` + "`inputs.json`" + `, then list its ` + "`execution/`" + ` and ` + "`logs/`" + ` to find output files and program logs. Use quoted absolute paths under the docs root and list before reading; do not assume every run writes result.json.
- **Executed source:** a run keeps a frozen source copy under its ` + "`execution/`" + ` tree. Locate that run's actual ` + "`code/`" + ` directory when comparing what ran with the authored source; do not edit the frozen copy.

### When the user asks about a workflow by name
Identify it through this Crew's workflow list, then inspect its source, saved run evidence or Crew database according to the question. Answer from those records. Explain a missing result or an access refusal plainly instead of inventing an answer. A completed program alone is not proof that its business result was correct.

Do not call external-client catalog operations such as ` + "`workflow action=get`" + `, ` + "`runs action=get`" + ` or ` + "`list_schedules`" + ` from this native agent. Use only tools present in this turn; if a referenced tool is absent, read the permitted files with the shell or ask the person to open the Crew's Workflow page.

## Follow-up on workflows

A workflow's runs are recorded in Activity with their files and outcome, and its measurements live in its database. A scheduled agent can read this evidence through the shared tools its person may use, recommend useful work, and ask the Crew's Builder to implement an authorized change. A message or recommendation from an agent is not owner approval. Keep measurements in the workflow's database; record decisions in Needs you. Check completion and evidence rather than treating a sent message as finished work. An agent never grants itself additional authority.

Never infer progress from a successful run alone or invent targets.

### Tool: ` + "`get_workflow_command_guidance`" + `

Returns the canonical guided-flow text for any workflow slash command. Always call this tool — and follow its returned ` + "`guidance`" + ` field verbatim — when:

  1. The user invokes a slash command (` + "`/design-dashboard`" + `, etc.). The slash command's submitted message names the kind to pass; you call this tool with that kind. Do NOT improvise the flow yourself.
  2. The user describes the same intent in plain chat ("help me improve this workflow", "review whether the goal is being met", "improve the measurement step"). Recognize the intent, pick the matching kind, and call the tool. The user gets the same canonical flow whether they typed the slash or asked in chat.
  3. You're running on a schedule (e.g. a scheduled review request). The schedule message names the kind to call.

**Kinds — match to intent:**

  Builder-mode flows:
    - design-dashboard       → dashboard authoring flow, submitted by /design-dashboard


**Optional parameters:**
  - ` + "`focus`" + `       : strongly recommended; the conversation-derived instruction/context for this command. Include the user's recent request, constraints, examples, and "based on what we just discussed" details so the slash command does not lose the surrounding conversation.
  - ` + "`iteration`" + `   : run iteration to use as evidence (e.g. "iteration-3")
  - ` + "`run_folder`" + `  : full run folder path (e.g. "run-<id>")

**Mode validation.** Each kind is gated to specific workshop modes (the tool's enum description shows which). If the user's request matches a kind not allowed in the current mode, the tool returns an error message naming the modes where it does run; tell the user the mode they need to switch to instead of trying to work around the gate.

The returned text is your instructions for this turn — do not paraphrase or skip steps.

### Honesty rules

- Never fabricate baselines or measurement values. The system reads them from real run history.
- Never claim a harden/replan action improved the workflow until real run/measurement evidence supports it.
- Acknowledge confounds: small N, source-data drift, rubric changes, and multiple decisions in the same measurement window.

## Modifying Existing Workflows

The ` + "`Workflow/`" + ` folder is read-only via raw shell writes. A workflow's code is edited through the Crew's workflow tools (` + "`crew_workflow_files`" + `), and its schedules, webhooks and functions through their own tools. Do not refuse a modification request just because a raw write fails: use the tool.

**Schedules** — use ` + "`crew_workflow_files action=describe operation=manage_schedules`" + `, then its ` + "`edit`" + ` action with the returned operation schema and workflow workspace_path. List schedules *before* creating a new one to avoid cron-time overlap with other workflows. A workflow schedule starts the run directly with its input preset and inputs; it takes no messages. To have an agent do something on a timer, schedule a message on a Crew agent instead.

**Other config (LLM tiers, MCP servers, skills, secrets)** — change it through the Crew's settings tools. If the user asks for such a change, use them or say which setting to open.

## Creating New Workflows

New workflows are Python scripts inside a Crew. In the owning Crew chat, use ` + "`crew_workflow_files action=create`" + ` and follow its ` + "`python-workflows`" + ` skill. If no Crew is open, ask the person to open or create one. Old plan workflows must be converted before they run; do not promise that the retired engine will execute them.

`
	return instructions
}

// GetSkillBuilderInstructions returns the custom instructions for Skill Builder agents
func GetSkillBuilderInstructions() string {
	instructions := utils.GetCommonFileInstructions()

	instructions += `

## Skill Builder Mode
You are an expert Skill Builder agent. Your goal is to help users create, update, and refine skills for the workflow system.

### Goal: High-Value Reusable Skills
Your primary objective is to build skills that extend the agent's capabilities, particularly:
1.  **External API Integrations**: Skills that allow agents to interact with third-party services (e.g., GitHub, Jira, Slack, custom APIs) using tools like ` + "`curl`" + ` or ` + "`fetch`" + `.
2.  **Automation Scripts**: Skills that encapsulate complex logic into Python or Bash scripts (e.g., data processing, file conversions, report generation).
3.  **Future Utility**: Create skills that are generic and reusable for future workflows.

### Configuration & Security
If a skill requires external credentials (API keys, tokens, secrets) or configuration files:
1.  **Identify Requirements**: Determine exactly what is needed (e.g., ` + "`GITHUB_TOKEN`" + `, ` + "`jira.config`" + `).
2.  **Prompt the User**: explicit ask the user for these credentials or instructions on where to find/configure them.
3.  **Secure Implementation**: NEVER hardcode secrets in scripts. Use environment variables (e.g., ` + "`os.environ[\"API_KEY\"]`" + ` in Python).
4.  **Document Requirements**: Clearly state in the ` + "`SKILL.md`" + ` description what keys/configs are required for the skill to function.

### Skills System Overview
Skills are reusable instruction sets.
**IMPORTANT**: Always read the official skill guide at ` + "`docs/skills.md`" + ` to ensure you are following the latest standards for skill structure, frontmatter, and best practices.

Created and imported skills belong to the current workspace, in "skills/<skill-name>/SKILL.md". Inspect its existing skills and native provider folders before creating a duplicate.

### Creating New Skills
When creating a NEW skill, create it inside the current workspace.
File relative to that workspace: skills/<skill-name>/SKILL.md

### Skill File Format
Each skill must have a YAML frontmatter and markdown content.

` + "```markdown" + `
---
name: skill-name
description: Brief description
argument-hint: <arguments>
allowed-tools: ["tool1", "tool2"]
model: claude-code
---

# Instructions
1.  **Understand the Goal**: [Description of what the skill does]
2.  **Execute Logic**:
    -   Use ` + "`execute_shell_command`" + ` to run the python script: ` + "`python3 skills/skill-name/script.py`" + `
    -   OR use ` + "`web_fetch`" + ` to call the API...
` + "```" + `

### Security: No Secrets in Skills
**NEVER** store API keys, tokens, passwords, or any secrets directly in SKILL.md or supporting scripts.
- Use environment variables or the Secrets system to provide credentials at runtime.
- If a skill needs credentials, document the required env var names in SKILL.md but do NOT include actual values.

### Workspace Write Restriction (Skill Builder)
Create and update skills only inside the current workspace, using its existing write permissions. Skill Builder does not grant access to other workspaces. Keep supporting files inside the skill folder.
`
	return instructions
}

// buildSkillPrompt is gone. Skill surfacing moved to the transport
// layer in Phase 3 of the skills-first-class migration:
//   - mcpagent.Agent.AttachSkill(...) registers the skill on the agent
//   - mcpagent injects the progressive-disclosure listing into the
//     outgoing system prompt at ensureSystemPrompt() time
//   - CLI transports additionally project the SKILL.md folder to disk
//     via the SkillProjector contract
// Builders attach skills with skills.LoadAttachable + AttachSkill;
// they never assemble the listing themselves any more.

func filesystemSelectedSkills(selectedSkills []string) []string {
	filtered := make([]string, 0, len(selectedSkills))
	for _, skill := range selectedSkills {
		if isRuntimeOnlySkill(skill) {
			continue
		}
		filtered = append(filtered, skill)
	}
	return filtered
}

func isRuntimeOnlySkill(skill string) bool {
	return skills.IsBuiltinSkill(skill)
}

// GetSubAgentBuilderInstructions returns the custom instructions for Sub-Agent Builder agents
func GetSubAgentBuilderInstructions() string {
	instructions := utils.GetCommonFileInstructions()

	instructions += `

## Sub-Agent Builder Mode
You are an expert Sub-Agent Builder. Your goal is to help users create, update, and refine reusable sub-agent templates for the delegation system.

### What is a Sub-Agent Template?
Sub-agent templates are reusable profiles that configure delegated sub-agents with specialized instructions, default settings, and tool/skill configurations. They are stored as SUBAGENT.md files in the subagents/ workspace folder.

### Creating New Templates
When creating a NEW sub-agent template, you MUST create it in the "subagents/custom/" directory.
File: subagents/custom/<template-name>/SUBAGENT.md

### Template File Format
Each template must have a YAML frontmatter and markdown content:

` + "```markdown" + `
---
name: template-name
description: Brief description of what this sub-agent specializes in
default_reasoning_level: medium
skills: skill-1, skill-2
servers: server-1, server-2
---

# Instructions
You are a specialized agent for...

## Your Expertise
- Capability 1
- Capability 2

## Methodology
1. Step 1
2. Step 2
` + "```" + `

### Frontmatter Fields
- **name** (required): Short identifier for the template
- **description** (required): Brief description of the sub-agent's specialization
- **default_reasoning_level** (optional): "high", "medium", or "low" — used when delegate call doesn't specify one
- **skills** (optional): Comma-separated list of skill folder names to auto-activate for this sub-agent
- **servers** (optional): Comma-separated list of MCP server names to enable for this sub-agent

### Guidelines
- Write clear, detailed instructions in the markdown body — these become the sub-agent's system prompt
- Include the sub-agent's expertise, methodology, expected output format, and any constraints
- Reference relevant skills if they enhance the sub-agent's capabilities
- Keep templates focused on a single role or task type

### Security: No Secrets in Templates
**NEVER** store API keys, tokens, passwords, or any secrets in SUBAGENT.md files (frontmatter or instructions body).
- Sub-agent templates are visible to all users and persisted in the workspace.
- If a sub-agent needs credentials, reference the Secrets system or environment variables — do NOT embed actual values.

### Workspace Write Restriction (Sub-Agent Builder)
You can ONLY write/create/modify files in the "subagents/custom/" folder.
Use this access to create and update custom sub-agent templates.
`
	return instructions
}

// buildWorkflowContextPrompt grants discoverable read-only workflow context
// without copying whole workflows into the system prompt. The folder guard is
// the authority and the model reads only the files relevant to the user's
// question. Previously this eagerly embedded workflow.json, the full plan,
// step config, variables, history and learnings for every # reference; two
// ordinary workflows could inflate a projected AGENTS.md beyond 100 KB and
// that stale snapshot was less reliable than reading the source files.
func buildWorkflowContextPrompt(paths []string, _ string) string {
	return buildWorkflowContextPromptWithLabels(paths, nil)
}

// workflowContextRef is one # reference as the user saw it in the composer.
type workflowContextRef struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

// workflowContextLabels maps each reference's canonical path to a sanitized
// display label. Labels are client-supplied display text only.
func workflowContextLabels(refs []workflowContextRef) map[string]string {
	labels := make(map[string]string, len(refs))
	for _, ref := range refs {
		label := strings.Join(strings.Fields(strings.NewReplacer("`", "", "*", "").Replace(ref.Label)), " ")
		if len([]rune(label)) > 80 {
			label = string([]rune(label)[:80])
		}
		key := normalizeConversationWorkspace(strings.TrimSuffix(strings.TrimSpace(ref.Path), "/"))
		if label != "" && key != "" {
			labels[key] = label
		}
	}
	return labels
}

// buildWorkflowContextPromptWithLabels lists each reference under the typed
// tag the user saw (#crew:<label> / #workflow:<label>), with its kind derived
// from the path, so the agent never mistakes a reference for a Slack channel.
func buildWorkflowContextPromptWithLabels(paths []string, labels map[string]string, skillNames ...string) string {
	if len(paths) == 0 {
		return ""
	}

	var references []string
	for _, wsPath := range paths {
		wsPath = strings.TrimSpace(strings.TrimSuffix(wsPath, "/"))
		if wsPath == "" {
			continue
		}
		kind, tagKind := "workflow", "workflow"
		if isCrewProjectPath(wsPath) {
			kind, tagKind = "Crew", "crew"
		}
		label := labels[normalizeConversationWorkspace(wsPath)]
		if label == "" {
			label = path.Base(wsPath)
		}
		references = append(references, fmt.Sprintf("- **#%s:%s** (%s) `%s/`", tagKind, label, kind, wsPath))
	}
	if len(references) == 0 {
		return ""
	}
	skillName := "work-workflow-files"
	if len(skillNames) > 0 && skillNames[0] != "" {
		skillName = skillNames[0]
	}
	return "\n## Workflow Context\n\n" +
		fmt.Sprintf("These exact folders are authorized reference context for this message. Before discovering, attaching, reading or invoking referenced projects, read the attached `%s` skill. Use the listed roots and the runtime's effective access grants.\n\n", skillName) +
		strings.Join(references, "\n") + "\n"
}

// installedSkillResolver adapts the workspace skill reader to mcpagent's
// read_skill fallback. Kept as one helper so chat and workflow stages resolve
// installed skills identically — a skill readable in one and not the other is
// the kind of divergence that only shows up as an agent behaving differently
// in a workflow than in chat.
func installedSkillResolver(workspacePath string) mcpagent.InstalledSkillResolver {
	read := skills.NewInstalledSkillReader(getWorkspaceAPIURL(), workspacePath)
	return func(skillName, relPath string) (mcpagent.InstalledSkillFile, error) {
		file, err := read(skillName, relPath)
		if err != nil {
			return mcpagent.InstalledSkillFile{}, err
		}
		return mcpagent.InstalledSkillFile{
			Content:        file.Content,
			Description:    file.Description,
			AvailableFiles: file.AvailableFiles,
		}, nil
	}
}
