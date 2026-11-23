package agycli

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestAgyPrivateConversationsSurviveSidecarCleanupAndStaySeparate(t *testing.T) {
	server, chat, other := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", server)
	private, cleanup, err := agyIsolatedHomeWithConversations(nil, chat)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(private, ".gemini", "antigravity-cli", "conversations", "own.db")
	if err := os.WriteFile(path, []byte("own chat"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	private, cleanup, err = agyIsolatedHomeWithConversations(nil, chat)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if raw, err := os.ReadFile(filepath.Join(private, ".gemini", "antigravity-cli", "conversations", "own.db")); err != nil || string(raw) != "own chat" {
		t.Fatalf("resume: %q %v", raw, err)
	}
	for _, home := range []string{server, other} {
		if _, err := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "own.db")); !os.IsNotExist(err) {
			t.Fatalf("chat history escaped into %s: %v", home, err)
		}
	}
}

func TestAgyScopedReadersUseChatHomeInsteadOfServerHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chat := agyTestHome(t, "scoped")
	path := filepath.Join(chat, ".gemini", "antigravity-cli", "conversations", "scoped.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE steps ADD COLUMN status INTEGER DEFAULT 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM steps WHERE idx = 7`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	record, err := agyReadTurnRecord("scoped", -1, "first question", chat)
	if err != nil || record.userIdx != 0 || record.finalAnswer != "second answer" {
		t.Fatalf("scoped record: %+v %v", record, err)
	}
	if got := agyTurnReplySince("scoped", 5, chat); got != "second answer" {
		t.Fatalf("scoped answer: %q", got)
	}
	if got := agyConversationMaxIdx("scoped", chat); got != 6 {
		t.Fatalf("scoped baseline: %d", got)
	}
	if got := agyDiscoverConversationID(time.Now().Add(-time.Minute), "second question", chat); got != "scoped" {
		t.Fatalf("scoped discovery: %q", got)
	}
	if got, err := agyCountUserSteps("scoped", -1, "first question", chat); err != nil || got != 1 {
		t.Fatalf("scoped intake: %d %v", got, err)
	}
	if got := agyTurnReplySince("scoped", -1); got != "" {
		t.Fatalf("server read private conversation: %q", got)
	}
}

// Opt-in live regression for the deployed Linux sandbox. It verifies real
// SQLite intake/completion, retained follow-up and recovery after TUI loss.
func TestAgyLandlockLiveConversationPersistence(t *testing.T) {
	runner := os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	if runtime.GOOS != "linux" || os.Getenv("AGY_LANDLOCK_LIVE") != "1" || runner == "" {
		t.Skip("set AGY_LANDLOCK_LIVE=1 and CODING_TEST_LANDLOCK_RUNNER on Linux")
	}
	if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Fatal("live test requires the configured Gemini key")
	}
	work, home := t.TempDir(), t.TempDir()
	owner := "agy-confined-history-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "agy-cli", LandlockRunner: runner, PrivateHome: home, CredentialHome: os.Getenv("HOME"), WorkspaceReadPaths: []string{work}, WorkspaceWritePaths: []string{work}}
	security := func(o *llmtypes.CallOptions) { o.CLISecurity = policy }
	opts := []llmtypes.CallOption{WithWorkingDir(work), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), llmtypes.WithModel("gemini-3.8-flash-high"), WithNativeToolsMode("full_unconfined"), security}
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	token := "AGY_CONFINED_" + agyRandomHex(t, 4)
	response, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Remember this token: "+token+". Reply with only that token. Do not use tools.")}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if text := agySidecarChoiceText(t, response); strings.TrimSpace(text) != token {
		t.Fatalf("first answer: %q", text)
	}
	handle, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(response)
	if !ok || handle.NativeSessionID == "" {
		t.Fatal("missing durable conversation receipt")
	}
	if _, ok, err := ReadNativeTranscript(handle.NativeSessionID, home); err != nil || !ok {
		t.Fatalf("private durable transcript: %t %v", ok, err)
	}
	for turn := 0; turn < 2; turn++ {
		if turn == 1 {
			if out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", handle.TmuxSession).CombinedOutput(); err != nil {
				t.Fatalf("test session loss: %s %v", out, err)
			}
			opts = append(opts, WithResumeSessionID(handle.NativeSessionID))
		}
		response, err = adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "What token did I give you earlier? Reply with only that token followed by _FOLLOWUP. Do not use tools.")}, opts...)
		if err != nil {
			t.Fatalf("follow-up %d: %v", turn, err)
		}
		if text := agySidecarChoiceText(t, response); strings.TrimSpace(text) != token+"_FOLLOWUP" {
			t.Fatalf("follow-up %d: %q", turn, text)
		}
		next, ok := llmtypes.ExtractCodingProviderSessionHandleFromResponse(response)
		if !ok || next.NativeSessionID != handle.NativeSessionID {
			t.Fatalf("conversation changed: %+v", next)
		}
	}
	response, err = adapter.GenerateContent(ctx, []llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Use native run_command to execute exactly: printf AGY_NATIVE_PTY_OK > native-shell-proof.txt . Do not write the file with another tool. After the command succeeds, reply only AGY_NATIVE_PTY_OK.")}, opts...)
	if err != nil {
		t.Fatalf("native PTY command: %v", err)
	}
	if text := agySidecarChoiceText(t, response); strings.TrimSpace(text) != "AGY_NATIVE_PTY_OK" {
		t.Fatalf("native command reply: %q", text)
	}
	if raw, err := os.ReadFile(filepath.Join(work, "native-shell-proof.txt")); err != nil || string(raw) != "AGY_NATIVE_PTY_OK" {
		t.Fatalf("native shell did not execute: %q %v", raw, err)
	}
	found := false
	for _, call := range agyTurnToolCallsSince(handle.NativeSessionID, -1, home) {
		if call.Name == "run_command" && call.ErrorText == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("native trail has no successful run_command receipt")
	}
	t.Log("verified first turn, retained follow-up, resume, and actual native shell under Landlock")
}
