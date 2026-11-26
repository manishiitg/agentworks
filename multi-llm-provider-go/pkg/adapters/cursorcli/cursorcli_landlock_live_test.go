package cursorcli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-364: Cursor started under the Landlock launcher keeps its read-only
// native tools inside its folder, and the kernel refuses a read of another
// folder. Needs Linux and AGENTWORKS_LANDLOCK_RUNNER.
func TestCursorCLIRealLandlockConfinesNativeToolsP0(t *testing.T) {
	requireRealCursorCLIE2E(t)
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || runner == "" {
		t.Skip("needs Linux and AGENTWORKS_LANDLOCK_RUNNER")
	}
	t.Cleanup(func() { _ = CleanupCursorCLIInteractiveSessions(context.Background()) })
	workDir := t.TempDir()
	otherDir := t.TempDir()
	own := "OWN-" + cursorRandomHex(4)
	foreign := "FOREIGN-" + cursorRandomHex(4)
	if err := os.WriteFile(filepath.Join(workDir, "own.txt"), []byte(own+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(otherDir, "secret-test.txt")
	if err := os.WriteFile(foreignPath, []byte(foreign+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := llmtypes.CLISecurityPolicy{
		Mode:           llmtypes.CLISecurityModeIsolated,
		Provider:       "cursor-cli",
		LandlockRunner: runner,
		PrivateHome:    filepath.Join(t.TempDir(), "cli-home"),
	}
	confine := func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p }
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	prompt := "Integration test in disposable scratch folders. Use Cursor's built-in Read tool (not MCP, not shell): 1) read own.txt; 2) read " + foreignPath +
		". Report each outcome, then end with one line: the contents of own.txt, and the result or exact error for the second file."
	resp, err := NewCursorCLIAdapter("", "cursor-cli", &MockLogger{}).GenerateContent(ctx, []llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "When the user asks you to use a built-in tool, your FIRST action must be to attempt that Cursor built-in tool. Do not refuse upfront; attempt the call and report whatever happens."}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}},
	}, WithInteractiveSessionID("cursor-landlock-"+cursorRandomHex(4)), WithPersistentInteractiveSession(true), WithWorkingDir(workDir), WithFullNativeTools(), confine)
	if err != nil {
		t.Fatalf("GenerateContent under Landlock: %v", err)
	}
	final := resp.Choices[0].Content
	t.Logf("final: %.600s", final)
	if !strings.Contains(final, own) {
		t.Fatalf("confined Cursor lost its own folder: %q", final)
	}
	if strings.Contains(final, foreign) {
		t.Fatalf("confined Cursor read another folder: %q", final)
	}
}
