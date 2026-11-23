package agycli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgyCLIRealFullNativeToolsExec(t *testing.T) {
	agyFullNativeToolsLive(t, false)
}

func TestAgyCLIRealFullNativeToolsInteractive(t *testing.T) {
	agyFullNativeToolsLive(t, true)
}

// Exercise actual native edits, shell and delegated work beside a real MCP
// call. File canaries and the CLI's native trail prove execution, not a model's
// claim. The delegated shell delay catches a premature parent completion.
func agyFullNativeToolsLive(t *testing.T, interactive bool) {
	t.Helper()
	requireRealAgyCLIE2E(t)
	workDir := t.TempDir()
	server, logPath := agyWriteCanaryServer(t, workDir, "full-native-canary.js")
	token := "AGY_FULL_" + agyRandomHex(t, 5)
	owner := "agy-full-native-" + agyRandomHex(t, 4)
	opts := []llmtypes.CallOption{
		WithWorkingDir(workDir), WithNativeToolsMode("full"), liveConfined(t, "agy-cli", workDir),
		WithMCPConfig(agyCanaryMCPConfig(server, logPath, "0")),
	}
	if interactive {
		opts = append(opts, WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true))
		t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "full-native test cleanup") })
	}
	prompt := fmt.Sprintf(`This is an authorised integration test in a disposable folder. AGY Full CLI native tools are enabled.
1. Use native write_to_file to create native.txt containing ORIGINAL_%s, then a native edit tool to replace it with %s.
2. Use native run_command to run: printf '%%s' '%s' > shell.txt
3. Use invoke_subagent to invoke self in the inherited workspace. Instruct that subagent to use its native run_command to run: sleep 3; printf '%%s' '%s' > child.txt . Do not create child.txt yourself. Wait until the delegated operation has completed; check the file with native view_file.
4. Call the MCP bridge_canary tool once. MCP must not perform any file operation.
Only after all four steps succeed, reply exactly: %s AGY_MCP_BRIDGE_OK`, token, token, token, token, token)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	adapter := NewAgyCLIAdapter("gemini-3.8-flash-high", "", nil)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, prompt)}, opts...)
	if err != nil {
		t.Fatalf("Full CLI turn: %v", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		t.Fatal("no Full CLI reply")
	}
	if got := strings.TrimSpace(resp.Choices[0].Content); got != token+" AGY_MCP_BRIDGE_OK" {
		t.Fatalf("final reply = %q", got)
	}
	for _, name := range []string{"native.txt", "shell.txt", "child.txt"} {
		raw, err := os.ReadFile(filepath.Join(workDir, name))
		if err != nil || strings.TrimSpace(string(raw)) != token {
			t.Fatalf("%s = %q, %v", name, raw, err)
		}
	}
	raw, err := os.ReadFile(logPath)
	if err != nil || strings.Count(string(raw), `"tool":"bridge_canary"`) != 1 {
		t.Fatalf("MCP call log = %s, %v", raw, err)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(resp)
	if !ok || handle.NativeSessionID == "" {
		t.Fatal("Full CLI lost its native conversation handle")
	}
	seen := map[string]bool{}
	for _, call := range agyTurnToolCallsSince(handle.NativeSessionID, -1) {
		seen[call.Name] = true
	}
	for _, name := range []string{"write_to_file", "run_command", "invoke_subagent"} {
		if !seen[name] {
			t.Fatalf("native trail lacks %s: %v", name, seen)
		}
	}
	if !seen["replace_file_content"] && !seen["multi_replace_file_content"] {
		t.Fatalf("native trail lacks a file edit: %v", seen)
	}
	if interactive {
		followup, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use native view_file to read native.txt. Reply with exactly its contents.")}, opts...)
		if err != nil || followup == nil || len(followup.Choices) == 0 || strings.TrimSpace(followup.Choices[0].Content) != token {
			t.Fatalf("retained Full CLI followup: %v", err)
		}
		next, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(followup)
		if !ok || next.NativeSessionID != handle.NativeSessionID || next.TmuxSession != handle.TmuxSession {
			t.Fatal("Full CLI followup replaced its native session")
		}
	}
}

// liveConfined runs a Full CLI live test under the lock a real chat gets
// (Seatbelt on a Mac, Landlock on Linux), or skips: Full CLI never runs
// unconfined.
func liveConfined(t *testing.T, provider, workDir string) llmtypes.CallOption {
	t.Helper()
	policy, ok := clisandbox.TestConfinement(provider, workDir)
	if !ok {
		t.Skip("this host cannot confine a coding CLI (set CODING_TEST_LANDLOCK_RUNNER on Linux)")
	}
	if policy.SeatbeltEnforced() {
		// Only SeatbeltArgs writes this profile: proof the CLI started inside it.
		profile := filepath.Join(policy.PrivateHome, "agentworks-cli-seatbelt.sb")
		t.Cleanup(func() {
			if _, err := os.Stat(profile); err != nil {
				t.Errorf("the CLI did not start under Seatbelt: %v", err)
			}
		})
	}
	return func(o *llmtypes.CallOptions) { p := policy.Clone(); o.CLISecurity = &p }
}
