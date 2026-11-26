package claudecode

import (
	"os"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestStatusLineForTmuxSessionReadsPlanUsage(t *testing.T) {
	session := "mlp-claude-code-test-" + randomHex(4)
	if _, ok := StatusLineForTmuxSession(session); ok {
		t.Fatal("no sidecar yet must report false")
	}
	path := claudeStatuslinePath(session)
	t.Cleanup(func() { _ = os.Remove(path) })
	payload := `{"context_window":{"total_input_tokens":1200,"total_output_tokens":30,"context_window_size":1000000,"used_percentage":19},` +
		`"cost":{"total_cost_usd":0.42},"rate_limits":{"five_hour":{"used_percentage":44,"resets_at":1790515800},"seven_day":{"used_percentage":12,"resets_at":1790870400}}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	status, ok := StatusLineForTmuxSession(session)
	if !ok {
		t.Fatal("sidecar with usage must be read")
	}
	windows := status.RateLimitWindows()
	if len(windows) != 2 {
		t.Fatalf("rate limit windows = %+v", windows)
	}
	if _, ok := status.Metadata[llmtypes.StatusExtrasMetaKey]; !ok {
		t.Fatalf("status extras missing: %+v", status.Metadata)
	}
}
