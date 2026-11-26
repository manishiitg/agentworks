package picli

import (
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/skillproject"
)

// piSkillsSubdir is Pi's native project-level Agent Skills location.
// The adapter passes this path explicitly with --skill because project
// resource discovery is intentionally disabled for managed tmux sessions.
const piSkillsSubdir = ".pi/skills"

// ProjectSkills writes each attached skill into <workdir>/.pi/skills/<name>/
// as Anthropic-format SKILL.md plus any supporting files.
//
// Idempotent: safe to call at both session launch and session resume.
func (a *PiCLIAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	if workdir == "" || len(skills) == 0 {
		return nil
	}
	return skillproject.Write(piProjectedSkillsPath(workdir), skills)
}

func piProjectedSkillsPath(workdir string) string {
	return filepath.Join(workdir, piSkillsSubdir)
}
