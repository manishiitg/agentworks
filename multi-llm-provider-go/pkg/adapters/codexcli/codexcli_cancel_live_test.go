package codexcli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A cancelled structured turn must leave no Codex process behind. `codex` on PATH is the npm wrapper, which
// starts the real binary as a child; killing only the wrapper left that child working after a workflow stop
// (Upwork, 2026-10-05: browser actions for 2.5 minutes after the cancel). Real Codex, real wrapper.
func TestCodexCLIRealStructuredCancelLeavesNoCodexProcess(t *testing.T) {
	if os.Getenv("RUN_CODEX_CLI_REAL_E2E") == "" {
		t.Skip("set RUN_CODEX_CLI_REAL_E2E=1 to run the real Codex cancel check")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("codex not in PATH: %v", err)
	}
	marker := fmt.Sprintf("cancel-probe-%d", time.Now().UnixNano())
	running := func() []string {
		out, _ := exec.CommandContext(context.Background(), "pgrep", "-f", marker).Output()
		return strings.Fields(string(out))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewCodexCLIAdapter("", "", quietCodexStreamLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{
			llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Ignore this tag: "+marker+". Write a detailed 3000-word essay on the history of maps."),
		}, WithProjectDirID(t.TempDir()), WithCodexStructuredTransport(true))
		done <- err
	}()

	deadline := time.Now().Add(60 * time.Second)
	for len(running()) < 2 && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if got := running(); len(got) < 2 {
		cancel()
		t.Fatalf("expected the wrapper and the real Codex to be running, found %v", got)
	}
	time.Sleep(3 * time.Second) // let the turn get going
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		for _, pid := range running() {
			_ = exec.CommandContext(context.Background(), "kill", "-9", pid).Run()
		}
		t.Fatal("GenerateContent did not return after cancel")
	}
	gone := time.Now().Add(5 * time.Second)
	for len(running()) > 0 && time.Now().Before(gone) {
		time.Sleep(200 * time.Millisecond)
	}
	if left := running(); len(left) > 0 {
		for _, pid := range left {
			_ = exec.CommandContext(context.Background(), "kill", "-9", pid).Run()
		}
		t.Fatalf("Codex processes were still running after cancel: %v", left)
	}
}
