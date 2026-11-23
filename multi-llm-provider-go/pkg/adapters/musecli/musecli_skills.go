package musecli

import (
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/skillproject"
)

// museSkillsSubdir is a project skill root Muse discovers in a trusted
// workspace (verified with `muse skills list --source project`: it reads
// .agents/skills and .claude/skills). .agents/skills is the cross-provider
// convention Codex already uses, so both CLIs share one projection.
const museSkillsSubdir = ".agents/skills"

// ProjectSkills writes each attached skill into <workdir>/.agents/skills/<name>/
// as Anthropic-format SKILL.md plus any supporting files, so Muse's native
// read_skill serves AgentWorks skills alongside its bundled ones. Launches
// always pass --trust-workspace, which project skills require.
//
// Idempotent: safe to call at both session launch and session resume.
func (a *MuseCLIAdapter) ProjectSkills(workdir string, skills []*llmtypes.Skill) error {
	if workdir == "" || len(skills) == 0 {
		return nil
	}
	return skillproject.Write(filepath.Join(workdir, museSkillsSubdir), skills)
}
