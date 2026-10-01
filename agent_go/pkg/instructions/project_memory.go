package instructions

import (
	_ "embed"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

//go:embed project_memory.md
var projectMemoryBody string

const ProjectMemorySkillName = "project-memory"

// ProjectMemoryPrompt retains the rules needed before skill selection. The
// managed skill teaches formatting and update procedures; it is never a store.
func ProjectMemoryPrompt(readOnly bool) string {
	core := `## Persistent project memory

Use the project root MEMORY.md as the one durable memory store shared across project chats and jobs (project/MEMORY.md in a linked CLI runtime). Read it before claiming project facts are unknown or repeating research. Skills hold procedures, never project facts. Do not edit projected provider instructions or use provider-native memory as an alternate store. Never retain secrets, guesses or transient logs. Sensitive personal information requires explicit permission. Creating or changing a reusable skill requires an explicit user request.
`
	if readOnly {
		return core + "Run mode may read permitted memory but cannot update memory, instructions or skills. Read the attached project-memory skill for memory questions; suggest requested changes to the owner.\n"
	}
	return core + "Save stable verified facts proactively. Read the attached project-memory skill before memory updates, corrections or forgetting; follow its format and report material changes.\n"
}

// ProjectMemorySkill returns a fresh mode-specific skill for each session. Run
// never receives the writer procedure, even when the account is the owner.
func ProjectMemorySkill(readOnly bool) *llmtypes.Skill {
	description := "Maintain the project's MEMORY.md: remember verified facts, retrieve earlier context, correct stale entries or forget information. Read before updating memory; this managed skill stores procedures, not facts."
	content := projectMemoryBody
	if readOnly {
		description = "Read permitted project memory to answer questions about earlier facts or decisions. Run cannot update MEMORY.md, instructions or skills; requested changes go to the owner."
		content = `# Read project memory

Resolve the active project's MEMORY.md from the workspace map; in a linked CLI runtime use project/MEMORY.md. Read only within current folder grants. Use relevant verified entries as context, check their source/date when freshness matters, and mention MEMORY.md when it materially shapes the answer. Missing memory is not proof a fact is false. Do not create or update memory, instructions or skills. Explain requested corrections or forgetting to the owner through the permitted suggestion mechanism; do not claim a change happened.
`
	}
	return &llmtypes.Skill{Name: ProjectMemorySkillName, Description: description, Content: strings.TrimSpace(content), Source: llmtypes.SkillSource{Origin: "builtin"}}
}
