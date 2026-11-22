package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-856-442: a failed changed-instruction resume must resend on retry, and
// an agent-controlled marker link cannot redirect an application-side write.
func TestClaudeResumeInstructionsRetryAndMarkerLinks(t *testing.T) {
	root := t.TempDir()
	opts := &llmtypes.CallOptions{CLIRuntimeDir: root}
	_, remember := claudeCurrentResumeInstructions(opts, root, "native-id", "source rules", "hello", false)
	remember()
	prompt, remember := claudeCurrentResumeInstructions(opts, root, "native-id", "destination rules", "next", true)
	if !strings.Contains(prompt, "destination rules") {
		t.Fatal("new instructions missing")
	}
	// Failed turn: do not call remember.
	retry, done := claudeCurrentResumeInstructions(opts, root, "native-id", "destination rules", "next", true)
	if !strings.Contains(retry, "destination rules") {
		t.Fatal("failed turn consumed refresh")
	}
	done()
	stable, _ := claudeCurrentResumeInstructions(opts, root, "native-id", "destination rules", "next", true)
	if stable != "next" {
		t.Fatal("unchanged instructions repeated")
	}
	outside := filepath.Join(t.TempDir(), "keep.txt")
	if e := os.WriteFile(outside, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	markers, _ := filepath.Glob(filepath.Join(root, ".claude", "*.sha256"))
	if len(markers) != 1 {
		t.Fatal("missing marker")
	}
	if e := os.Remove(markers[0]); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, markers[0]); e != nil {
		t.Fatal(e)
	}
	_, remember = claudeCurrentResumeInstructions(opts, root, "native-id", "new rules", "next", true)
	remember()
	raw, e := os.ReadFile(outside)
	if e != nil || string(raw) != "keep" {
		t.Fatal("instruction marker followed a link")
	}
}
