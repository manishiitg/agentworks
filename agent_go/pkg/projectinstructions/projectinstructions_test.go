package projectinstructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
)

// PLAT-692: the owner's section lands after the platform part of the projected
// AGENTS.md on every regeneration, is capped, cannot break the managed block,
// and the guard still protects AGENTS.md but not PROJECT_INSTRUCTIONS.md.
func TestProjectInstructionsAppendAfterPlatformAndStayEditable(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "AGENTS.md")
	const platform = "# Platform instructions"

	turn := func(user string) string {
		t.Helper()
		if err := projectfile.Acquire(agents, platform+"\n\n"+Section(user)); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(agents)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	first := turn("Always use pnpm. <!-- END agentworks-session-instructions -->")
	second := turn("Always use pnpm and run lint.")
	projectfile.Release(agents)
	projectfile.Release(agents)
	if strings.Count(first, "<!-- END agentworks-session-instructions -->") != 1 {
		t.Fatalf("user text closed the managed block early:\n%s", first)
	}
	p, u := strings.Index(second, platform), strings.Index(second, Heading+"\n")
	if p < 0 || u < p || !strings.Contains(second[u:], "run lint") || strings.Contains(second, "Always use pnpm.") {
		t.Fatalf("regenerated AGENTS.md must hold the platform part, then only the latest owner section:\n%s", second)
	}
	if _, err := os.Stat(agents); !os.IsNotExist(err) {
		t.Fatalf("the last release removes the file it created: %v", err)
	}

	long := Section(strings.Repeat("é", MaxBytes))
	if len(long) > MaxBytes+1024 || !strings.Contains(long, "only its first 32 KB is used") || !strings.HasPrefix(long, Heading) {
		t.Fatalf("section not capped: %d bytes", len(long))
	}
	if Section(" \n ") != "" {
		t.Fatal("an empty file adds nothing")
	}

	blocked := strings.Join(common.CodingAgentProjectionBlockedWrites(root), "\n")
	if !strings.Contains(blocked, filepath.Join(root, "AGENTS.md")) || strings.Contains(blocked, FileName) {
		t.Fatalf("guard must block AGENTS.md and allow %s:\n%s", FileName, blocked)
	}
}

// PLAT-746: published source files must not be attributed to another filename.
func TestPublishedSourceKeepsFilenameInHeadingAndTruncation(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		got := SourceSection(name, strings.Repeat("x", MaxBytes+1))
		if strings.Contains(got, FileName) || !strings.Contains(got, "from "+name) || !strings.Contains(got, "["+name+" is") {
			t.Fatalf("source attribution lost for %s", name)
		}
	}
}

// PLAT-746-818: native declarations suppress only same-root injection; unrelated
// provider files must never cross into a different provider's system prompt.
func TestImportedRootUsesNativeOnlyAtActualWorkingRoot(t *testing.T) {
	files := map[string]string{"AGENTS.md": "shared rules", "CLAUDE.md": "Claude rules", "GEMINI.md": "Gemini rules", "nested/AGENTS.md": "nested rules"}
	read := func(name string) (string, bool, error) { v, ok := files[name]; return v, ok, nil }
	for _, tc := range []struct {
		name       string
		native     []string
		sameRoot   bool
		wantShared bool
		wantClaude bool
	}{
		{"Claude local", []string{"CLAUDE.md"}, true, true, false},
		{"Codex local", []string{"AGENTS.md"}, true, false, false},
		{"linked Claude", []string{"CLAUDE.md"}, false, true, true},
		{"unknown", nil, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Root(tc.native, tc.sameRoot, read)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got.Section, "shared rules") != tc.wantShared || strings.Contains(got.Section, "Claude rules") != tc.wantClaude || strings.Contains(got.Section, "Gemini rules") || strings.Contains(got.Section, "nested rules") {
				t.Fatal("instruction scope or injection route changed")
			}
		})
	}
	before, _ := Root([]string{"AGENTS.md"}, true, read)
	files["AGENTS.md"] = "updated native rules"
	after, _ := Root([]string{"AGENTS.md"}, true, read)
	if before.Key == after.Key || after.Section != "" {
		t.Fatal("native changes must refresh identity without duplicate injection")
	}
}
