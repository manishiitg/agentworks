package cursorcli

import (
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/skillproject"
)

// cursorSkillsSubdir is Cursor's native project-level skill location.
// Cursor also reads .agents/skills/ (the cross-provider convention),
// but we project to its native path so Cursor's own UI surfaces the
// skill discretely rather than as a third-party-folder entry.
const cursorSkillsSubdir = ".cursor/skills"

// ProjectSkills writes each attached skill into <workdir>/.cursor/skills/<name>/
// as Anthropic-format SKILL.md plus any supporting files. Cursor
// auto-discovers skills from this directory and applies them via its
// Agent's progressive-disclosure routing.
//
// Idempotent: safe to call at both session launch and session resume.
func (a *CursorCLIAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	if workdir == "" || len(skills) == 0 {
		return nil
	}
	return skillproject.Write(filepath.Join(workdir, cursorSkillsSubdir), skills)
}
