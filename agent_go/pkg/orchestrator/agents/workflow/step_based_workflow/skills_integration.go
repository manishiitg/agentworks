package step_based_workflow

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// GetEffectiveSkills returns the skills explicitly enabled for this step.
//
// PHASE 5 HARD CUT: the previous fallback to orchestrator.GetSelectedSkills()
// is gone. Step skills are step.EnabledSkills only. Workflow-level
// SelectedSkills are for the workshop builder agent, not for step
// execution. Workflows that relied on cascade need to either (a) set
// EnabledSkills per step, or (b) put the shared know-how in
// learnings/_global/SKILL.md, which is auto-attached at step launch
// via appendSupplementaryPrompts.
func GetEffectiveSkills(stepConfig *AgentConfigs, _ *orchestrator.BaseOrchestrator) []string {
	if stepConfig != nil && len(stepConfig.EnabledSkills) > 0 {
		return stepConfig.EnabledSkills
	}
	return nil
}

// effectiveStepSkills adds the built-in brain skill to a step that has Brain tools (the same condition that gives it
// brain_browse/brain_read), so it knows how to use Brain well, not only what each tool does.
func (hcpo *StepBasedWorkflowOrchestrator) effectiveStepSkills(stepConfig *AgentConfigs) []string {
	out := GetEffectiveSkills(stepConfig, hcpo.BaseOrchestrator)
	if resolveKnowledgebaseAccess(stepConfig, hcpo.UseKnowledgebase()) == KBAccessNone {
		return out
	}
	for _, name := range out {
		if name == "brain" {
			return out
		}
	}
	return append(append([]string(nil), out...), "brain")
}

// BuildWorkflowSkillPrompt is gone (Phase 3 rewire). Step skills are
// now attached to the agent via skills.LoadAttachable + AttachSkill in
// appendSupplementaryPrompts; mcpagent.ensureSystemPrompt renders the
// progressive-disclosure listing into the outgoing system prompt and
// CLI adapters project SKILL.md folders to disk via the SkillProjector
// contract. No builder needs to assemble skill markdown manually.

// BuildSkillFolderGuardPaths builds the folder guard paths for skills.
// Returns (readPaths, writePaths) - skills are read-only
func BuildSkillFolderGuardPaths(selectedSkills []string) (readPaths []string, writePaths []string) {
	if len(selectedSkills) == 0 {
		return nil, nil
	}

	// Build list of allowed skill paths (read-only)
	readPaths = make([]string, 0, len(selectedSkills)*2)
	for _, skill := range selectedSkills {
		if _, err := skills.ValidateSkillName(skill); err != nil {
			continue
		}
		for _, root := range skills.WorkspaceSkillDirectories {
			readPaths = append(readPaths, root+"/"+skill+"/", root+"/"+skill)
		}
	}

	// No write paths for skills - they are read-only
	return readPaths, nil
}

func filesystemSkills(skills []string) []string {
	return skills
}

func isBrowserAutomationSkill(skill string) bool {
	return skill == "agent-browser"
}
