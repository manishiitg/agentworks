package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestCreateFolderTreatsExistingFolderAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/folders" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"folder already exists"}`))
	}))
	defer server.Close()

	if err := NewClient(server.URL).CreateFolder(context.Background(), "Chats/Work/projects/demo/code"); err != nil {
		t.Fatalf("existing folder should satisfy CreateFolder: %v", err)
	}
}

func TestCreateFolderStillReturnsOtherWorkspaceErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"denied"}`))
	}))
	defer server.Close()

	err := NewClient(server.URL).CreateFolder(context.Background(), "Chats/Work/projects/demo/code")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("non-conflict error must be preserved, got %v", err)
	}
}

func TestDeleteFolderForwardsUserAndConfirmsPermanentDeletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/folders/Chats/Work/projects/demo" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("confirm") != "true" {
			t.Fatal("folder deletion was not explicitly confirmed")
		}
		if got := r.Header.Get("X-User-ID"); got != "crew-owner" {
			t.Fatalf("X-User-ID=%q, want crew-owner", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := NewClient(server.URL, WithUserID("crew-owner")).DeleteFolder(context.Background(), "Chats/Work/projects/demo"); err != nil {
		t.Fatal(err)
	}
}

// TestValidatePathAgainstGuard_BlockedWritePaths verifies the write-only deny
// semantic on the Go-side path validator:
//
//   - A path under BlockedWritePaths is allowed to READ.
//   - The same path is denied for WRITE.
//   - Paths not under BlockedWritePaths are unaffected.
//   - BlockedPaths (the hard deny) still denies both reads and writes.
//
// This is the client-side counterpart to the isolator's kernel-level enforcement
// tested in workspace/security/isolator_test.go. Both surfaces must deny the same
// writes for the semantic to be consistent — a Go-client check that was lenient
// here would let raw Go-level file API calls bypass the block even though shell
// commands would still hit the kernel sandbox.
func TestValidatePathAgainstGuard_BlockedWritePaths(t *testing.T) {
	guard := &FolderGuardConfig{
		Enabled:           true,
		WritePaths:        []string{"Workflow/test-ops"},
		BlockedWritePaths: []string{"Workflow/test-ops/planning", "Workflow/test-ops/AGENTS.md"},
	}

	cases := []struct {
		name      string
		path      string
		isWrite   bool
		wantError string // substring; empty = expect success
	}{
		{
			name:    "read of blocked-write path is allowed",
			path:    "Workflow/test-ops/planning/plan.json",
			isWrite: false,
		},
		{
			name:    "read of nested file under blocked-write path is allowed",
			path:    "Workflow/test-ops/planning/nested/deep.json",
			isWrite: false,
		},
		{
			name:      "write to blocked-write path is denied",
			path:      "Workflow/test-ops/planning/plan.json",
			isWrite:   true,
			wantError: "blocked for writes",
		},
		{
			name:      "write to nested file under blocked-write path is denied",
			path:      "Workflow/test-ops/planning/nested/deep.json",
			isWrite:   true,
			wantError: "blocked for writes",
		},
		{
			name:      "write to exact blocked-write file is denied",
			path:      "Workflow/test-ops/AGENTS.md",
			isWrite:   true,
			wantError: "blocked for writes",
		},
		{
			name:    "read of exact blocked-write file is allowed",
			path:    "Workflow/test-ops/AGENTS.md",
			isWrite: false,
		},
		{
			name:    "similarly prefixed sibling of exact file remains writable",
			path:    "Workflow/test-ops/AGENTS.md.notes",
			isWrite: true,
		},
		{
			name:    "write to sibling under same workflow root is allowed",
			path:    "Workflow/test-ops/reports/report_plan.md",
			isWrite: true,
		},
		{
			name:    "read from sibling is allowed",
			path:    "Workflow/test-ops/reports/report_plan.md",
			isWrite: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePathAgainstGuard(guard, tc.path, tc.isWrite)
			switch {
			case tc.wantError == "" && err != nil:
				t.Fatalf("expected success for path=%q isWrite=%v, got error: %v", tc.path, tc.isWrite, err)
			case tc.wantError != "" && err == nil:
				t.Fatalf("expected error containing %q for path=%q isWrite=%v, got nil", tc.wantError, tc.path, tc.isWrite)
			case tc.wantError != "" && err != nil && !strings.Contains(err.Error(), tc.wantError):
				t.Fatalf("expected error containing %q, got: %v", tc.wantError, err)
			}
		})
	}
}

func TestValidatePathAgainstGuardEmptyCapabilitiesFailClosed(t *testing.T) {
	guard := &FolderGuardConfig{Enabled: true}
	if err := validatePathAgainstGuard(guard, "Workflow/demo/report.html", false); err == nil || !strings.Contains(err.Error(), "no workspace read paths") {
		t.Fatalf("empty read capability should fail closed, got %v", err)
	}
	if err := validatePathAgainstGuard(guard, "Workflow/demo/report.html", true); err == nil || !strings.Contains(err.Error(), "no workspace write paths") {
		t.Fatalf("empty write capability should fail closed, got %v", err)
	}
}

func TestValidatePathWithContextNormalizesPublicPerUserPath(t *testing.T) {
	const (
		sessionID = "work-project-public-path"
		userID    = "aa73da63e26b40a1bb701c2b4c024870"
	)
	project := "_users/" + userID + "/Chats/Work/projects/latency"
	SetSessionFolderGuard(sessionID, []string{project}, []string{project + "/db"})
	SetSessionFolderGuardBlockedPaths(sessionID, []string{project + "/db/private"})
	defer ClearSessionShellConfig(sessionID)

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	client := NewClient("http://unused", WithUserID(userID))
	if err := client.ValidatePathWithContext(ctx, "Chats/Work/projects/latency/db/migrations/add.sql", false); err != nil {
		t.Fatalf("public Work path should match its physical session guard: %v", err)
	}
	if err := client.ValidatePathWithContext(ctx, "Chats/Work/projects/latency/db/private/secret.sql", false); err == nil {
		t.Fatal("public Work path must retain the physical guard's blocked paths")
	}
	if err := client.ValidatePathWithContext(ctx, "Chats/Work/projects/other/db/migrations/add.sql", false); err == nil {
		t.Fatal("normalization must not grant another Work project")
	}
	foreignClient := NewClient("http://unused", WithUserID("another-user"))
	if err := foreignClient.ValidatePathWithContext(ctx, "Chats/Work/projects/latency/db/migrations/add.sql", false); err == nil {
		t.Fatal("normalization must not grant another user's physical path")
	}
}

func TestResolveEffectiveFolderGuardPreservesReadOnlyAndDenyPaths(t *testing.T) {
	sessionID := "read-only-session-guard"
	SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, nil)
	SetSessionFolderGuardBlockedPaths(sessionID, []string{"Workflow/demo/secrets"})
	SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/demo/planning"})
	defer ClearSessionShellConfig(sessionID)

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	guard := NewClient("http://unused").resolveEffectiveFolderGuard(ctx)
	if guard == nil || len(guard.ReadPaths) != 1 || len(guard.WritePaths) != 0 {
		t.Fatalf("read-only guard was not preserved: %#v", guard)
	}
	if len(guard.BlockedPaths) != 1 || len(guard.BlockedWritePaths) != 1 {
		t.Fatalf("deny paths were dropped: %#v", guard)
	}
	if err := validatePathAgainstGuard(guard, "Workflow/demo/report.html", false); err != nil {
		t.Fatalf("granted read should pass: %v", err)
	}
	if err := validatePathAgainstGuard(guard, "Workflow/demo/report.html", true); err == nil {
		t.Fatal("read-only session unexpectedly gained write access")
	}
}

func TestSystemManagedWritePathsOverrideWriteOnlyDeny(t *testing.T) {
	sessionID := "system-managed-write-session"
	SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, []string{"Workflow/demo"})
	SetSessionFolderGuardBlockedPaths(sessionID, []string{"Workflow/demo/secrets"})
	SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/demo/planning"})
	defer ClearSessionShellConfig(sessionID)

	client := NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	planPath := "Workflow/demo/planning/plan.json"

	if err := client.ValidatePathWithContext(ctx, planPath, true); err == nil || !strings.Contains(err.Error(), "blocked for writes") {
		t.Fatalf("ordinary session write should remain blocked, got %v", err)
	}

	managedCtx := WithSystemManagedWritePaths(ctx, "Workflow/demo/planning")
	if err := client.ValidatePathWithContext(managedCtx, planPath, true); err != nil {
		t.Fatalf("trusted plan write should bypass write-only deny: %v", err)
	}
	if err := client.ValidatePathWithContext(managedCtx, "Workflow/demo/secrets/token.txt", true); err == nil || !strings.Contains(err.Error(), "is blocked") {
		t.Fatalf("system-managed write must not bypass hard deny, got %v", err)
	}
	if err := client.ValidatePathWithContext(managedCtx, "Workflow/other/planning/plan.json", true); err == nil {
		t.Fatal("system-managed capability escaped its workflow planning path")
	}
}

// TestValidatePathAgainstGuard_BlockedPathsStillDeniesBoth asserts that the
// pre-existing BlockedPaths semantic — "deny both reads and writes" — is
// unchanged by the addition of BlockedWritePaths. These are two independent
// primitives and must not interfere.
func TestValidatePathAgainstGuard_BlockedPathsStillDeniesBoth(t *testing.T) {
	guard := &FolderGuardConfig{
		Enabled:      true,
		WritePaths:   []string{"Workflow/test-ops"},
		BlockedPaths: []string{"Workflow/test-ops/secrets"},
	}

	for _, isWrite := range []bool{true, false} {
		name := "read"
		if isWrite {
			name = "write"
		}
		t.Run(name+"_of_blocked_path_is_denied", func(t *testing.T) {
			err := validatePathAgainstGuard(guard, "Workflow/test-ops/secrets/token.txt", isWrite)
			if err == nil {
				t.Fatalf("expected error for %s of blocked path, got nil", name)
			}
			if !strings.Contains(err.Error(), "is blocked") {
				t.Fatalf("expected 'is blocked' error, got: %v", err)
			}
		})
	}
}

func TestValidatePathAgainstGuard_ExactFileWritePathIsNotPrefix(t *testing.T) {
	guard := &FolderGuardConfig{
		Enabled:    true,
		WritePaths: []string{"Workflow/latency-report/builder/improve.html"},
	}

	cases := []struct {
		name      string
		path      string
		wantError bool
	}{
		{
			name: "exact improve log is writable",
			path: "Workflow/latency-report/builder/improve.html",
		},
		{
			name:      "sibling builder file is not writable",
			path:      "Workflow/latency-report/builder/other.html",
			wantError: true,
		},
		{
			name:      "file path is not treated as writable directory prefix",
			path:      "Workflow/latency-report/builder/improve.html/child.html",
			wantError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePathAgainstGuard(guard, tc.path, true)
			if tc.wantError && err == nil {
				t.Fatalf("expected write to %q to be blocked", tc.path)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("expected write to %q to be allowed, got: %v", tc.path, err)
			}
		})
	}
}

// Enforcement regression for message_sequence per-item permissions: the reused
// execution agent carries a BROAD frozen snapshot (step's full write scope: db +
// learnings), but the current item's per-item session guard must take priority and
// narrow it. Proves a db-only item is denied a learnings write and allowed a db write.
func TestSessionGuardNarrowsFrozenSnapshotForAgentItem(t *testing.T) {
	sessionID := "msgseq-narrow-enforcement"
	client := NewClient("http://unused")

	// Frozen per-agent snapshot (ctx System 2): agent created with the step's FULL
	// write scope — db AND learnings/_global.
	broad := []string{"Workflow/wf/db", "Workflow/wf/learnings/_global"}
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	ctx = context.WithValue(ctx, common.FolderGuardWritePathsKey, broad)
	ctx = context.WithValue(ctx, common.FolderGuardReadPathsKey, broad)

	// Snapshot-only (no session guard): the broad snapshot decides — learnings writable.
	if err := client.ValidatePathWithContext(ctx, "Workflow/wf/learnings/_global/SKILL.md", true); err != nil {
		t.Fatalf("broad snapshot should allow a learnings write on its own: %v", err)
	}

	// Now a DB-ONLY per-item session guard for this turn must OUTRANK the snapshot.
	SetSessionFolderGuard(sessionID, []string{"Workflow/wf/db"}, []string{"Workflow/wf/db"})
	defer ClearSessionShellConfig(sessionID)

	if err := client.ValidatePathWithContext(ctx, "Workflow/wf/db/state.sqlite", true); err != nil {
		t.Fatalf("db write must be accepted under the db-only session guard: %v", err)
	}
	if err := client.ValidatePathWithContext(ctx, "Workflow/wf/learnings/_global/SKILL.md", true); err == nil {
		t.Fatal("learnings write must be DENIED: the db-only per-item session guard outranks the broad frozen snapshot")
	}

	// The deciding guard must be the session guard, not the snapshot.
	if g := client.resolveEffectiveFolderGuard(ctx); g == nil || g.Source != "session" {
		t.Fatalf("expected session guard to decide, got source=%q", func() string {
			if g == nil {
				return "<nil>"
			}
			return g.Source
		}())
	}
}

func TestResolveEffectiveFolderGuardCarriesSandboxPolicy(t *testing.T) {
	sessionID := "strict-sandbox-session"
	SetSessionFolderGuard(sessionID, []string{"_users/u1/Chats/family/activity-1"}, []string{"_users/u1/Chats/family/activity-1"})
	SetSessionSandbox(sessionID, true, true)
	defer ClearSessionShellConfig(sessionID)

	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	guard := NewClient("http://unused").resolveEffectiveFolderGuard(ctx)
	if guard == nil || !guard.StrictAllowlist || !guard.DenyNetwork {
		t.Fatalf("sandbox policy did not reach the folder guard: %#v", guard)
	}
	// A session without a sandbox policy keeps the permissive defaults.
	other := "plain-session"
	SetSessionFolderGuard(other, []string{"Workflow/demo"}, []string{"Workflow/demo"})
	defer ClearSessionShellConfig(other)
	plain := NewClient("http://unused").resolveEffectiveFolderGuard(context.WithValue(context.Background(), common.ChatSessionIDKey, other))
	if plain == nil || plain.StrictAllowlist || plain.DenyNetwork {
		t.Fatalf("plain session should not be strict: %#v", plain)
	}
}
