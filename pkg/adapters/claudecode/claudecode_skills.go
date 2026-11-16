package claudecode

import (
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/skillproject"
)

// claudeCodeSkillsSubdir is claude-code's native skill location at the
// project scope. Global (user-level) skills live at ~/.claude/skills/
// but the adapter only handles per-workflow project scope here.
const claudeCodeSkillsSubdir = ".claude/skills"

// ProjectSkills writes each attached skill into <workdir>/.claude/skills/<name>/
// as Anthropic-format SKILL.md plus any supporting files. Claude Code
// discovers skills natively from this path; no further wiring needed.
//
// Idempotent: safe to call at both session launch and session resume.
func (a *ClaudeCodeAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	return projectClaudeCodeSkills(workdir, skills)
}

// ProjectSkills satisfies the mcpagent.SkillProjector contract for the
// tmux adapter. Same on-disk layout as ClaudeCodeAdapter — Claude Code
// reads skills from <workdir>/.claude/skills/ regardless of whether
// it's launched in print or interactive/tmux mode.
func (a *ClaudeCodeInteractiveAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	return projectClaudeCodeSkills(workdir, skills)
}

func projectClaudeCodeSkills(workdir string, skills []*llmtypes.Skill) error {
	if workdir == "" {
		return nil
	}
	target := filepath.Join(workdir, claudeCodeSkillsSubdir)
	if len(skills) == 0 {
		// The last attached skill was removed: a kept process must not keep it.
		skillproject.Prune(target)
		return nil
	}
	return skillproject.Write(target, skills)
}
