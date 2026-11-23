package mcpagent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func isolatedWorkspaceTestAgent() *Agent {
	return &Agent{logger: loggerv2.NewDefault(), sessionID: "isolated-workspace-test"}
}

// TestEnsureIsolatedWorkspaceDirCreatesTmpDirOnceAndCleansUpOnClose
// covers the lifecycle invariants the workflow-step isolation
// feature depends on:
//
//  1. ensureIsolatedWorkspaceDir creates a tmp dir under the OS
//     tmp location, named "mlp-cli-session-*" so it's recognizable
//     in `ls /tmp`.
//  2. The dir exists between creation and Close.
//  3. Repeated calls return the SAME dir (sync.Once guarantee).
//  4. Agent.Close does NOT remove a session-derived dir; CloseSession does.
//
// Invariant 4 previously read "Agent.Close rm -rf's the dir". That was the
// bug: a new Agent is constructed for EVERY turn, so Agent.Close runs between
// turns of a live session. Removing the workspace there destroyed the coding
// CLI's resumable conversation, and the next turn failed with "No conversation
// found with session ID" and silently restarted as a fresh conversation.
// Session-scoped dirs now live until CloseSession — the real end of session.
func TestEnsureIsolatedWorkspaceDirCreatesTmpDirOnceAndCleansUpOnClose(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.isolatedSessionWorkspace = true
	t.Cleanup(func() { CloseSession(a.sessionID) })

	dir1 := a.ensureIsolatedWorkspaceDir()
	if dir1 == "" {
		t.Fatal("ensureIsolatedWorkspaceDir returned empty path on first call; tmp-dir creation failed silently")
	}
	if !strings.Contains(filepath.Base(dir1), "mlp-cli-session-") {
		t.Errorf("tmp dir name must include the mlp-cli-session-* prefix so leaked dirs are recognizable; got %q", filepath.Base(dir1))
	}
	if info, err := os.Stat(dir1); err != nil {
		t.Fatalf("expected tmp dir %q to exist after creation: %v", dir1, err)
	} else if !info.IsDir() {
		t.Fatalf("expected %q to be a directory; got mode %v", dir1, info.Mode())
	}

	// Repeated calls must return the SAME dir. The sync.Once gate
	// prevents per-call dir creation which would leak dirs and break
	// session-scoped state.
	dir2 := a.ensureIsolatedWorkspaceDir()
	if dir2 != dir1 {
		t.Errorf("repeated ensureIsolatedWorkspaceDir must return the same dir; got %q then %q", dir1, dir2)
	}

	// Closing one turn's Agent must leave the session's workspace intact, or
	// the next turn cannot resume the conversation that lives inside it.
	_ = a.Close()
	if _, err := os.Stat(dir1); err != nil {
		t.Errorf("Agent.Close must NOT remove a session-derived workspace (the next turn resumes into it); stat err=%v (dir=%q)", err, dir1)
	}

	// CloseSession is the real end of the session and reclaims the dir.
	CloseSession(a.sessionID)
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Errorf("CloseSession must rm -rf the isolated workspace dir; stat err=%v (dir=%q)", err, dir1)
	}
}

// The random fallback dir (no session ID, so never resumable) keeps the
// original lifecycle: Agent.Close still reclaims it immediately.
func TestAgentCloseRemovesRandomFallbackWorkspace(t *testing.T) {
	a := &Agent{logger: loggerv2.NewDefault()} // no SessionID
	a.isolatedSessionWorkspace = true

	dir := a.ensureIsolatedWorkspaceDir()
	if dir == "" {
		t.Fatal("expected a random fallback workspace dir")
	}
	_ = a.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Agent.Close must rm -rf a non-resumable random workspace; stat err=%v (dir=%q)", err, dir)
	}
}

// TestEnsureIsolatedWorkspaceDirRespectsFlag asserts that when
// IsolatedSessionWorkspace is false (the chat-mode default),
// ensureIsolatedWorkspaceDir is never invoked by the option
// appender. Without this guarantee, chat sessions would silently
// get a tmp dir override and lose the "agent edits my files" UX.
func TestEnsureIsolatedWorkspaceDirRespectsFlag(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.isolatedSessionWorkspace = false
	a.codingAgentWorkingDir = "/Users/test/workspace"

	opts := a.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli")
	if a.isolatedWorkspacePath != "" {
		t.Errorf("isolated workspace dir must NOT be created when flag is off; got %q", a.isolatedWorkspacePath)
	}
	// The CodingAgentWorkingDir option should still be appended for chat-mode usage.
	got := metadataFromCallOptions(opts)
	wd, _ := got["cursor_working_dir"].(string)
	if wd != "/Users/test/workspace" {
		t.Errorf("chat mode must pass CodingAgentWorkingDir through verbatim; got %q want %q", wd, "/Users/test/workspace")
	}
}

// TestAppendCodingAgentWorkingDirOverridesWithIsolatedTmpDir is the
// observable contract: when the flag is on, the cursor working-dir
// option carries the tmp dir path, NOT the operator-supplied
// CodingAgentWorkingDir. This is what protects the user's actual
// workspace from accidental model writes.
func TestAppendCodingAgentWorkingDirOverridesWithIsolatedTmpDir(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.isolatedSessionWorkspace = true
	a.codingAgentWorkingDir = "/Users/test/workspace"
	defer a.Close()

	opts := a.appendCodingAgentWorkingDirOptionForProvider(nil, llm.ProviderCursorCLI, "cursor-cli")
	got := metadataFromCallOptions(opts)
	wd, _ := got["cursor_working_dir"].(string)
	if wd == "" || wd == "/Users/test/workspace" {
		t.Errorf("isolation must override CodingAgentWorkingDir with a tmp path; got %q", wd)
	}
	if !strings.Contains(filepath.Base(wd), "mlp-cli-session-") {
		t.Errorf("isolation override must use mlp-cli-session-* tmp dir; got %q", wd)
	}
	if wd != a.isolatedWorkspacePath {
		t.Errorf("option value must match the Agent's stored isolated path; option=%q field=%q", wd, a.isolatedWorkspacePath)
	}
}

// TestWithIsolatedSessionWorkspaceOptionThreadsThroughField asserts
// the public agentOption wires the bool onto the Agent struct
// correctly. Belt-and-suspenders against future field renames.
func TestWithIsolatedSessionWorkspaceOptionThreadsThroughField(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	withIsolatedSessionWorkspace(true)(a)
	if !a.isolatedSessionWorkspace {
		t.Error("withIsolatedSessionWorkspace(true) must set IsolatedSessionWorkspace=true")
	}
	withIsolatedSessionWorkspace(false)(a)
	if a.isolatedSessionWorkspace {
		t.Error("withIsolatedSessionWorkspace(false) must set IsolatedSessionWorkspace=false")
	}
}

// TestIsolatedWorkspaceDirConcurrencyCreatesOnlyOneDir guards the
// sync.Once contract under concurrent option-appending. If the
// sync.Once were dropped (e.g. replaced with a plain bool check),
// concurrent goroutines could each create their own tmp dir,
// leak all but one, and the cleanup would only rm -rf the last
// one assigned.
func TestIsolatedWorkspaceDirConcurrencyCreatesOnlyOneDir(t *testing.T) {
	a := isolatedWorkspaceTestAgent()
	a.isolatedSessionWorkspace = true
	defer a.Close()

	const goroutines = 32
	var wg sync.WaitGroup
	results := make([]string, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = a.ensureIsolatedWorkspaceDir()
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == "" {
		t.Fatal("concurrent calls all returned empty; tmp-dir creation failed")
	}
	for i, got := range results {
		if got != first {
			t.Errorf("concurrent call %d returned different dir than first; got %q want %q", i, got, first)
		}
	}
}

// metadataFromCallOptions is defined in coding_agent_options_test.go;
// we reuse it via this package-level shared helper.
