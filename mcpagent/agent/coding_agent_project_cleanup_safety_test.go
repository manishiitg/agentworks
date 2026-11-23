package mcpagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// Starting a turn for one provider must never delete a project's own files
// from another CLI's folders (settings, commands, rules, skills).
func TestInactiveCleanupKeepsProjectsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	own := []string{
		".claude/settings.json", ".claude/commands/deploy.md", ".claude/skills/review/SKILL.md",
		".cursor/rules/team.mdc", ".cursor/mcp.json", ".pi/settings.json", ".codex/config.toml",
		".agents/skills/mine/SKILL.md", ".gemini/settings.json", "CLAUDE.md", "AGENTS.md",
	}
	for _, f := range own {
		writeFile(t, filepath.Join(dir, f), "user file")
	}
	for _, provider := range []llm.Provider{llm.ProviderCodexCLI, llm.ProviderClaudeCode, llm.ProviderCursorCLI, llm.ProviderPiCLI, llm.ProviderMuseCLI} {
		cleanupInactiveCodingAgentProjectArtifacts(dir, provider)
		for _, f := range own {
			if !exists(filepath.Join(dir, f)) {
				t.Fatalf("starting %s deleted the project's own %s", provider, f)
			}
		}
	}
}

// What a session projected is still cleaned up: skills that carry the marker,
// and a block left behind by a crashed session.
func TestInactiveCleanupRemovesOnlyWhatWeProjected(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, ".claude", "skills", "code-review")
	writeFile(t, filepath.Join(ours, "SKILL.md"), "ours")
	writeFile(t, filepath.Join(ours, projectfile.SkillMarkerFile), "x")
	writeFile(t, filepath.Join(dir, ".claude", "skills", "review", "SKILL.md"), "user's")
	agents := filepath.Join(dir, "AGENTS.md")
	writeFile(t, agents, "# my rules\n")
	if err := projectfile.Acquire(agents, "PROMPT"); err != nil {
		t.Fatal(err)
	}
	projectfile.Release(agents) // normal end
	writeFile(t, agents, "# my rules\n")

	cleanupInactiveCodingAgentProjectArtifacts(dir, llm.ProviderCodexCLI)
	if exists(ours) {
		t.Fatal("a projected skill was not cleaned up")
	}
	if !exists(filepath.Join(dir, ".claude", "skills", "review", "SKILL.md")) {
		t.Fatal("the project's own skill was removed")
	}
	b, _ := os.ReadFile(agents) // #nosec G304 - test temp path
	if string(b) != "# my rules\n" {
		t.Fatalf("AGENTS.md changed: %q", b)
	}
}

// A live session's prompt block is left alone.
func TestInactiveCleanupSparesAHeldInstructionFile(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	if err := projectfile.Acquire(agents, "LIVE PROMPT"); err != nil {
		t.Fatal(err)
	}
	defer projectfile.Release(agents)
	cleanupInactiveCodingAgentProjectArtifacts(dir, llm.ProviderClaudeCode)
	b, _ := os.ReadFile(agents) // #nosec G304 - test temp path
	if !contains(string(b), "LIVE PROMPT") {
		t.Fatalf("running session's prompt removed: %q", b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
