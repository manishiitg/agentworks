package server

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// The platform decides how coding CLIs run; there is no switch to forget.
// Only a person's own Mac runs them unconfined. Everywhere else they are
// locked, and if the lock cannot be applied the chat loses native tools
// rather than running unconfined.
func TestDecideCLIConfinement(t *testing.T) {
	origOS, origRunner, origSeatbelt := cliHostOS, cliLandlockRunner, cliSeatbeltAvailable
	t.Cleanup(func() { cliHostOS, cliLandlockRunner, cliSeatbeltAvailable = origOS, origRunner, origSeatbelt })
	cliSeatbeltAvailable = func() bool { return true }
	withLock := func() (string, bool) { return "/usr/lib/agentworks/landlock-run", true }
	noLock := func() (string, bool) { return "", false }

	cases := []struct {
		name       string
		provider   string
		os         string
		multiUser  string
		workingDir string
		runner     func() (string, bool)
		want       cliRunDecision
	}{
		{"own Mac, Claude: Seatbelt", "claude-code", "darwin", "", "/w", noLock, cliRunSeatbelt},
		{"own Mac, a CLI not certified for Seatbelt", "codex-cli", "darwin", "", "/w", noLock, cliRunUnconfined},
		{"own Mac, no working folder", "claude-code", "darwin", "", "", noLock, cliRunUnconfined},
		{"multi-user Mac never runs unconfined", "claude-code", "darwin", "true", "/w", noLock, cliRunBridgeOnly},
		{"Linux server with the lock", "claude-code", "linux", "true", "/w", withLock, cliRunConfined},
		{"Linux without MULTI_USER_MODE still locks", "codex-cli", "linux", "", "/w", withLock, cliRunConfined},
		{"Linux whose lock is broken fails closed", "claude-code", "linux", "true", "/w", noLock, cliRunBridgeOnly},
		{"Linux with no working folder fails closed", "claude-code", "linux", "true", "", withLock, cliRunBridgeOnly},
	}
	for _, tc := range cases {
		cliHostOS, cliLandlockRunner = tc.os, tc.runner
		t.Setenv("MULTI_USER_MODE", tc.multiUser)
		got, runner, why := decideCLIConfinement(tc.provider, tc.workingDir)
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
		if got == cliRunConfined && runner == "" {
			t.Errorf("%s: confined without a launcher", tc.name)
		}
		if got == cliRunBridgeOnly && why == "" {
			t.Errorf("%s: fell back without saying why", tc.name)
		}
	}
}

// A Mac without sandbox-exec keeps the old behaviour (Full CLI unconfined).
func TestDecideCLIConfinementWithoutSandboxExec(t *testing.T) {
	origOS, origSeatbelt := cliHostOS, cliSeatbeltAvailable
	t.Cleanup(func() { cliHostOS, cliSeatbeltAvailable = origOS, origSeatbelt })
	cliHostOS, cliSeatbeltAvailable = "darwin", func() bool { return false }
	t.Setenv("MULTI_USER_MODE", "")
	if got, _, _ := decideCLIConfinement("claude-code", "/w"); got != cliRunUnconfined {
		t.Fatalf("got %s, want unconfined", got)
	}
}

// Host grants are absolute and must reach the sandbox policy as they are;
// workspace entries join the docs root; blocked paths travel with the policy.
func TestCLISandboxPolicyPaths(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	origOS := cliHostOS
	t.Cleanup(func() { cliHostOS = origOS })
	cliHostOS = "darwin"
	t.Setenv("MULTI_USER_MODE", "")
	const session = "cli-policy-paths-test"
	common.SetSessionFolderGuard(session, []string{"Workflow/w"}, []string{"Workflow/w", "/Users/someone/Downloads"})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	common.SetSessionFolderGuardBlockedPaths(session, []string{"Workflow/w/db/db.sqlite"})
	common.SetSessionFolderGuardBlockedWritePaths(session, []string{"Workflow/w/planning/"})

	policy := cliLandlockPolicyForSession(session, "claude-code", "/w/run", nil)
	has := func(list []string, want string) bool {
		for _, p := range list {
			if p == want {
				return true
			}
		}
		return false
	}
	if !has(policy.WorkspaceWritePaths, "/Users/someone/Downloads") {
		t.Errorf("absolute host grant mangled: %v", policy.WorkspaceWritePaths)
	}
	if !has(policy.WorkspaceWritePaths, codingAgentWorkspaceWorkingDir("Workflow/w")) {
		t.Errorf("workspace grant missing: %v", policy.WorkspaceWritePaths)
	}
	if !has(policy.BlockedPaths, codingAgentWorkspaceWorkingDir("Workflow/w/db/db.sqlite")) {
		t.Errorf("blocked path missing: %v", policy.BlockedPaths)
	}
	if !has(policy.BlockedWritePaths, codingAgentWorkspaceWorkingDir("Workflow/w/planning/")) {
		t.Errorf("blocked write path missing: %v", policy.BlockedWritePaths)
	}
}

// On a server an absolute host grant never reaches the CLI's sandbox policy.
func TestCLISandboxPolicyDropsHostGrantsOnServers(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	origOS := cliHostOS
	t.Cleanup(func() { cliHostOS = origOS })
	const session = "cli-policy-server-test"
	common.SetSessionFolderGuard(session, []string{"Workflow/w", "/srv/secrets"}, []string{"Workflow/w", "/home/someone"})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	for _, tc := range []struct{ os, multiUser string }{{"linux", ""}, {"linux", "true"}, {"darwin", "true"}} {
		cliHostOS = tc.os
		t.Setenv("MULTI_USER_MODE", tc.multiUser)
		policy := cliLandlockPolicyForSession(session, "claude-code", "/w/run", nil)
		for _, p := range append(append([]string{}, policy.WorkspaceReadPaths...), policy.WorkspaceWritePaths...) {
			if p == "/srv/secrets" || p == "/home/someone" {
				t.Errorf("%s multi-user=%q: host grant %s reached the policy", tc.os, tc.multiUser, p)
			}
		}
	}
}
