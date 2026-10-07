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
