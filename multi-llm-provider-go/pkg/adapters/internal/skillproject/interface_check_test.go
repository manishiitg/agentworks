package skillproject_test

// This test exists only to assert at compile time that every CLI
// adapter implements the projection contract. If an adapter is renamed
// or its ProjectSkills signature drifts, this file fails to compile
// instead of silently breaking the launch path at runtime.
//
// The contract is duplicated locally (not imported from mcpagent) to
// avoid pulling mcpagent into a test in this repo, which would create
// the import cycle the type-in-llmtypes split was designed to prevent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

type skillProjector interface {
	ProjectSkills(workdir string, skills []*llmtypes.Skill) error
}

var (
	_ skillProjector = (*claudecode.ClaudeCodeAdapter)(nil)
	_ skillProjector = (*cursorcli.CursorCLIAdapter)(nil)
	_ skillProjector = (*codexcli.CodexCLIAdapter)(nil)
	_ skillProjector = (*picli.PiCLIAdapter)(nil)
)

func TestCLIAdaptersProjectSkillsToNativeDirectories(t *testing.T) {
	skills := []*llmtypes.Skill{{
		Name:        "Agent Browser",
		Description: "Drive a browser",
		Content:     "# Agent Browser\n\nUse the browser carefully.\n",
		SupportingFiles: []llmtypes.SkillFile{{
			RelPath: "references/api.md",
			Content: []byte("# API\n"),
		}},
	}}

	tests := []struct {
		name         string
		projector    skillProjector
		skillsSubdir string
	}{
		{name: "claude-code", projector: &claudecode.ClaudeCodeAdapter{}, skillsSubdir: ".claude/skills"},
		{name: "cursor-cli", projector: &cursorcli.CursorCLIAdapter{}, skillsSubdir: ".cursor/skills"},
		{name: "codex-cli", projector: &codexcli.CodexCLIAdapter{}, skillsSubdir: ".agents/skills"},
		{name: "pi-cli", projector: &picli.PiCLIAdapter{}, skillsSubdir: ".pi/skills"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workdir := t.TempDir()
			if err := tt.projector.ProjectSkills(workdir, skills); err != nil {
				t.Fatalf("ProjectSkills() error = %v", err)
			}

			skillDir := filepath.Join(workdir, filepath.FromSlash(tt.skillsSubdir), "agent-browser")
			body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
			if err != nil {
				t.Fatalf("read projected SKILL.md: %v", err)
			}
			for _, want := range []string{"name: agent-browser", "description: Drive a browser", "# Agent Browser"} {
				if !strings.Contains(string(body), want) {
					t.Fatalf("projected SKILL.md = %q, want %q", body, want)
				}
			}
			if _, err := os.Stat(filepath.Join(skillDir, "references", "api.md")); err != nil {
				t.Fatalf("projected supporting file missing: %v", err)
			}
		})
	}
}
