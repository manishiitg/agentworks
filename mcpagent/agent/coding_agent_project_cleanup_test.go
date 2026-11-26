package mcpagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

// TestCleanupProjectedArtifactsOnClose is the deterministic (no live CLI) proof
// of the on-close cleanup logic: for every provider it must remove exactly the
// skill folder(s) this session projected and the marker-verified managed prompt
// file, while leaving an operator's own differently-named skills and un-marked
// prompt files untouched, and never touching the workdir itself.
func TestCleanupProjectedArtifactsOnClose(t *testing.T) {
	// managedPrompt carries our marker; provider-specific content built inline.
	writeFile := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill := func(t *testing.T, workdir, subdir, name string) string {
		t.Helper()
		dir := filepath.Join(workdir, subdir, name)
		writeFile(t, filepath.Join(dir, "SKILL.md"), "# "+name+"\ncontent\n")
		if name == "managed-skill" {
			writeFile(t, filepath.Join(dir, projectfile.SkillMarkerFile), "x")
		}
		return dir
	}

	cases := []struct {
		provider     llm.Provider
		skillsSubdir string
		promptFile   string // "" => provider keeps prompt in an adapter-wiped dir; no managed prompt to assert
		promptBody   string // managed body (with marker) written when promptFile != ""
	}{
		{llm.ProviderClaudeCode, ".claude/skills", "CLAUDE.md", "<!-- BEGIN agentworks-session-instructions created=true -->\nyou are managed\n<!-- END agentworks-session-instructions -->\n"},
		{llm.ProviderCodexCLI, ".agents/skills", "AGENTS.md", "<!-- BEGIN agentworks-session-instructions created=true -->\nyou are managed\n<!-- END agentworks-session-instructions -->\n"},
		{llm.ProviderCursorCLI, ".cursor/skills", "", ""},
		{llm.ProviderPiCLI, ".pi/skills", ".pi/APPEND_SYSTEM.md", "<!-- BEGIN agentworks-session-instructions created=true -->\nyou are managed\n<!-- END agentworks-session-instructions -->\n"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.provider), func(t *testing.T) {
			workdir := t.TempDir()

			managedSkill := writeSkill(t, workdir, tc.skillsSubdir, "managed-skill")
			operatorSkill := writeSkill(t, workdir, tc.skillsSubdir, "operator-own-skill")

			// A marker-less prompt file the operator wrote themselves (same path a
			// managed one would use) must survive when we DIDN'T project a managed
			// one — but in the managed sub-case we overwrite it. Test operator
			// safety with a distinct un-marked file at the managed path only in a
			// separate assertion below.
			if tc.promptFile != "" {
				writeFile(t, filepath.Join(workdir, tc.promptFile), tc.promptBody)
			}

			// The session projected ONLY "managed-skill".
			skills := []*llmtypes.Skill{{Name: "managed-skill", Content: "x"}}
			cleanupProjectedArtifactsOnClose(workdir, tc.provider, skills)

			if _, err := os.Stat(managedSkill); !os.IsNotExist(err) {
				t.Fatalf("%s: projected skill dir %q should have been removed (err=%v)", tc.provider, managedSkill, err)
			}
			if _, err := os.Stat(operatorSkill); err != nil {
				t.Fatalf("%s: operator's own skill dir %q must survive but was removed: %v", tc.provider, operatorSkill, err)
			}
			if tc.promptFile != "" {
				if _, err := os.Stat(filepath.Join(workdir, tc.promptFile)); !os.IsNotExist(err) {
					t.Fatalf("%s: managed prompt %q (has marker) should have been removed (err=%v)", tc.provider, tc.promptFile, err)
				}
			}
			// The workdir itself must never be pruned.
			if _, err := os.Stat(workdir); err != nil {
				t.Fatalf("%s: workdir must never be removed: %v", tc.provider, err)
			}
		})
	}
}

// TestCleanupProjectedArtifactsPreservesUnmarkedPrompt proves the marker guard:
// a prompt file the operator wrote themselves (no mlp marker) is NEVER deleted,
// even when we projected a skill for that provider.
func TestCleanupProjectedArtifactsPreservesUnmarkedPrompt(t *testing.T) {
	workdir := t.TempDir()
	// Operator's own CLAUDE.md, no managed marker.
	operatorPrompt := filepath.Join(workdir, "CLAUDE.md")
	if err := os.WriteFile(operatorPrompt, []byte("# my own project instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(workdir, ".claude/skills", "managed-skill")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, projectfile.SkillMarkerFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupProjectedArtifactsOnClose(workdir, llm.ProviderClaudeCode, []*llmtypes.Skill{{Name: "managed-skill"}})

	if _, err := os.Stat(operatorPrompt); err != nil {
		t.Fatalf("operator's un-marked CLAUDE.md must NOT be deleted: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("projected skill should still have been removed (err=%v)", err)
	}
}

// TestCleanupProjectedArtifactsNoopForNonCodingProvider guards the provider gate.
func TestCleanupProjectedArtifactsNoopForNonCodingProvider(t *testing.T) {
	workdir := t.TempDir()
	stray := filepath.Join(workdir, ".claude/skills", "managed-skill")
	if err := os.MkdirAll(stray, 0o750); err != nil {
		t.Fatal(err)
	}
	// A non-coding provider has no entry in projectedSkillLocations -> no-op.
	cleanupProjectedArtifactsOnClose(workdir, llm.ProviderOpenAI, []*llmtypes.Skill{{Name: "managed-skill"}})
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("non-coding provider cleanup must be a no-op, but the dir was removed: %v", err)
	}
}

// A product/chat Agent is constructed and closed for every browser turn, while
// its provider-native tmux session stays alive for the whole conversation.
// Closing the short-lived Agent must therefore not delete the skill folders or
// managed project instructions that the live CLI still uses.
func TestAgentCloseKeepsProjectedArtifactsForPersistentInteractiveSession(t *testing.T) {
	workdir := t.TempDir()
	skillDir := filepath.Join(workdir, ".claude", "skills", "managed-skill")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(workdir, "CLAUDE.md")
	if err := os.WriteFile(prompt, []byte("<!-- mlp-session-instructions -->\nmanaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	agent := &Agent{
		provider:                               llm.ProviderClaudeCode,
		modelID:                                "claude-sonnet-5",
		codingAgentWorkingDir:                  workdir,
		claudeCodePersistentInteractiveSession: true,
		attachedSkills:                         []*llmtypes.Skill{{Name: "managed-skill"}},
	}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(skillDir); err != nil {
		t.Fatalf("persistent session skill must survive per-turn Agent.Close: %v", err)
	}
	if _, err := os.Stat(prompt); err != nil {
		t.Fatalf("persistent session CLAUDE.md must survive per-turn Agent.Close: %v", err)
	}
}
