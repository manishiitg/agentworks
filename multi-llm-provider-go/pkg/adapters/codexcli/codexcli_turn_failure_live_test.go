package codexcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A real Codex failure on the structured transport (`exec --json`): a model the account cannot use.
// The reason must come back as a CodexTurnError read from the structured turn.failed event, not as
// "exit status 1: Reading additional input from stdin...". Set RUN_CODEX_CLI_REAL_E2E=1 to run.
func TestCodexCLIRealStructuredTurnFailureReportsTheReason(t *testing.T) {
	if os.Getenv("RUN_CODEX_CLI_REAL_E2E") == "" {
		t.Skip("set RUN_CODEX_CLI_REAL_E2E=1 to run the real Codex failure check")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("codex not in PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adapter := NewCodexCLIAdapter("", "definitely-not-a-model", quietCodexStreamLogger{})
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with one word."),
	}, WithProjectDirID(t.TempDir()), WithCodexStructuredTransport(true))
	var turnErr *CodexTurnError
	if !errors.As(err, &turnErr) {
		t.Fatalf("want a CodexTurnError, got %v", err)
	}
	if strings.Contains(err.Error(), "Reading additional input") || !strings.Contains(err.Error(), "definitely-not-a-model") {
		t.Fatalf("the provider's reason is missing: %v", err)
	}
}
