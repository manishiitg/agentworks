package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Compile-time conformance: the codex adapter must satisfy the statusline
// contract interface so the cross-provider harness can drive it.
var _ llmtypes.StatusLineProvider = (*CodexCLIAdapter)(nil)

// TestStreamCodexStatusLineEmitsChunk is the codex producer-side e2e and the
// CertStatusLine certification target. Codex (tmux mode) has no statusline
// command hook, so it sources telemetry from its rollout JSONL. The test seeds a
// real rollout file + a registered session, runs streamCodexStatusLine, and
// asserts the emitted chunk carries the exact token telemetry (uncached prompt,
// cached, output), the real model, and the owning tmux session.
func TestStreamCodexStatusLineEmitsChunk(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	const cwd = "/work/codex-statusline"
	const sessionName = "mlp-codex-statusline-e2e"

	day := filepath.Join(tmpHome, ".codex", "sessions", "2026", "05", "19")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	rollout := filepath.Join(day, "rollout-2026-05-19T12-00-00-aaaabbbb-cccc-dddd-eeee-ffff00002222.jsonl")
	// codex reports input_tokens as the full prompt (uncached + cached);
	// readCodexTranscriptUsage splits them, so 15000 - 2000 = 13000 uncached.
	lines := "" +
		`{"type":"session_meta","timestamp":"2026-05-19T12:00:00Z","payload":{"id":"sess-1","cwd":"` + cwd + `"}}` + "\n" +
		`{"type":"turn_context","timestamp":"2026-05-19T12:00:01Z","payload":{"model":"gpt-5-codex"}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-05-19T12:00:02Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":15000,"cached_input_tokens":2000,"output_tokens":273,"reasoning_output_tokens":40,"total_tokens":15273}}}}` + "\n"
	if err := os.WriteFile(rollout, []byte(lines), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	// Register the session with its exact rollout, as submission does in production.
	old := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{
		sessionName: {tmuxSessionName: sessionName, workingDir: cwd, rolloutPath: rollout},
	})
	t.Cleanup(func() {
		codexPersistentRegistry.Replace(old)
	})

	streamChan := make(chan llmtypes.StreamChunk, 1)
	if ok := streamCodexStatusLine(context.Background(), sessionName, streamChan); !ok {
		t.Fatal("streamCodexStatusLine returned false; expected it to emit a chunk")
	}

	select {
	case chunk := <-streamChan:
		if chunk.Type != llmtypes.StreamChunkTypeStatusLine {
			t.Fatalf("chunk.Type = %q, want status_line", chunk.Type)
		}
		sl := chunk.StatusLine
		if sl == nil {
			t.Fatal("chunk.StatusLine is nil")
		}
		testcontracts.AssertStatusLineContract(t, sl, "codex-cli", true)
		if sl.Model != "gpt-5-codex" {
			t.Errorf("Model = %q, want gpt-5-codex", sl.Model)
		}
		if sl.InputTokens != 13000 {
			t.Errorf("InputTokens = %d, want 13000 (uncached prompt)", sl.InputTokens)
		}
		if sl.CacheReadInputTokens != 2000 {
			t.Errorf("CacheReadInputTokens = %d, want 2000", sl.CacheReadInputTokens)
		}
		if sl.OutputTokens != 273 {
			t.Errorf("OutputTokens = %d, want 273", sl.OutputTokens)
		}
		if sl.TotalInputTokens != 15000 {
			t.Errorf("TotalInputTokens = %d, want 15000 (uncached + cached)", sl.TotalInputTokens)
		}
		if got, _ := sl.Metadata["tmux_session"].(string); got != sessionName {
			t.Errorf("Metadata[tmux_session] = %q, want %q", got, sessionName)
		}
	default:
		t.Fatal("no chunk emitted on streamChan")
	}
}

func TestBuildCodexStatusLineUsesBoundConversation(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	cwd := t.TempDir()
	day := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "09", "30")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRollout := func(name, tokens string) string {
		t.Helper()
		path := filepath.Join(day, name)
		lines := `{"type":"session_meta","payload":{"id":"` + name + `","cwd":"` + cwd + `"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + tokens + `,"output_tokens":1}}}}` + "\n"
		if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	own := writeRollout("rollout-own.jsonl", "100")
	writeRollout("rollout-parallel.jsonl", "999")
	const bound, cold = "bound-terminal", "cold-terminal"
	old := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{
		bound: {tmuxSessionName: bound, workingDir: cwd, rolloutPath: own},
		cold:  {tmuxSessionName: cold, workingDir: cwd},
	})
	t.Cleanup(func() { codexPersistentRegistry.Replace(old) })
	status := buildCodexStatusLine(bound, cwd)
	if status == nil || status.InputTokens != 100 {
		t.Fatalf("bound terminal must report its own usage, got %+v", status)
	}
	if status := buildCodexStatusLine(cold, cwd); status != nil {
		t.Fatalf("unbound terminal must not borrow a parallel conversation's usage, got %+v", status)
	}
}
