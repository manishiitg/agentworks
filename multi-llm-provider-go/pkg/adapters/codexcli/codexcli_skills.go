package codexcli

import (
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/skillproject"
)

// codexSkillsSubdir uses the .agents/ cross-provider convention.
// Codex CLI reads skills from this repo-level path at session launch
// (per the docs: "$CWD/.agents/skills — current working directory:
// where you launch Codex").
const codexSkillsSubdir = ".agents/skills"

// ProjectSkills writes each attached skill into <workdir>/.agents/skills/<name>/
// as Anthropic-format SKILL.md plus any supporting files.
//
// Idempotent: safe to call at both session launch and session resume.
func (a *CodexCLIAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	if workdir == "" || len(skills) == 0 {
		return nil
	}
	return skillproject.Write(filepath.Join(workdir, codexSkillsSubdir), skills)
}
