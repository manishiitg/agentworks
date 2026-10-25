package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func TestCodeShellIsOwnerOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	target := func(userID string) (string, string, int) {
		req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/x", nil, userID), map[string]string{"project_id": "c0de0001-0000"})
		_, shellID, root, status, _ := api.codeShellTarget(req)
		return shellID, root, status
	}
	ownerShell, root, status := target("owner")
	if status != 0 || root != codePrivacyOwnerRoot || !strings.HasPrefix(ownerShell, "code-") || len(ownerShell) > 48 {
		t.Fatalf("owner shell = %q %q %d", ownerShell, root, status)
	}
	// Code is owner-only: nobody else reaches the owner's terminal, whatever the share list says.
	if _, _, status := target("other"); status != http.StatusNotFound {
		t.Fatalf("a stranger reached the shell: %d", status)
	}
}

func TestCodeShellFolderGuardIsTheProjectOnly(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	guard := api.codeShellFolderGuard(ctx, "owner", codePrivacyOwnerRoot)
	if !guard.Enabled || len(guard.WritePaths) != 1 || guard.WritePaths[0] != codePrivacyOwnerRoot+"/" {
		t.Fatalf("writes = %v", guard.WritePaths)
	}
	for _, read := range guard.ReadPaths {
		if !strings.HasPrefix(read, codePrivacyOwnerRoot) && !strings.HasSuffix(read, "/") {
			t.Fatalf("unexpected read %q", read)
		}
		if strings.Contains(read, "_users/") && !strings.HasPrefix(read, codePrivacyOwnerRoot) {
			t.Fatalf("the shell reads another user's tree: %q", read)
		}
	}
	protected := strings.Join(guard.BlockedWritePaths, " ")
	for _, file := range []string{"CLAUDE.md", "AGENTS.md", ".claude"} {
		if !strings.Contains(protected, codePrivacyOwnerRoot+"/"+file) {
			t.Fatalf("%s is not write-protected: %v", file, guard.BlockedWritePaths)
		}
	}
}

func TestIdleCodeShellsAreStopped(t *testing.T) {
	var stopped []string
	previous := codeShellStop
	codeShellStop = func(_ context.Context, id string) error { stopped = append(stopped, id); return nil }
	t.Cleanup(func() { codeShellStop = previous })

	codeShellViewer("code-watched", "/s1", +1)
	codeShellViewer("code-idle", "/s2", +1)
	codeShellViewer("code-idle", "/s2", -1)
	t.Cleanup(func() {
		codeShells.Lock()
		delete(codeShells.byID, "code-watched")
		delete(codeShells.byID, "code-idle")
		codeShells.Unlock()
	})
	if got := stopIdleCodeShells(time.Now()); len(got) != 0 {
		t.Fatalf("stopped a fresh shell: %v", got)
	}
	if got := stopIdleCodeShells(time.Now().Add(codeShellIdleTimeout + time.Minute)); len(got) != 1 || got[0] != "code-idle" || len(stopped) != 1 {
		t.Fatalf("idle stop = %v (stopped %v); a watched shell must stay", got, stopped)
	}
}

// Losing access closes an open shell at once: unsharing or demoting a person
// stops only their shell, deleting the Code stops everyone's.
func TestRevokedCodeShellsAreStoppedAndClosed(t *testing.T) {
	previous := codeShellStop
	var stopped []string
	codeShellStop = func(_ context.Context, id string) error {
		stopped = append(stopped, id)
		return nil
	}
	t.Cleanup(func() { codeShellStop = previous })
	codeShells.Lock()
	saved := codeShells.byID
	codeShells.byID = map[string]*codeShellState{}
	codeShells.Unlock()
	t.Cleanup(func() {
		codeShells.Lock()
		codeShells.byID = saved
		codeShells.Unlock()
	})

	ownerShell := codeShellID("owner", "p1", "owner", 1)
	editorShell := codeShellID("owner", "p1", "editor", 1)
	otherCode := codeShellID("owner", "p2", "editor", 1)
	codeShellTrack(ownerShell, "owner", "p1", "owner")
	codeShellTrack(editorShell, "owner", "p1", "editor")
	codeShellTrack(otherCode, "owner", "p2", "editor")

	if got := stopCodeShellsFor("owner", "p1", []string{"editor"}); len(got) != 1 || got[0] != editorShell {
		t.Fatalf("unshare stopped %v, want only the editor's shell", got)
	}
	if got := stopCodeShellsFor("owner", "p1", nil); len(got) != 1 || got[0] != ownerShell {
		t.Fatalf("delete stopped %v, want the owner's remaining shell", got)
	}
	codeShells.Lock()
	_, kept := codeShells.byID[otherCode]
	codeShells.Unlock()
	if !kept || len(stopped) != 2 {
		t.Fatalf("another Code's shell must survive; stopped=%v", stopped)
	}
}

// The test binary must never stop a developer's real Code shells.
func init() { codeShellSweepOnStart = false }

// A person has at most three terminals per Code. Tab 1 is the shell a single terminal always had, so one running
// before tabs existed carries over; tabs 2 and 3 are separate shells; anything else is refused.
func TestCodeShellTabs(t *testing.T) {
	legacy := func(ownerID, projectID, userID string) string {
		sum := sha256.Sum256([]byte(ownerID + "\x00" + projectID + "\x00" + userID))
		return "code-" + hex.EncodeToString(sum[:])[:32]
	}
	if got := codeShellID("o", "p", "u", 1); got != legacy("o", "p", "u") {
		t.Fatalf("tab 1 must keep the pre-tab shell id: %s", got)
	}
	seen := map[string]bool{}
	for tab := 1; tab <= codeShellMaxTabs; tab++ {
		id := codeShellID("o", "p", "u", tab)
		if seen[id] || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`).MatchString(id) {
			t.Fatalf("tab %d id %q is reused or not a valid workspace shell id", tab, id)
		}
		seen[id] = true
	}
	for raw, want := range map[string]int{"": 1, "1": 1, "2": 2, "3": 3} {
		r := httptest.NewRequest(http.MethodGet, "/x?tab="+raw, nil)
		if got, ok := codeShellTab(r); !ok || got != want {
			t.Errorf("tab=%q -> %d,%v want %d", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"0", "4", "-1", "x", "2.5", "99"} {
		if _, ok := codeShellTab(httptest.NewRequest(http.MethodGet, "/x?tab="+raw, nil)); ok {
			t.Errorf("tab=%q must be refused", raw)
		}
	}
}
