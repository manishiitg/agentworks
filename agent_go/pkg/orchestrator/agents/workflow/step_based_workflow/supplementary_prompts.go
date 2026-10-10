package step_based_workflow

import (
	"context"
	"fmt"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	browserinstructions "github.com/manishiitg/coding-agent-loop/agent_go/pkg/instructions"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// appendSupplementaryPrompts injects skills, secrets, and browser instructions
// into the agent's system prompt.
// This is the standard post-setup injection used by execution and todo-task agents.
func (hcpo *StepBasedWorkflowOrchestrator) appendSupplementaryPrompts(
	ctx context.Context,
	baseAgent *agents.BaseAgent,
	config *agents.OrchestratorAgentConfig,
	effectiveSkills []string,
	attachGlobalLearnings bool,
	registeredTools []string,
	scriptedStep bool,
) {
	var identitySkills []*llmtypes.Skill
	var supplements []string
	browserCfg := hcpo.resolveBrowserConfig(config.ServerNames, effectiveSkills)
	for _, toolName := range registeredTools {
		if toolName == "agent_browser" {
			browserCfg.HasAgentBrowser = true
			break
		}
	}
	effectiveSkills = skills.WithAgentBrowserCapability(effectiveSkills, browserCfg.HasAgentBrowser)
	// Every transport gets the same static AgentWorks reference identity.
	// Coding CLIs additionally project it to disk; API models read it through
	// mcpagent's intrinsic read_skill tool. The execution role deliberately
	// receives only the reference corpus, not workflow-commands.
	//
	// The corpus is selected by the tools this agent actually holds, not by a
	// workshop mode (PLAT-125). Step execution is not a workshop surface: it
	// previously borrowed the builder's mode and was handed 41 docs describing
	// tools it does not have, then acted on them.
	if workflowReference := workflowReferenceSkill(guidance.StepExecutionSignals{
		ToolNames:         registeredTools,
		CodeExecutionMode: config != nil && config.UseCodeExecutionMode,
		ScriptedStep:      scriptedStep,
	}); workflowReference != nil {
		identitySkills = append(identitySkills, workflowReference)
		hcpo.GetLogger().Info(fmt.Sprintf("📚 Attached workflow reference skill (%d supporting docs, from %d registered tools)", len(workflowReference.SupportingFiles), len(registeredTools)))
	}

	// 1. Skills — Phase 3 rewire. Load the step's selected skills as
	// first-class llmtypes.Skill values and attach to the agent.
	// mcpagent.ensureSystemPrompt injects the listing into the system
	// prompt; CLI adapters additionally project SKILL.md folders to
	// disk via the SkillProjector contract. No more manual
	// BuildWorkflowSkillPrompt + AppendSystemPrompt.
	// Set unconditionally: a stage with no attached skills can still be told by
	// its description to read one, and stages must resolve installed skills the
	// same way chat does or an agent behaves differently in a workflow.
	if baseAgent != nil && baseAgent.Agent() != nil {
		baseAgent.SetInstalledSkillResolver(installedWorkflowSkillResolver(hcpo.GetWorkspacePath()))
	}
	if len(effectiveSkills) > 0 {
		if attached := skills.LoadAttachableIn(getWorkspaceAPIURL(), hcpo.GetWorkspacePath(), effectiveSkills); len(attached) > 0 {
			identitySkills = append(identitySkills, attached...)
			attachedNames := make([]string, 0, len(attached))
			for _, skill := range attached {
				attachedNames = append(attachedNames, skill.Name)
			}
			// Log what attached, not what was asked for. Printing the request
			// beside the count read as "attached 2: [seven names]" and hid the
			// five failures behind a line that looked like success.
			hcpo.GetLogger().Info(fmt.Sprintf("🎯 Attached %d of %d step skill(s): %v", len(attached), len(effectiveSkills), attachedNames))
		}
	}

	// 1b. Workflow global skill (Phase 4): attach a small pointer
	// skill telling the agent to read learnings/_global/ in the
	// workflow folder when it needs the workflow's accumulated
	// know-how. We attach a pointer (not the full body + references/)
	// so the workspace files stay the single source of truth and the
	// projected skill doesn't drift from what the workflow has
	// learned mid-session.
	//
	// Same helper the workshop chat uses (server.go workshop-phase
	// setup) — both paths land the identical pointer skill.
	if attachGlobalLearnings {
		if globalSkill := skills.LoadGlobalSkill(getWorkspaceAPIURL(), hcpo.GetWorkspacePath()); globalSkill != nil {
			identitySkills = append(identitySkills, globalSkill)
			hcpo.GetLogger().Info("🌐 Attached workflow global skill pointer (_global → learnings/_global/)")
		}
	}

	// 3. Secrets
	effectiveSecrets := GetEffectiveSecrets(hcpo.BaseOrchestrator)
	if len(effectiveSecrets) > 0 {
		secretPrompt := BuildWorkflowSecretPrompt(effectiveSecrets)
		if secretPrompt != "" {
			supplements = append(supplements, secretPrompt)
			hcpo.GetLogger().Info(fmt.Sprintf("🔐 Added secret prompt to agent (%d secrets)", len(effectiveSecrets)))
		}
	}

	// 4. Browser instructions (mode-specific)
	browserPrompt := browserinstructions.BuildBrowserInstructions(browserCfg)
	if isCodingCLIConfig(config) {
		browserPrompt = browserinstructions.BuildBrowserRuntimeInstructions(browserCfg)
	}
	if browserPrompt != "" {
		supplements = append(supplements, browserPrompt)
		hcpo.GetLogger().Info(fmt.Sprintf("🌐 Added browser instructions to agent (agent-browser=%v, cdp=%v)",
			browserCfg.HasAgentBrowser, browserCfg.CdpPort > 0))
	}

	// 4b. Workflow-specific browser downloads guidance.
	// Generic browser instructions mention logical Downloads/ for normal chat uploads, but
	// workflow runs must stay inside their run-scoped execution/Downloads folder.
	if browserDownloadsPath := hcpo.GetBrowserDownloadsPath(); browserDownloadsPath != "" {
		downloadsPrompt := fmt.Sprintf(
			"## Workflow Browser Downloads\nFor this workflow run, use the run-scoped downloads folder %q for browser downloads and file cleanup. Do not read from, write to, or delete files under the root workspace Downloads/ folder.",
			browserDownloadsPath,
		)
		if hostDownloads := common.CDPHostDownloadsPath(browserCfg.Mode); hostDownloads != "" {
			downloadsPrompt += fmt.Sprintf(" In CDP mode, the host Downloads folder %q is also available for reading and writing so browser files can be staged or retrieved there. Prefer %q for run-owned artifacts, and do not modify unrelated files in host Downloads.", hostDownloads, browserDownloadsPath)
		}
		supplements = append(supplements, downloadsPrompt)
		hcpo.GetLogger().Info(fmt.Sprintf("🌐 Added workflow browser downloads guidance to agent: %s", browserDownloadsPath))
	}
	sharedKBPrompt := ""
	if session := common.GetSessionShellConfig(config.MCPSessionID); session != nil && session.Env["WORKFLOW_KB_ACCESS"] == "read" {
		sharedKBPrompt = workflowKnowledgebaseSourcesPrompt(hcpo.GetWorkspacePath())
	}
	if attachedFolders := workflowFolderAccessPrompt(hcpo.GetWorkspacePath()) + "\n" + sharedKBPrompt; strings.TrimSpace(attachedFolders) != "" {
		supplements = append(supplements, attachedFolders)
	}
	if section := testModePromptSection(hcpo.activeTestRun()); section != "" {
		supplements = append(supplements, section)
	}
	if err := baseAgent.ApplyIdentity(ctx, identitySkills, supplements...); err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to apply supplementary agent identity: %v", err))
	}

}

func isCodingCLIConfig(config *agents.OrchestratorAgentConfig) bool {
	return config != nil && common.IsCLIProvider(config.LLMConfig.Primary.Provider)
}

func usesProjectedReferenceSkills(config *agents.OrchestratorAgentConfig, templateVars map[string]string) bool {
	if raw, ok := templateVars["UseProjectedReferenceSkills"]; ok {
		return raw == "true"
	}
	// The variable name is retained for template compatibility. Reference
	// skills are attached on every transport; only native disk projection is
	// CLI-specific, while read_skill is universal.
	_ = config
	return true
}

func workflowReferenceSkill(signals guidance.StepExecutionSignals) *llmtypes.Skill {
	return guidance.MaterializeStepExecutionReferenceSkill(signals)
}

// resolveBrowserConfig resolves the browser configuration for prompt instructions.
// Uses orchestrator-level browserMode as primary, falls back to auto-detection from servers/skills.
func (hcpo *StepBasedWorkflowOrchestrator) resolveBrowserConfig(serverNames []string, skills []string) browserinstructions.BrowserConfig {
	cfg := browserinstructions.BrowserConfig{
		CdpPort:  hcpo.GetCdpPort(),
		CdpPorts: hcpo.GetCdpPorts(),
	}

	// Detect browser capabilities from server names and skills
	for _, s := range serverNames {
		switch s {
		case "workspace_browser":
			cfg.HasAgentBrowser = true
		}
	}
	for _, skill := range skills {
		switch skill {
		case "agent-browser":
			cfg.HasAgentBrowser = true
		}
	}

	// Resolve mode: explicit setting > auto-detect from capabilities
	if mode := hcpo.GetBrowserMode(); mode != "" {
		cfg.Mode = mode
	} else if cfg.CdpPort > 0 || len(cfg.CdpPorts) > 0 {
		cfg.Mode = "cdp"
	} else if cfg.HasAgentBrowser {
		cfg.Mode = "headless"
	}

	return cfg
}

// installedWorkflowSkillResolver adapts the workspace skill reader to
// mcpagent's read_skill fallback, matching what chat mode installs.
func installedWorkflowSkillResolver(workspacePath string) mcpagent.InstalledSkillResolver {
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

// registeredToolNames extracts the tool names actually handed to an agent, so
// the reference corpus can be selected from what the session holds rather than
// from a workshop mode (PLAT-125).
func registeredToolNames(tools []llmtypes.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		if name := strings.TrimSpace(tool.Function.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
