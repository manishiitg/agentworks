package common

import (
	"slices"
	"strings"
	"testing"
)

func TestResolveBrowserSessionID(t *testing.T) {
	sessionID := "chat-session-test"
	SetSessionBrowserSessionID(sessionID, "workflow-browser-session")
	defer ClearSessionShellConfig(sessionID)

	if got := ResolveBrowserSessionID(sessionID, "default"); got != "workflow-browser-session" {
		t.Fatalf("default browser session should resolve to shared browser session, got %q", got)
	}

	if got := ResolveBrowserSessionID(sessionID, "isolated-123"); got != "isolated-123" {
		t.Fatalf("explicit non-default browser session should win, got %q", got)
	}
}

func TestResolveBrowserSessionIDUsesInstancePrefix(t *testing.T) {
	t.Setenv("AGENTWORKS_BROWSER_SESSION_PREFIX", "video-product-dev")

	if got := ResolveBrowserSessionID("", "isolated-123"); got != "video-product-dev--isolated-123" {
		t.Fatalf("explicit browser session should be instance-qualified, got %q", got)
	}
	if got := ResolveBrowserSessionID("", "video-product-dev--isolated-123"); got != "video-product-dev--isolated-123" {
		t.Fatalf("already-qualified browser session should not be prefixed twice, got %q", got)
	}
}

func TestBrowserSessionsSharedAcrossWorkflowUsersAndIsolatedAcrossWorkflows(t *testing.T) {
	manishChat := "browser-isolation-manish"
	shubhamChat := "browser-isolation-shubham"
	secondManishChat := "browser-isolation-manish-two"
	otherWorkflowChat := "browser-isolation-manish-other-workflow"
	for _, sessionID := range []string{manishChat, shubhamChat, secondManishChat, otherWorkflowChat} {
		t.Cleanup(func() { ClearSessionShellConfig(sessionID) })
	}

	BindSessionBrowserIsolationForWorkflow(manishChat, "Workflow/research")
	BindSessionBrowserIsolationForWorkflow(shubhamChat, "Workflow/research")
	BindSessionBrowserIsolationForWorkflow(secondManishChat, "Workflow/research")
	BindSessionBrowserIsolationForWorkflow(otherWorkflowChat, "Workflow/publishing")

	manishDefault := ResolveBrowserSessionID(manishChat, "default")
	shubhamDefault := ResolveBrowserSessionID(shubhamChat, "default")
	if manishDefault != shubhamDefault {
		t.Fatalf("authorized users of the same workflow got different browsers: %q != %q", manishDefault, shubhamDefault)
	}
	if manishDefault != ResolveBrowserSessionID(secondManishChat, "default") {
		t.Fatalf("same user and workflow got different browsers: %q", manishDefault)
	}
	if manishDefault == ResolveBrowserSessionID(otherWorkflowChat, "default") {
		t.Fatalf("different workflows shared browser %q", manishDefault)
	}
	if ResolveBrowserSessionID(manishChat, "research") != ResolveBrowserSessionID(shubhamChat, "another-label") {
		t.Fatal("session labels or user identity split one workflow browser")
	}
}

func TestBindSessionBrowserIsolationOverridesWorkflowDefault(t *testing.T) {
	sessionID := "browser-isolation-workflow"
	t.Cleanup(func() { ClearSessionShellConfig(sessionID) })
	BindSessionBrowserIsolationForWorkflow(sessionID, "Workflow/research")
	SetSessionBrowserSessionID(sessionID, "workflow-browser-stable")

	BindSessionBrowserIsolationForWorkflow(sessionID, "Workflow/research")
	if got := ResolveBrowserSessionID(sessionID, "default"); got != PrefixBrowserSessionID(WorkflowBrowserSessionNamespace("Workflow/research")+"--browser") {
		t.Fatalf("workflow override escaped user browser: %q", got)
	}
}

func TestWorkflowBrowserSessionNamespaceNormalizesWorkflowPath(t *testing.T) {
	want := WorkflowBrowserSessionNamespace("Workflow/research")
	for _, path := range []string{"/Workflow/research/", "Workflow//research", `Workflow\research`} {
		if got := WorkflowBrowserSessionNamespace(path); got != want {
			t.Fatalf("path %q resolved to %q, want %q", path, got, want)
		}
	}
	if got := WorkflowBrowserSessionNamespace("Workflow/publishing"); got == want {
		t.Fatalf("different workflows shared namespace %q", got)
	}
}

func TestProjectBrowserSessionNamespaceIsPerProjectNotPerUser(t *testing.T) {
	key := "_users/alice/Chats/Work/projects/crew-a"
	want := ProjectBrowserSessionNamespace(key)
	for _, path := range []string{"/_users/alice/Chats/Work/projects/crew-a/", "_users/alice/Chats/Work//projects/crew-a", `_users\alice\Chats\Work\projects\crew-a`} {
		if got := ProjectBrowserSessionNamespace(path); got != want {
			t.Fatalf("path %q resolved to %q, want %q", path, got, want)
		}
	}
	if got := ProjectBrowserSessionNamespace("_users/alice/Chats/Work/projects/crew-b"); got == want {
		t.Fatalf("different Crew projects shared namespace %q", got)
	}
	if got := ProjectBrowserSessionNamespace("_users/bob/Chats/Work/projects/crew-a"); got == want {
		t.Fatalf("two owners' same-named projects shared namespace %q", got)
	}
	if got := WorkflowBrowserSessionNamespace(key); got == want {
		t.Fatalf("project and workflow browsers collided: %q", got)
	}
	// Two users binding the same project get the same browser.
	aliceChat, bobChat := "project-alice-chat", "project-bob-chat"
	t.Cleanup(func() { ClearSessionShellConfig(aliceChat); ClearSessionShellConfig(bobChat) })
	BindSessionBrowserIsolationForProject(aliceChat, key)
	BindSessionBrowserIsolationForProject(bobChat, key)
	if a, b := ResolveBrowserSessionID(aliceChat, "default"), ResolveBrowserSessionID(bobChat, "default"); a != b {
		t.Fatalf("users of one project got different browsers: %q vs %q", a, b)
	}
}

func TestBrowserSessionIsolationComposesWithDeploymentPrefix(t *testing.T) {
	t.Setenv("AGENTWORKS_BROWSER_SESSION_PREFIX", "customer")
	sessionID := "browser-isolation-prefix"
	t.Cleanup(func() { ClearSessionShellConfig(sessionID) })
	BindSessionBrowserIsolationForSession(sessionID)

	got := ResolveBrowserSessionID(sessionID, "default")
	if !strings.HasPrefix(got, "customer--session-") {
		t.Fatalf("deployment prefix was not applied to isolated browser: %q", got)
	}
}

func TestGrantSessionCDPHostDownloadsReadWriteWidensPriorReadOnlyGrant(t *testing.T) {
	t.Setenv("PI_HOST_DOWNLOADS_PATH", "/tmp/test-host-downloads")
	sessionID := "cdp-host-downloads-read-write"
	t.Cleanup(func() { ClearSessionShellConfig(sessionID) })
	SetSessionFolderGuard(sessionID, []string{"Workflow/test"}, []string{"Workflow/test"})
	SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"/tmp/test-host-downloads"})

	if got := GrantSessionCDPHostDownloadsReadWrite(sessionID, "cdp"); got != "/tmp/test-host-downloads" {
		t.Fatalf("grant path = %q", got)
	}
	config := GetSessionShellConfig(sessionID)
	if !slices.Contains(config.ReadPaths, "/tmp/test-host-downloads") || !slices.Contains(config.WritePaths, "/tmp/test-host-downloads") {
		t.Fatalf("host Downloads was not added to read and write paths: %#v", config)
	}
	if slices.Contains(config.BlockedWritePaths, "/tmp/test-host-downloads") {
		t.Fatalf("stale read-only deny remained after widening: %#v", config.BlockedWritePaths)
	}
	if got := GrantSessionCDPHostDownloadsReadWrite(sessionID, "headless"); got != "" {
		t.Fatalf("headless mode unexpectedly granted host Downloads: %q", got)
	}
}

func TestPopulateMCPBridgeShortEnv(t *testing.T) {
	env := map[string]string{
		"MCP_API_URL":   "http://example.test/s/session-1/",
		"MCP_API_TOKEN": "test-token",
	}

	PopulateMCPBridgeShortEnv(env)

	want := map[string]string{
		"MCP_AUTH":    "Authorization: Bearer test-token",
		"MCP_MCP":     "http://example.test/s/session-1/tools/mcp",
		"MCP_CUSTOM":  "http://example.test/s/session-1/tools/custom",
		"MCP_VIRTUAL": "http://example.test/s/session-1/tools/virtual",
	}
	for k, v := range want {
		if got := env[k]; got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestPopulateMCPBridgeShortEnvClearsStaleValues(t *testing.T) {
	env := map[string]string{
		"MCP_MCP":     "old",
		"MCP_CUSTOM":  "old",
		"MCP_VIRTUAL": "old",
		"MCP_AUTH":    "old",
	}

	PopulateMCPBridgeShortEnv(env)

	for _, k := range []string{"MCP_MCP", "MCP_CUSTOM", "MCP_VIRTUAL", "MCP_AUTH"} {
		if _, exists := env[k]; exists {
			t.Fatalf("%s should be cleared when source env is missing", k)
		}
	}
}

func TestSetSessionShellEnvMergesAndCopies(t *testing.T) {
	sid := "sess-env-merge"
	defer ClearSessionShellConfig(sid)

	SetSessionShellEnv(sid, map[string]string{"DB_PATH": "/a", "STEP_OUTPUT_DIR": "/out"})
	SetSessionShellEnv(sid, map[string]string{"DB_PATH": "/b"}) // override one, keep the other

	env := GetSessionShellEnv(sid)
	if env["DB_PATH"] != "/b" {
		t.Fatalf("DB_PATH = %q, want /b (later call overrides)", env["DB_PATH"])
	}
	if env["STEP_OUTPUT_DIR"] != "/out" {
		t.Fatalf("STEP_OUTPUT_DIR = %q, want /out (preserved)", env["STEP_OUTPUT_DIR"])
	}
	// Returned map must be a copy — mutating it must not affect stored config.
	env["DB_PATH"] = "/mutated"
	if GetSessionShellEnv(sid)["DB_PATH"] != "/b" {
		t.Fatal("GetSessionShellEnv must return a copy, not the live map")
	}
	// Empty input is a no-op.
	SetSessionShellEnv(sid, nil)
	if GetSessionShellEnv(sid)["DB_PATH"] != "/b" {
		t.Fatal("nil env should be a no-op")
	}
}

func TestReplaceSessionShellEnvPrefixReplacesOnlyManagedNamespace(t *testing.T) {
	sid := "sess-env-prefix"
	defer ClearSessionShellConfig(sid)

	SetSessionShellEnv(sid, map[string]string{
		"DB_PATH":           "/db",
		"WORK_FOLDER_OLD":   "/old",
		"WORKFLOW_FOLDER_X": "/workflow",
	})
	ReplaceSessionShellEnvPrefix(sid, "WORK_FOLDER_", map[string]string{"WORK_FOLDER_NEW": "/new"})

	env := GetSessionShellEnv(sid)
	if _, exists := env["WORK_FOLDER_OLD"]; exists {
		t.Fatal("stale Work folder alias remained after namespace replacement")
	}
	if env["WORK_FOLDER_NEW"] != "/new" || env["DB_PATH"] != "/db" || env["WORKFLOW_FOLDER_X"] != "/workflow" {
		t.Fatalf("namespace replacement damaged unrelated env or omitted new alias: %#v", env)
	}
	ReplaceSessionShellEnvPrefix(sid, "WORK_FOLDER_", nil)
	if _, exists := GetSessionShellEnv(sid)["WORK_FOLDER_NEW"]; exists {
		t.Fatal("empty replacement did not remove managed namespace")
	}
}

func TestReconcileSessionWorkflowFolderAccessIsWorkflowScoped(t *testing.T) {
	const first = "folder-reconcile-first"
	const second = "folder-reconcile-second"
	defer ClearSessionShellConfig(first)
	defer ClearSessionShellConfig(second)
	shared := "/tmp/shared-grant"
	SetSessionWorkflowPath(first, "Workflow/first")
	SetSessionFolderGuard(first, []string{"Workflow/first", shared}, []string{shared})
	SetSessionFolderGuardBlockedWritePaths(first, []string{"Workflow/first/planning", shared})
	SetSessionShellEnv(first, map[string]string{"WORKFLOW_FOLDER_SHARED": shared})
	SetSessionWorkflowPath(second, "Workflow/second")
	SetSessionFolderGuard(second, []string{"Workflow/second", shared}, []string{shared})
	SetSessionShellEnv(second, map[string]string{"WORKFLOW_FOLDER_SHARED": shared})

	ReconcileSessionWorkflowFolderAccess("Workflow/first", []string{shared}, nil, nil, nil, nil)
	firstConfig := GetSessionShellConfig(first)
	if len(firstConfig.ReadPaths) != 1 || firstConfig.ReadPaths[0] != "Workflow/first" || len(firstConfig.WritePaths) != 0 {
		t.Fatalf("first workflow grant was not revoked: %#v", firstConfig)
	}
	if len(firstConfig.BlockedWritePaths) != 1 || firstConfig.BlockedWritePaths[0] != "Workflow/first/planning" {
		t.Fatalf("revoked read-only deny was not removed cleanly: %#v", firstConfig.BlockedWritePaths)
	}
	if _, exists := firstConfig.Env["WORKFLOW_FOLDER_SHARED"]; exists {
		t.Fatal("revoked alias remained in first workflow environment")
	}
	ReconcileSessionWorkflowFolderAccess(
		"Workflow/first", nil, []string{shared}, nil, []string{shared},
		map[string]string{"WORKFLOW_FOLDER_SHARED": shared},
	)
	firstConfig = GetSessionShellConfig(first)
	if len(firstConfig.WritePaths) != 0 || !slices.Contains(firstConfig.ReadPaths, shared) || !slices.Contains(firstConfig.BlockedWritePaths, shared) {
		t.Fatalf("read-only grant was not applied live: %#v", firstConfig)
	}
	ReconcileSessionWorkflowFolderAccess(
		"Workflow/first", []string{shared}, []string{shared}, []string{shared}, nil,
		map[string]string{"WORKFLOW_FOLDER_SHARED": shared},
	)
	firstConfig = GetSessionShellConfig(first)
	if !slices.Contains(firstConfig.WritePaths, shared) || slices.Contains(firstConfig.BlockedWritePaths, shared) {
		t.Fatalf("read-only grant was not widened cleanly: %#v", firstConfig)
	}
	secondConfig := GetSessionShellConfig(second)
	if len(secondConfig.WritePaths) != 1 || secondConfig.WritePaths[0] != shared || secondConfig.Env["WORKFLOW_FOLDER_SHARED"] != shared {
		t.Fatalf("second workflow was incorrectly changed: %#v", secondConfig)
	}
}

func TestReconcileSessionWorkflowFolderAccessFindsLegacyUntaggedWorkflowSession(t *testing.T) {
	sessionID := "legacy-untagged-workflow-session"
	defer ClearSessionShellConfig(sessionID)
	SetSessionWorkingDir(sessionID, "Workflow/websiteaeo")
	SetSessionFolderGuard(sessionID, []string{"Workflow/websiteaeo"}, []string{"Workflow/websiteaeo"})
	external := "/tmp/public-website"

	ReconcileSessionWorkflowFolderAccess(
		"Workflow/websiteaeo", nil, []string{external}, []string{external}, nil,
		map[string]string{"WORKFLOW_FOLDER_PUBLIC_WEBSITE": external},
	)

	config := GetSessionShellConfig(sessionID)
	if config.WorkflowPath != "Workflow/websiteaeo" || !slices.Contains(config.ReadPaths, external) || !slices.Contains(config.WritePaths, external) {
		t.Fatalf("legacy session was not adopted and widened: %#v", config)
	}
	if config.Env["WORKFLOW_FOLDER_PUBLIC_WEBSITE"] != external {
		t.Fatalf("legacy session did not receive alias environment: %#v", config.Env)
	}
}

func TestApplySessionWorkflowFolderAccessRefreshesAndRevokesCurrentGrant(t *testing.T) {
	sessionID := "refresh-current-workflow-session"
	defer ClearSessionShellConfig(sessionID)
	SetSessionFolderGuard(sessionID, []string{"Workflow/websiteaeo"}, []string{"Workflow/websiteaeo"})
	external := "/tmp/public-website"
	env := map[string]string{"WORKFLOW_FOLDER_PUBLIC_WEBSITE": external}

	ApplySessionWorkflowFolderAccess(sessionID, "Workflow/websiteaeo", []string{external}, []string{external}, nil, env)
	config := GetSessionShellConfig(sessionID)
	if !slices.Contains(config.ReadPaths, external) || !slices.Contains(config.WritePaths, external) {
		t.Fatalf("current grant was not applied: %#v", config)
	}

	ApplySessionWorkflowFolderAccess(sessionID, "Workflow/websiteaeo", []string{external}, nil, []string{external}, env)
	config = GetSessionShellConfig(sessionID)
	if slices.Contains(config.WritePaths, external) || !slices.Contains(config.BlockedWritePaths, external) {
		t.Fatalf("current grant was not narrowed to read-only: %#v", config)
	}

	ApplySessionWorkflowFolderAccess(sessionID, "Workflow/websiteaeo", nil, nil, nil, nil)
	config = GetSessionShellConfig(sessionID)
	if slices.Contains(config.ReadPaths, external) || slices.Contains(config.WritePaths, external) || slices.Contains(config.BlockedWritePaths, external) {
		t.Fatalf("current grant was not revoked: %#v", config)
	}
}

func TestReadOnlyWorkflowSessionKeepsAttachedFolderGrantsReadOnly(t *testing.T) {
	const sessionID = "readonly-workflow-folder-grant"
	const workflow = "Workflow/demo"
	const external = "/tmp/readonly-workflow-external"
	defer ClearSessionShellConfig(sessionID)
	SetSessionWorkflowReadOnly(sessionID, true)
	SetSessionFolderGuard(sessionID, []string{workflow, "Downloads"}, []string{"Downloads"})
	env := map[string]string{"WORKFLOW_FOLDER_EXTERNAL": external}

	ApplySessionWorkflowFolderAccess(sessionID, workflow, []string{external}, []string{external}, nil, env)
	config := GetSessionShellConfig(sessionID)
	if slices.Contains(config.WritePaths, workflow) || slices.Contains(config.WritePaths, external) ||
		!slices.Contains(config.ReadPaths, external) || !slices.Contains(config.BlockedWritePaths, external) {
		t.Fatalf("read-only grant was widened during session refresh: %#v", config)
	}

	ReconcileSessionWorkflowFolderAccess(workflow, []string{external}, []string{external}, []string{external}, nil, env)
	config = GetSessionShellConfig(sessionID)
	if slices.Contains(config.WritePaths, external) || !slices.Contains(config.BlockedWritePaths, external) {
		t.Fatalf("read-only grant was widened during live reconciliation: %#v", config)
	}

	SetSessionWorkflowReadOnly(sessionID, false)
	ReconcileSessionWorkflowFolderAccess(workflow, []string{external}, []string{external}, []string{external}, nil, env)
	config = GetSessionShellConfig(sessionID)
	if !slices.Contains(config.WritePaths, external) || slices.Contains(config.BlockedWritePaths, external) {
		t.Fatalf("writable workflow did not regain its approved grant: %#v", config)
	}
}

func TestSessionShellConfigIsImmutableAcrossCallers(t *testing.T) {
	sessionID := "immutable-shell-config"
	defer ClearSessionShellConfig(sessionID)
	reads := []string{"Workflow/demo"}
	writes := []string{"Workflow/demo/output"}
	SetSessionFolderGuard(sessionID, reads, writes)
	SetSessionFolderGuardBlockedPaths(sessionID, []string{"Workflow/demo/secrets"})
	SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/demo/planning"})

	reads[0] = "Workflow/other"
	writes[0] = "Workflow/other/output"
	first := GetSessionShellConfig(sessionID)
	first.ReadPaths[0] = "mutated-read"
	first.WritePaths[0] = "mutated-write"
	first.BlockedPaths[0] = "mutated-block"
	first.BlockedWritePaths[0] = "mutated-write-block"

	second := GetSessionShellConfig(sessionID)
	if got := second.ReadPaths[0]; got != "Workflow/demo" {
		t.Fatalf("stored read path mutated: %q", got)
	}
	if got := second.WritePaths[0]; got != "Workflow/demo/output" {
		t.Fatalf("stored write path mutated: %q", got)
	}
	if got := second.BlockedPaths[0]; got != "Workflow/demo/secrets" {
		t.Fatalf("stored blocked path mutated: %q", got)
	}
	if got := second.BlockedWritePaths[0]; got != "Workflow/demo/planning" {
		t.Fatalf("stored blocked-write path mutated: %q", got)
	}
}

func TestCopySessionFolderGuardPreservesDenyOnlyGuard(t *testing.T) {
	const source = "deny-only-source"
	const target = "deny-only-target"
	defer ClearSessionShellConfig(source)
	defer ClearSessionShellConfig(target)

	SetSessionFolderGuardBlockedPaths(source, []string{"Workflow/demo/secrets"})
	SetSessionFolderGuardBlockedWritePaths(source, []string{"Workflow/demo/planning"})
	if !CopySessionFolderGuard(source, target) {
		t.Fatal("deny-only guard should be copied")
	}

	copied := GetSessionShellConfig(target)
	if copied == nil || len(copied.BlockedPaths) != 1 || copied.BlockedPaths[0] != "Workflow/demo/secrets" {
		t.Fatalf("blocked paths not copied: %+v", copied)
	}
	if len(copied.BlockedWritePaths) != 1 || copied.BlockedWritePaths[0] != "Workflow/demo/planning" {
		t.Fatalf("blocked write paths not copied: %+v", copied)
	}
}

func TestUserBrowserAliasesAndChildSessions(t *testing.T) {
	t.Setenv("AGENTWORKS_BROWSER_SESSION_PREFIX", "customer")
	BindSessionBrowserIsolationForWorkflow("parent", "Workflow/research")
	SetSessionBrowserNamespace("child", GetSessionShellConfig("parent").BrowserSessionNamespace)
	SetSessionBrowserSessionID("child", "obsolete-workflow-browser")
	defer ClearSessionShellConfig("parent")
	defer ClearSessionShellConfig("child")
	expected := ResolveBrowserSessionID("parent", "default")
	for _, sid := range []string{"parent", "child"} {
		for _, name := range []string{"", "default", "main", "ai-news", expected, "user-someone-else--browser"} {
			if got := ResolveBrowserSessionID(sid, name); got != expected {
				t.Fatalf("%s/%s: %s != %s", sid, name, got, expected)
			}
		}
	}
	BindSessionBrowserIsolationForWorkflow("parent", "Workflow/research")
	if ResolveBrowserSessionID("parent", "main") != expected {
		t.Fatal("changing user split the shared workflow browser")
	}
	if SessionBrowserSessionNamespace("guest-one") == SessionBrowserSessionNamespace("guest-two") {
		t.Fatal("anonymous chats share cookies")
	}
}
