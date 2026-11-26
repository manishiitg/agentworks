package mcpagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// A coding CLI keys its resumable conversation by working directory (claude
// files it under ~/.claude/projects/<slugified-cwd>/). A new Agent is built for
// EVERY turn, so if the isolated workspace were minted per Agent, each turn
// would run in a different cwd and native --resume could never find the prior
// turn's conversation — the observed failure was "No conversation found with
// session ID", one orphaned conversation dir leaked per turn.
func TestIsolatedWorkspaceDirIsStableAcrossTurnsOfSameSession(t *testing.T) {
	const sessionID = "msgseq-iteration-0-job-search-step-4-search-find-and-shortlist"

	turn1 := &Agent{sessionID: sessionID, isolatedSessionWorkspace: true}
	turn2 := &Agent{sessionID: sessionID, isolatedSessionWorkspace: true}

	dir1 := turn1.ensureIsolatedWorkspaceDir()
	dir2 := turn2.ensureIsolatedWorkspaceDir()
	t.Cleanup(func() { _ = os.RemoveAll(dir1) })

	if dir1 == "" {
		t.Fatal("expected an isolated workspace dir for a session with an ID")
	}
	if dir1 != dir2 {
		t.Fatalf("same session must resolve to the same dir across turns; got %q then %q", dir1, dir2)
	}
	if _, err := os.Stat(dir1); err != nil {
		t.Fatalf("isolated workspace dir must exist on disk: %v", err)
	}
}

// Isolation is the whole point of the feature: distinct sessions (e.g. two
// concurrent workflow steps) must never share a workspace.
func TestIsolatedWorkspaceDirDiffersAcrossSessions(t *testing.T) {
	stepA := &Agent{sessionID: "step-a", isolatedSessionWorkspace: true}
	stepB := &Agent{sessionID: "step-b", isolatedSessionWorkspace: true}

	dirA := stepA.ensureIsolatedWorkspaceDir()
	dirB := stepB.ensureIsolatedWorkspaceDir()
	t.Cleanup(func() { _ = os.RemoveAll(dirA); _ = os.RemoveAll(dirB) })

	if dirA == "" || dirB == "" {
		t.Fatal("expected isolated dirs for both sessions")
	}
	if dirA == dirB {
		t.Fatalf("distinct sessions must not share an isolated workspace; both got %q", dirA)
	}
}

// Session IDs contain '/' and other path metacharacters; the derived path must
// stay a single directory under TempDir rather than escaping into subpaths.
func TestIsolatedWorkspaceDirSanitizesSessionID(t *testing.T) {
	dir := isolatedWorkspaceDirForSession("group/../../etc/passwd step 4")
	if dir == "" {
		t.Fatal("expected a derived dir")
	}
	if filepath.Dir(dir) != filepath.Clean(os.TempDir()) {
		t.Fatalf("derived dir must sit directly under TempDir, got %q", dir)
	}
	if !strings.HasPrefix(filepath.Base(dir), "mlp-cli-session-") {
		t.Fatalf("derived dir must keep the recognizable prefix, got %q", filepath.Base(dir))
	}
}

// No session ID means the session could never be resumed anyway, so it keeps
// the historical random-dir behaviour — and stays eligible for rm -rf on Close.
func TestIsolatedWorkspaceWithoutSessionIDIsRandomAndDisposable(t *testing.T) {
	a := &Agent{isolatedSessionWorkspace: true}
	dir := a.ensureIsolatedWorkspaceDir()
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if dir == "" {
		t.Fatal("expected a fallback random dir")
	}
	if a.isolatedWorkspaceStable {
		t.Fatal("a random fallback dir must not be marked stable; Agent.Close still owns removing it")
	}
}

// The bug that started this: resume fell back to CodingAgentWorkingDir even
// when the conversation had been created inside an isolated workspace, so the
// CLI looked for it under the wrong project key and reported it missing.
func TestContinuationHandleResumesIntoIsolatedWorkspace(t *testing.T) {
	a := &Agent{
		sessionID:                "resume-into-isolated",
		isolatedSessionWorkspace: true,
		codingAgentWorkingDir:    "/Users/someone/workspace-docs/Workflow/upwork",
	}
	isolated := a.ensureIsolatedWorkspaceDir()
	t.Cleanup(func() { _ = os.RemoveAll(isolated) })

	if got := a.resolveContinuationWorkingDir(""); got != isolated {
		t.Fatalf("isolated session must resume into its own workspace %q, got %q", isolated, got)
	}
	// A handle that already recorded its dir stays authoritative.
	if got := a.resolveContinuationWorkingDir("/explicit/dir"); got != "/explicit/dir" {
		t.Fatalf("an explicit handle WorkingDir must win, got %q", got)
	}
}

// Non-isolated (chat) sessions must keep operating directly on the user's
// chosen workspace — that's the "agent edits my files" UX.
func TestContinuationHandleKeepsRealWorkingDirWhenNotIsolated(t *testing.T) {
	a := &Agent{
		sessionID:             "chat-session",
		codingAgentWorkingDir: "/Users/someone/workspace-docs/Workflow/upwork",
	}
	if got := a.resolveContinuationWorkingDir(""); got != a.codingAgentWorkingDir {
		t.Fatalf("non-isolated session must resume in the real workspace, got %q", got)
	}
}

// Covers the tmux/interactive shape, where the adapter DOES record the cwd in
// the handle (claudecode_interactive_adapter.go sets WorkingDir). That path
// never hits the empty-WorkingDir fallback, so it is fixed by a different
// property: the dir must still be the same one AND still exist on the next
// turn. Previously it was neither — a fresh random dir per Agent, rm -rf'd by
// Agent.Close — so a recorded handle pointed at a deleted directory.
func TestRecordedWorkingDirStillValidOnNextTurn(t *testing.T) {
	const sessionID = "tmux-shaped-session"

	turn1 := &Agent{logger: loggerv2.NewDefault(), sessionID: sessionID, isolatedSessionWorkspace: true}
	recorded := turn1.ensureIsolatedWorkspaceDir() // what the adapter would persist
	t.Cleanup(func() { CloseSession(sessionID) })
	if recorded == "" {
		t.Fatal("expected an isolated workspace dir")
	}
	_ = turn1.Close() // end of turn 1 — must NOT destroy the session's workspace

	if _, err := os.Stat(recorded); err != nil {
		t.Fatalf("dir recorded in the handle must survive between turns: %v", err)
	}

	// Turn 2: a brand-new Agent resumes with the recorded handle dir.
	turn2 := &Agent{logger: loggerv2.NewDefault(), sessionID: sessionID, isolatedSessionWorkspace: true}
	if got := turn2.resolveContinuationWorkingDir(recorded); got != recorded {
		t.Fatalf("resume must return to the recorded dir; want %q got %q", recorded, got)
	}
	// ...and its own derivation agrees, so both transports converge on one dir.
	if got := turn2.ensureIsolatedWorkspaceDir(); got != recorded {
		t.Fatalf("derived dir must match the recorded one; want %q got %q", recorded, got)
	}
}
