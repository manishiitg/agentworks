package agycli

import (
	"slices"
	"testing"
)

func TestAgyDeclaresTheRuleFilesItReads(t *testing.T) {
	got := NativeProjectInstructionFiles()
	if !slices.Equal(got, []string{"AGENTS.md", "GEMINI.md"}) {
		t.Fatalf("agy reads AGENTS.md and GEMINI.md natively, not CLAUDE.md: %v", got)
	}
	got[0] = "changed"
	if NativeProjectInstructionFiles()[0] != "AGENTS.md" {
		t.Fatal("returned slice must be caller-owned")
	}
}
