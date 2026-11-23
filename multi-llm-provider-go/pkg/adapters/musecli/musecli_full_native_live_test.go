package musecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/nativeshell"
)

// Full CLI Muse (no allowlist, --yolo): the TUI must settle and take the first
// prompt, its own shell must create a file in the working directory, and the
// MCP bridge must keep working. Nothing exercised this launch before
// Excellence 2026-10-03, where Muse's failed sandbox probe kept the TUI from
// settling and the prompt was never typed in.
func TestMuseCLIRealFullNative(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	t.Setenv(nativeshell.EnvVar, "on") // this test drives Muse's own shell
	for _, structured := range []bool{false, true} {
		name := "tmux"
		if structured {
			name = "structured"
		}
		t.Run(name, func(t *testing.T) {
			var called atomic.Int32
			stub := museProbeMCPStub(&called)
			defer stub.Close()
			workDir := t.TempDir()
			secret := "FULL-READ-" + museRandomHex(t, 4)
			if err := os.WriteFile(filepath.Join(workDir, "witness.txt"), []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			token := "FULL-MCP-" + museRandomHex(t, 4)
			prompt := "Integration test in a disposable directory. Use your own shell to run: cat witness.txt && touch native-made.txt. " +
				"Then call the probe-stub MCP tool with token " + token + ". Reply with the witness contents and the MCP result."
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
				WithMCPConfig(`{"mcpServers":{"probe-stub":{"url":"`+stub.URL+`/mcp"}}}`), WithWorkingDir(workDir), WithMuseStructuredTransport(structured), WithTmuxTransport(!structured), llmtypes.WithReasoningEffort("low"), liveConfined(t, "muse-cli", workDir))
			if err != nil {
				t.Fatalf("full-mode round trip: %v", err)
			}
			if handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle; handle != nil && handle.TmuxSession != "" {
				t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "full-mode probe complete") })
			}
			final := resp.Choices[0].Content
			if !strings.Contains(final, secret) {
				t.Fatalf("native read failed: %q", final)
			}
			if _, statErr := os.Stat(filepath.Join(workDir, "native-made.txt")); statErr != nil {
				t.Fatalf("Muse's own shell could not create a file: %v", statErr)
			}
			if called.Load() == 0 || !strings.Contains(final, token) {
				t.Fatalf("MCP bridge not used: calls=%d final=%q", called.Load(), final)
			}
		})
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

// PLAT-491: in Full mode Muse's own shell (bash, bash_input, monitor) is refused unless the escape hatch is on,
// while native file edit stays. The bridge is simulated by the env marker the policy keys on (MCP_API_URL).
func TestMuseCLIRealFullModeNativeShellOff(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	run := func(t *testing.T, prompt string, workDir string) string {
		var called atomic.Int32
		stub := museProbeMCPStub(&called)
		defer stub.Close()
		cfg := `{"mcpServers":{"api-bridge":{"url":"` + stub.URL + `/mcp","env":{"MCP_API_URL":"http://127.0.0.1:1"}}}}`
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		resp, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}},
			WithMCPConfig(cfg), WithWorkingDir(workDir), WithMuseStructuredTransport(true), llmtypes.WithReasoningEffort("low"), liveConfined(t, "muse-cli", workDir))
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		if handle := resp.Choices[0].GenerationInfo.CodingProviderSessionHandle; handle != nil && handle.TmuxSession != "" {
			t.Cleanup(func() { CloseMuseCLIInteractiveSessionByTmux(handle.TmuxSession, "native shell probe complete") })
		}
		t.Logf("final answer: %s", resp.Choices[0].Content)
		return resp.Choices[0].Content
	}
	shellPrompt := "Integration test in a disposable directory. Step 1: use your own built-in bash tool (not any MCP tool) to run: touch shell-made.txt. " +
		"Step 2: use your write_file tool to create native-made.txt containing the word EDITOK. " +
		"Report exactly what happened for each step, quoting any refusal message verbatim."
	t.Run("off", func(t *testing.T) {
		t.Setenv(nativeshell.EnvVar, "off")
		dir := t.TempDir()
		final := run(t, shellPrompt, dir)
		if _, err := os.Stat(filepath.Join(dir, "shell-made.txt")); err == nil {
			t.Fatalf("Muse's own shell ran with the escape hatch off")
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "native-made.txt")); err != nil || !strings.Contains(string(raw), "EDITOK") {
			t.Fatalf("native file write did not work: %v %q", err, raw)
		}
		if !strings.Contains(final, "execute_shell_command") {
			t.Errorf("the refusal did not point at execute_shell_command: %q", final)
		}
	})
	t.Run("on", func(t *testing.T) {
		t.Setenv(nativeshell.EnvVar, "on")
		dir := t.TempDir()
		run(t, shellPrompt, dir)
		if _, err := os.Stat(filepath.Join(dir, "shell-made.txt")); err != nil {
			t.Fatalf("Muse's own shell did not run with the escape hatch on: %v", err)
		}
	})
	t.Run("on_platform_call_still_redirected", func(t *testing.T) {
		t.Setenv(nativeshell.EnvVar, "on")
		dir := t.TempDir()
		final := run(t, "Use your own built-in bash tool to run exactly: curl -s http://127.0.0.1:1/tools/custom/x ; then report verbatim what the tool returned or why it was refused.", dir)
		if !strings.Contains(final, "execute_shell_command") {
			t.Errorf("the platform-call redirect did not fire: %q", final)
		}
	})
}
