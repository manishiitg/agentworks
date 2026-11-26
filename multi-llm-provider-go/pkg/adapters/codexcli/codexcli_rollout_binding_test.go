package codexcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-106 root cause: retained-answer lookup resolved a transcript by working
// directory + newest modification time. A workflow's interactive Chat and its
// scheduled run execute in the SAME directory, so the newest rollout there
// frequently belongs to the other conversation. The wrong final answer was then
// returned and stamped with the requesting session's IDs, which is why no
// frontend guard could detect it.

func writeRollout(t *testing.T, root, threadID, cwd, finalText string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "2026", "08", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fmt.Sprintf("rollout-2026-08-15T10-00-00-%s.jsonl", threadID))

	stamp := modTime.UTC().Format(time.RFC3339Nano)
	body := fmt.Sprintf(
		"{\"timestamp\":%q,\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q}}\n"+
			"{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"last_agent_message\":%q}}\n",
		stamp, threadID, cwd, stamp, finalText,
	)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRetainedLookupDoesNotReturnAnotherSessionsAnswer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessionsRoot := filepath.Join(root, "sessions")

	const sharedWorkingDir = "/workspace/Workflow/build-in-public"
	turnStart := time.Now().Add(-time.Minute)

	// The Chat session's own transcript, written FIRST.
	chatPath := writeRollout(t, sessionsRoot, "11111111-1111-4111-8111-111111111111",
		sharedWorkingDir, "Chat answer for the user's question.", time.Now().Add(-30*time.Second))

	// The Schedule session writes to the same directory more recently. Under the
	// old newest-mtime rule this is what a Chat lookup would have returned.
	writeRollout(t, sessionsRoot, "22222222-2222-4222-8222-222222222222",
		sharedWorkingDir, "Schedule answer about durable Pulse state.", time.Now())

	chatSession := &codexInteractiveSession{
		ownerSessionID: "chat-build-in-public",
		workingDir:     sharedWorkingDir,
		threadID:       "11111111-1111-4111-8111-111111111111",
	}

	resolved := resolveCodexRolloutPath(chatSession, turnStart)
	if resolved != chatPath {
		t.Fatalf("resolved rollout = %q, want the Chat session's own rollout %q", resolved, chatPath)
	}

	final, _ := readCodexRolloutFinalAssistantText(resolved, turnStart)
	if final != "Chat answer for the user's question." {
		t.Fatalf("final answer = %q, want the Chat session's own answer", final)
	}
	f, err := os.OpenFile(chatPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(f, "{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Checking the Chat request.\"}}\n", time.Now().UTC().Format(time.RFC3339Nano))
	if closeErr := f.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	old := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{chatSession.ownerSessionID: chatSession})
	t.Cleanup(func() { codexPersistentRegistry.Replace(old) })
	updates := ReadRetainedTurnProgressMessages(chatSession.ownerSessionID, turnStart)
	if len(updates) != 1 || updates[0].Parts[0].(llmtypes.TextContent).Text != "Checking the Chat request." {
		t.Fatalf("progress must use this session's rollout: %+v", updates)
	}
}

// Demonstrates the defect directly: the unbound directory+recency rule picks the
// most recently written rollout, which is the other session's.
func TestDirectoryRecencyRuleIsAmbiguousForSharedWorkingDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessionsRoot := filepath.Join(root, "sessions")

	const sharedWorkingDir = "/workspace/Workflow/build-in-public"
	turnStart := time.Now().Add(-time.Minute)

	writeRollout(t, sessionsRoot, "11111111-1111-4111-8111-111111111111",
		sharedWorkingDir, "Chat answer.", time.Now().Add(-30*time.Second))
	schedulePath := writeRollout(t, sessionsRoot, "22222222-2222-4222-8222-222222222222",
		sharedWorkingDir, "Schedule answer.", time.Now())

	if got := findCodexRolloutByWorkingDirUnsafe(turnStart, sharedWorkingDir); got != schedulePath {
		t.Fatalf("directory match = %q, expected it to select the newest (%q) — this documents why binding is required", got, schedulePath)
	}

	// Excluding a rollout already claimed by another live session is what makes
	// the unbound path safe before a thread ID is known.
	claimed := map[string]bool{schedulePath: true}
	got := findCodexRolloutByWorkingDirExcluding(turnStart, sharedWorkingDir, claimed)
	if got == schedulePath || got == "" {
		t.Fatalf("excluded lookup = %q, want the Chat rollout", got)
	}
}

// PLAT-108: completion detection previously resolved its rollout by working
// directory too, so a Chat turn could be declared complete by the Schedule's
// task_complete — or follow the wrong transcript entirely.
func TestCompletionTrackerFollowsOnlyItsOwnSessionRollout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessionsRoot := filepath.Join(root, "sessions")

	const sharedWorkingDir = "/workspace/Workflow/build-in-public"
	turnStart := time.Now().Add(-time.Minute)

	// This session's rollout has NOT completed: no task_complete row.
	chatPath := filepath.Join(sessionsRoot, "2026", "08", "15",
		"rollout-2026-08-15T10-00-00-11111111-1111-4111-8111-111111111111.jsonl")
	if err := os.MkdirAll(filepath.Dir(chatPath), 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	chatBody := fmt.Sprintf(
		"{\"timestamp\":%q,\"type\":\"session_meta\",\"payload\":{\"id\":\"11111111-1111-4111-8111-111111111111\",\"cwd\":%q}}\n",
		stamp, sharedWorkingDir)
	if err := os.WriteFile(chatPath, []byte(chatBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// The Schedule session, same directory, DID complete and wrote more recently.
	writeRollout(t, sessionsRoot, "22222222-2222-4222-8222-222222222222",
		sharedWorkingDir, "Schedule finished.", time.Now().Add(time.Second))

	chatSession := &codexInteractiveSession{
		ownerSessionID: "chat-build-in-public",
		workingDir:     sharedWorkingDir,
		threadID:       "11111111-1111-4111-8111-111111111111",
	}

	bound := newCodexTurnCompletionTracker(turnStart, sharedWorkingDir, codexRolloutResolverForSession(chatSession))
	if bound.completed() {
		t.Fatal("bound tracker reported completion from another session's task_complete")
	}
	if bound.rolloutPath != chatPath {
		t.Fatalf("bound tracker followed %q, want its own rollout %q", bound.rolloutPath, chatPath)
	}

	// Without binding, the same tracker latches onto the Schedule's rollout —
	// documenting the defect this resolver removes.
	unbound := newCodexTurnCompletionTracker(turnStart, sharedWorkingDir, nil)
	if !unbound.completed() {
		t.Fatal("expected the unbound tracker to be fooled by the newer foreign rollout")
	}
}

func TestResolveBindsThreadIDOnFirstUseAndStaysStable(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessionsRoot := filepath.Join(root, "sessions")

	const workingDir = "/workspace/Workflow/solo"
	turnStart := time.Now().Add(-time.Minute)
	path := writeRollout(t, sessionsRoot, "33333333-3333-4333-8333-333333333333",
		workingDir, "Only answer.", time.Now())

	session := &codexInteractiveSession{ownerSessionID: "solo", workingDir: workingDir}

	if got := resolveCodexRolloutPath(session, turnStart); got != path {
		t.Fatalf("first resolve = %q, want %q", got, path)
	}
	session.mu.Lock()
	boundThread := session.threadID
	session.mu.Unlock()
	if boundThread != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("thread ID was not pinned on first resolve: %q", boundThread)
	}

	// A newer rollout for a DIFFERENT conversation appears in the same directory.
	writeRollout(t, sessionsRoot, "44444444-4444-4444-8444-444444444444",
		workingDir, "Someone else's answer.", time.Now().Add(time.Minute))

	if got := resolveCodexRolloutPath(session, turnStart); got != path {
		t.Fatalf("resolve after a newer foreign rollout = %q, want the pinned %q", got, path)
	}
}

// A structured run must not be completed by ANOTHER Codex run's finished turn in the same folder
// (the schedule's own chat session next to a step run: step failed with "exit status 1", 2026-10-05).
func TestStructuredCompletionIgnoresAnotherRunInTheSameFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	sessionsRoot := filepath.Join(root, "sessions")
	const sharedWorkingDir = "/workspace/Workflow/websiteaeo"
	const ownID = "33333333-3333-4333-8333-333333333333"
	turnStart := time.Now().Add(-time.Minute)

	// The neighbour (chat session) finished a turn in the same folder after our run started.
	writeRollout(t, sessionsRoot, "44444444-4444-4444-8444-444444444444", sharedWorkingDir, "neighbour", time.Now())

	ownID_ := ""
	tracker := newCodexTurnCompletionTracker(turnStart, sharedWorkingDir, codexOwnThreadRolloutResolver(func() string { return ownID_ }))
	if tracker.completed() {
		t.Fatal("completed before the run announced its thread id")
	}
	ownID_ = ownID
	if tracker.completed() {
		t.Fatal("the neighbour's finished turn completed this run")
	}
	writeRollout(t, sessionsRoot, ownID, sharedWorkingDir, "own answer", time.Now())
	if !tracker.completed() {
		t.Fatal("the run's own task_complete was not seen")
	}
}
