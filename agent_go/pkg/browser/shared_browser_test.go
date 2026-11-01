package browser

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedBrowserDoesNotLockOtherUsers(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	release, ok := TryTakeBrowserControl(SharedSessionName)
	if !ok {
		t.Fatal("control unavailable")
	}
	defer release()
	release2, ok := TryTakeBrowserControl(SharedSessionName)
	if !ok {
		t.Fatal("shared control locked")
	}
	defer release2()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release3, err := AcquireBrowserAutomation(ctx, SharedSessionName)
	if err != nil {
		t.Fatal(err)
	}
	release3()
}

func TestRemoveStaleChromeSingletonLockClearsChromeOwnLockFiles(t *testing.T) {
	profileRoot := t.TempDir()
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", profileRoot)
	session := "user-0123456789abcdef--browser"
	profile := ProfilePathForSession(session)
	if profile == "" {
		t.Fatal("expected a resolved profile path")
	}
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if err := os.WriteFile(filepath.Join(profile, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file agent-browser's own recovery must not touch.
	keep := filepath.Join(profile, "Preferences")
	if err := os.WriteFile(keep, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	removeStaleChromeSingletonLock(session)

	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Stat(filepath.Join(profile, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err=%v", name, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("expected unrelated profile file to survive: %v", err)
	}
}

func TestRemoveStaleChromeSingletonLockNoopWithoutSharedProfile(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "")
	// Must not panic or attempt any filesystem access when there is no shared profile.
	removeStaleChromeSingletonLock("user-0123456789abcdef--browser")
}

func TestSessionDirsChecksBothNamespacedLocationsWhenSet(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("AGENT_BROWSER_NAMESPACE", "example-app")

	dirs := sessionDirs()
	if len(dirs) < 2 {
		t.Fatalf("expected at least two namespaced candidate dirs, got %v", dirs)
	}
	wantXDG := filepath.Join(runtimeDir, "agent-browser", "namespaces", "example-app", "run")
	wantTmp := filepath.Join("/tmp/.agent-browser", "namespaces", "example-app", "run")
	if dirs[0] != wantXDG {
		t.Fatalf("expected XDG runtime dir first, got %v", dirs)
	}
	if dirs[1] != wantTmp {
		// This is the location that actually matters in production: the
		// Landlock-sandboxed subprocess that really spawns agent-browser
		// doesn't see XDG_RUNTIME_DIR even though the parent service does.
		t.Fatalf("expected /tmp namespaced fallback second (this is where production actually writes), got %v", dirs)
	}
}

func TestSessionDirsFallsBackWithoutNamespace(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AGENT_BROWSER_NAMESPACE", "")

	for _, dir := range sessionDirs() {
		if strings.Contains(dir, "namespaces") {
			t.Fatalf("did not expect a namespaced dir without AGENT_BROWSER_NAMESPACE set: %v", dir)
		}
	}
}

func TestSharedBrowserMapsWorkflowsToSameRuntime(t *testing.T) {
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", "/data/browser-profile")
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ShellExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if !strings.Contains(req.Command, "shared-browser") || strings.Contains(req.Command, "--user-agent") || !strings.Contains(req.Command, "/data/browser-profile") {
			t.Errorf("wrong shared command: %s", req.Command)
		}
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: ShellExecuteResponse{Stdout: `{"success":true}`, ExitCode: 0}})
	}))
	defer server.Close()
	for _, owner := range []string{"alice", "bob"} {
		ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, owner)
		_, err := NewExecutor(NewClient(server.URL)).HandleAgentBrowser(ctx, map[string]interface{}{"session": owner, "command": "open", "args": []interface{}{"https://example.com"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range GetSessionTracker().ActiveSessions() {
		if s["browser_session"] == SharedSessionName {
			t.Fatal("shared browser registered for workflow cleanup")
		}
	}
}
