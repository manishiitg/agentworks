package mcpagent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

// projectedSkillLocation describes where a provider projects attached skills and
// its managed system-prompt file, so on-close cleanup can remove exactly what
// this session wrote into a REAL workdir. (Isolated workspaces are rm -rf'd
// wholesale by Agent.Close, so this never runs for them.) Skill subdirs mirror
// the per-adapter constants in multi-llm-provider-go
// (claudeCodeSkillsSubdir/cursorSkillsSubdir/codexSkillsSubdir/piSkillsSubdir).
type projectedSkillLocation struct {
	skillsSubdir string // e.g. ".claude/skills"
	promptFile   string // managed system-prompt file; "" when the provider keeps it inside a dir its own adapter teardown already wipes (Cursor: .cursor)
	promptMarker string // substring proving WE wrote promptFile — never delete operator content
}

var projectedSkillLocations = map[llm.Provider]projectedSkillLocation{
	llm.ProviderClaudeCode: {".claude/skills", "CLAUDE.md", "mlp-session-instructions"},
	llm.ProviderCodexCLI:   {".agents/skills", "AGENTS.md", "mlp-session-instructions"},
	llm.ProviderCursorCLI:  {".cursor/skills", "", ""},
	llm.ProviderPiCLI:      {".pi/skills", ".pi/APPEND_SYSTEM.md", "MCP Agent System Instructions"},
	// Muse shares codex's convention: .agents/skills subdirs and AGENTS.md
	// (both discovered by the CLI; .muse/skills is NOT discovered).
	llm.ProviderMuseCLI: {".agents/skills", "AGENTS.md", "mlp-session-instructions"},
}

// cleanupProjectedArtifactsOnClose removes exactly the skills + managed prompt
// this session projected for the active provider into a real (non-isolated)
// workdir: each attached skill's own folder (by name — an operator's
// differently-named skills are untouched) and the managed prompt file (only when
// it still carries our marker, so operator content is never destroyed). Empty
// parent dirs are pruned; the workdir itself is never pruned. No-op for
// non-coding providers and unknown workdirs. Closes the gap where Claude
// (.claude/skills + CLAUDE.md), Codex (.agents/skills), and Pi (.pi/skills +
// .pi/APPEND_SYSTEM.md) left projected artifacts on disk after a real-workdir
// session ended (only Cursor's adapter already wiped its .cursor tree).
func cleanupProjectedArtifactsOnClose(workingDir string, provider llm.Provider, skills []*llmtypes.Skill) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return
	}
	loc, ok := projectedSkillLocations[provider]
	if !ok {
		return
	}

	var pruneDirs []string
	if loc.skillsSubdir != "" && len(skills) > 0 {
		base := filepath.Join(workingDir, loc.skillsSubdir)
		for _, s := range skills {
			if s == nil || strings.TrimSpace(s.Name) == "" {
				continue
			}
			// Only a folder this session wrote (it holds the marker).
			skill := filepath.Join(base, s.Name)
			if _, err := os.Stat(filepath.Join(skill, projectfile.SkillMarkerFile)); err == nil {
				_ = os.RemoveAll(skill)
			}
		}
		pruneDirs = append(pruneDirs, base, filepath.Dir(base)) // e.g. .claude/skills, then .claude
	}
	if loc.promptFile != "" && loc.promptMarker != "" {
		promptPath := filepath.Join(workingDir, loc.promptFile)
		// The adapter already released this session's block; this strips a
		// leftover one and does nothing while another session still holds it.
		projectfile.StripStale(promptPath)
		removeManagedInstructionFile(promptPath)
		if dir := filepath.Dir(promptPath); dir != workingDir {
			pruneDirs = append(pruneDirs, dir) // e.g. .pi (only if now empty)
		}
	}
	pruneEmptyDirs(pruneDirs...)
}

func cleanupInactiveCodingAgentProjectArtifacts(workingDir string, activeProvider llm.Provider) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return
	}
	active := string(activeProvider)

	// Instruction files carry our marked block only while a session holds
	// them; a block left behind by a crash is stripped, the project's own
	// text never is. A file from the old format (whole file ours) is removed.
	for _, file := range []string{"CLAUDE.md", "AGENTS.md", filepath.Join(".pi", "APPEND_SYSTEM.md")} {
		path := filepath.Join(workingDir, file)
		projectfile.StripStale(path)
		removeManagedInstructionFile(path)
	}
	removeManagedFile(filepath.Join(workingDir, ".cursor", "rules", "mlp-system.mdc"))

	// Skills a session projected carry an ownership marker; remove only those
	// (never a project's own skills, settings, commands or rules).
	skillDirs := map[llm.Provider]string{
		llm.ProviderClaudeCode: filepath.Join(".claude", "skills"),
		llm.ProviderCursorCLI:  filepath.Join(".cursor", "skills"),
		llm.ProviderPiCLI:      filepath.Join(".pi", "skills"),
	}
	for provider, dir := range skillDirs {
		if provider != activeProvider {
			removeManagedSkills(filepath.Join(workingDir, dir))
		}
	}
	// .agents/skills is shared by codex and muse: spare it when either is active.
	if active != string(llm.ProviderCodexCLI) && active != string(llm.ProviderMuseCLI) {
		removeManagedSkills(filepath.Join(workingDir, ".agents", "skills"))
	}

	// Generated configuration is recognised by its content, never by folder.
	if active != string(llm.ProviderCursorCLI) {
		for _, file := range []string{"cli.json", "hooks.json", "mcp.json"} {
			removeManagedFileIfGenerated(filepath.Join(workingDir, ".cursor", file))
		}
		removeManagedFile(filepath.Join(workingDir, ".cursor", "hooks", "mlp-deny-builtin.sh"))
	}
	if active != string(llm.ProviderPiCLI) {
		removeManagedFileIfGenerated(filepath.Join(workingDir, ".pi", "mcp.json"))
	}
	if active != string(llm.ProviderCodexCLI) {
		removeManagedFileIfGenerated(filepath.Join(workingDir, ".codex", "config.toml"))
	}
	// Antigravity CLI (Agy) is no longer a supported provider; its generated
	// files under .agents/ are stale and recognised by content or fixed name.
	removeManagedFile(filepath.Join(workingDir, ".agents", "rules", "mlp-system.md"))
	removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "mcp_config.json"))
	removeManagedFileIfGenerated(filepath.Join(workingDir, ".agents", "hooks.json"))
	removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-hook.sh"))
	removeManagedFile(filepath.Join(workingDir, ".agents", "mlp-bridge-only-denials.jsonl"))
	pruneEmptyDirs(
		filepath.Join(workingDir, ".agents", "rules"),
		filepath.Join(workingDir, ".agents", "skills"),
		filepath.Join(workingDir, ".agents"),
		filepath.Join(workingDir, ".claude", "skills"),
		filepath.Join(workingDir, ".claude"),
		filepath.Join(workingDir, ".cursor", "rules"),
		filepath.Join(workingDir, ".cursor", "hooks"),
		filepath.Join(workingDir, ".cursor", "skills"),
		filepath.Join(workingDir, ".cursor"),
		filepath.Join(workingDir, ".pi", "skills"),
		filepath.Join(workingDir, ".pi"),
		filepath.Join(workingDir, ".codex"),
	)
}

// removeManagedSkills removes the skill folders under dir that a session
// projected (they hold the ownership marker), and nothing else.
func removeManagedSkills(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill := filepath.Join(dir, entry.Name())
		if _, err := os.Stat(filepath.Join(skill, projectfile.SkillMarkerFile)); err == nil {
			_ = os.RemoveAll(skill)
		}
	}
}

func removeManagedInstructionFile(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	// Old format: the whole file was ours and ended with this sentinel.
	if strings.Contains(string(body), "<!-- mlp-session-instructions -->") && !strings.Contains(string(body), "BEGIN agentworks-session-instructions") {
		_ = os.Remove(path)
	}
}

func removeManagedFileIfGenerated(path string) {
	// #nosec G304 - path is built from the configured coding-agent working directory.
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := string(body)
	if strings.Contains(text, "api-bridge") ||
		strings.Contains(text, "MCP_TOOLS") ||
		strings.Contains(text, "mcpbridge") ||
		strings.Contains(text, "mlp-deny-builtin") ||
		strings.Contains(text, "mlp-bridge-only") {
		_ = os.Remove(path)
	}
}

func removeManagedFile(path string) {
	_ = os.Remove(path)
}

func pruneEmptyDirs(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}
