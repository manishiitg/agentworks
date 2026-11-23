package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func writeClaudeModTestEvents(t *testing.T, sessionName string, events ...claudeModEvent) {
	t.Helper()
	if _, ok := claudeModBaseFor(sessionName); !ok {
		dir := t.TempDir()
		claudeModBases.Store(sessionName, claudeModBase{dir: dir, inbox: filepath.Join(dir, "inbox"), private: true})
	}
	dir, err := prepareClaudeModSessionDir(sessionName)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, _ := json.Marshal(event)
		name := fmt.Sprintf("%014d-%06d-%s.json", event.At, event.Seq, event.Type)
		if err := os.WriteFile(filepath.Join(dir, "events", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// /clear typed in the terminal moved Claude to a new session; the chat kept
// watching and resuming the old one (why typed slash commands were blocked).
// The mod's events name the new session and the retained terminal adopts it,
// but only a session of its own project folder: anything in the session can
// write events, so a named session without a transcript there is refused.
func TestClaudeModFollowsSessionSwitchAndQueuedTurns(t *testing.T) {
	sessionName := fmt.Sprintf("mod-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { removeClaudeModSessionDir(sessionName) })
	start := time.Now().Add(-time.Second)
	at := start.UnixMilli() + 10
	const oldID, newID, foreignID = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	writeClaudeModTestEvents(t, sessionName,
		claudeModEvent{Type: "turn_start", At: at, Seq: 1, SessionID: oldID},
		claudeModEvent{Type: "turn_complete", At: at + 1, Seq: 2, SessionID: oldID, Answer: "first"},
		// A message queued during the first answer starts the next turn.
		claudeModEvent{Type: "turn_start", At: at + 2, Seq: 3, SessionID: oldID},
		claudeModEvent{Type: "turn_complete", At: at + 3, Seq: 4, SessionID: oldID, Answer: "second"},
		claudeModEvent{Type: "session_end", At: at + 4, Seq: 5, SessionID: oldID, Reason: "clear"},
		claudeModEvent{Type: "command", At: at + 5, Seq: 6, SessionID: newID, Command: "clear"},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	end, ok := waitForClaudeModTurnComplete(ctx, sessionName, start)
	if !ok || end.Answer != "second" {
		t.Fatalf("turn end = %+v, %v; want the queued turn's answer", end, ok)
	}

	home, workingDir := t.TempDir(), t.TempDir()
	session := &claudeInteractivePersistentSession{tmuxSessionName: sessionName, nativeSessionID: oldID, workingDir: workingDir, accountHome: home}
	adoptClaudeModSession(session, nil)
	if session.nativeSessionID != oldID {
		t.Fatalf("native session = %q, adopted a session with no transcript in this project", session.nativeSessionID)
	}
	transcript := claudeTranscriptWorkingDirCandidates(home, workingDir, newID)[0]
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	adoptClaudeModSession(session, nil)
	if session.nativeSessionID != newID {
		t.Fatalf("native session = %q, want the one /clear moved to", session.nativeSessionID)
	}
	writeClaudeModTestEvents(t, sessionName, claudeModEvent{Type: "command", At: at + 6, Seq: 7, SessionID: foreignID})
	adoptClaudeModSession(session, nil)
	if session.nativeSessionID != newID {
		t.Fatalf("native session = %q, adopted a session named from elsewhere", session.nativeSessionID)
	}
}

// The mod runs each inbox request as the person (a prompt, a slash command),
// so a confined agent must not be able to write one. A Crew launch works in its
// private runtime, which also holds the CLI home: everything under it is
// writable, so the inbox has to be outside the sandbox's whole write set, not
// just the mod's own write paths.
func TestClaudeModInboxIsOutsideTheConfinedWriteSet(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "slots", "slot01", "cli-runtimes", "v1", "digest")
	home := filepath.Join(runtimeDir, ".sandbox-cache", "cli-home", "claude-code")
	project := filepath.Join(root, "docs", "Crew", "abc")
	policy := &llmtypes.CLISecurityPolicy{Provider: "claude-code", PrivateHome: home, WorkspaceWritePaths: []string{project}}
	for _, dir := range []string{home, project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	sessionName := fmt.Sprintf("mod-inbox-test-%d", time.Now().UnixNano())
	inbox, plugin := t.TempDir(), t.TempDir()
	claudeModBases.Store(sessionName, claudeModBase{dir: filepath.Join(home, ".agentworks-mod"), inbox: inbox, plugin: plugin, shared: true})
	t.Cleanup(func() { claudeModBases.Delete(sessionName) })
	read, write := claudeModSandboxPaths([]string{"claude", "--plugin-dir", "/plugin"}, sessionName, nil, nil)
	if !slices.Contains(read, inbox) {
		t.Fatalf("read paths %v do not include the inbox %s", read, inbox)
	}
	writeSet := clisandbox.LandlockWritePaths(policy, runtimeDir, write)
	// The mod's code runs inside Claude Code with the login: the agent must not be able to change it either.
	for _, dir := range []string{inbox, plugin} {
		if clisandbox.WritableUnder(writeSet, dir) {
			t.Fatalf("%s is writable through the sandbox's write set %v", dir, writeSet)
		}
	}
	// The earlier place of the mod, inside the private home, is writable; on Linux a launch with it is refused.
	oldPlugin := filepath.Join(home, ".agentworks-mod")
	if !clisandbox.WritableUnder(writeSet, oldPlugin) {
		t.Fatalf("expected the private home's %s to be writable", oldPlugin)
	}
	if runtime.GOOS == "linux" {
		policy.LandlockRunner, policy.Mode = "/bin/true", llmtypes.CLISecurityModeIsolated
		if err := claudeModInboxOutsideWrites(policy, runtimeDir, write, sessionName); err != nil {
			t.Fatalf("launch refused with inbox and mod outside the write set: %v", err)
		}
		claudeModBases.Store(sessionName, claudeModBase{dir: oldPlugin, inbox: inbox, plugin: oldPlugin, shared: true})
		if err := claudeModInboxOutsideWrites(policy, runtimeDir, write, sessionName); err == nil {
			t.Fatal("a launch whose mod the agent could rewrite was not refused")
		}
	}
	// The earlier place, beside the private home, is inside the runtime the CLI works in.
	if beside := filepath.Join(filepath.Dir(home), ".agentworks-mod-inbox"); !clisandbox.WritableUnder(writeSet, beside) {
		t.Fatalf("expected %s to be writable through the working folder %s", beside, runtimeDir)
	}
}
