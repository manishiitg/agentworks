package llmtypes

// Skill is an Anthropic-format SKILL.md bundle attached to an agent.
//
// Skills live here in llmtypes (not in mcpagent) so adapters in this
// package and the Agent in mcpagent can share the same value type
// without a circular import. mcpagent owns the attachment API
// (Agent.AttachSkill / AttachedSkills); adapters own projection to
// the provider's working directory at session launch.
//
// Adapter projection layout (set by each adapter):
//   - claude-code      → <workdir>/.claude/skills/<name>/
//   - cursor-cli       → <workdir>/.cursor/skills/<name>/
//   - codex-cli        → <workdir>/.agents/skills/<name>/
//   - pi-cli           → <workdir>/.pi/skills/<name>/
//   - API transports   → not projected; surfaced through the system prompt listing
//
// The SKILL.md content is identical across providers; only the directory
// differs, because every native skill-aware CLI reads the same Anthropic
// frontmatter (name, description, optional paths / disable-model-invocation
// / metadata) and the same body.
type Skill struct {
	Name                   string            // folder name; lowercase + hyphens (e.g. "agent-browser")
	Description            string            // short summary used in the system prompt listing
	Content                string            // SKILL.md body (after YAML frontmatter)
	Paths                  []string          // optional glob patterns the skill applies to
	DisableModelInvocation bool              // when true, only invocable via /<name>, not auto-selected
	Metadata               map[string]string // arbitrary frontmatter key/value pairs
	SupportingFiles        []SkillFile       // scripts/, references/, assets/ relative to skill root
	Source                 SkillSource       // origin of this skill (for diagnostics / lock tracking)
}

// SkillFile is a single supporting file under a skill's folder.
type SkillFile struct {
	RelPath string // e.g. "scripts/extract.py" or "references/api.md"
	Content []byte // file contents (binary-safe for assets)
}

// SkillSource describes where a skill came from. Diagnostics only; not
// projected to disk.
type SkillSource struct {
	Origin    string // "imported" | "global-learnings" | "builtin"
	SourceURL string // GitHub or other URL when Origin == "imported"
}
