package security

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

// PLAT-478: a project chat's shell command named its project's browser (common.SandboxBrowserSession), and
// scopeBrowser added that browser's profile (<profile>-projects/project-<hash>--browser, app-owned 0700) and socket
// folder to the command's write grants. Run as a slot, the launcher could not stat the profile and refused the
// whole command. A slot command gets no browser folder; the service account's commands keep theirs.
func TestSlotCommandGetsNoBrowserFolders(t *testing.T) {
	root := t.TempDir()
	t.Setenv(browserconfig.ProfileEnv, filepath.Join(root, "browser-profile"))
	session := "project-3bdc30503fa28a4f--browser"
	profile := browserconfig.ProfilePathForSession(session)
	if !strings.Contains(profile, "browser-profile-projects") {
		t.Fatalf("unexpected profile path %s", profile)
	}

	slot := &Isolator{Slot: "slot02", BrowserSession: session}
	if socket := slot.scopeBrowser(); socket != "" {
		t.Fatalf("a slot command was given a browser socket folder %s", socket)
	}
	for _, path := range slot.WritePaths {
		if strings.Contains(path, "browser-profile") || strings.Contains(path, browserconfig.SocketRoot) {
			t.Fatalf("a slot command was granted a browser folder: %s", path)
		}
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("the slot path must not create the profile (err=%v)", err)
	}
	if slot.BrowserSession == "" {
		t.Fatal("the session must stay set so the policy is browser scoped (no shared browser grants either)")
	}

	app := &Isolator{BrowserSession: session}
	if socket := app.scopeBrowser(); socket == "" {
		t.Fatal("a service-account command keeps its own browser")
	}
	found := false
	for _, path := range app.WritePaths {
		if path == profile {
			found = true
		}
	}
	if !found {
		t.Fatalf("a service-account command keeps its profile grant: %v", app.WritePaths)
	}
}

func TestSlotGrantsNeverIncludeAppPrivatePaths(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	state := filepath.Join(root, "state")
	t.Setenv(browserconfig.ProfileEnv, filepath.Join(state, "browser-profile"))
	t.Setenv(browserconfig.ProfileRootEnv, filepath.Join(root, "profile-root"))
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	project := filepath.Join(docs, "_users", "u1", "Chats", "Work", "projects", "p1")
	private := []string{
		filepath.Join(state, "browser-profile"),
		filepath.Join(state, "browser-profile-projects", "project-3bdc30503fa28a4f--browser"),
		filepath.Join(state, "browser-profile-workflows", "workflow-0123456789abcdef--browser"),
		filepath.Join(state, "browser-profile-users", "x"),
		filepath.Join(root, "profile-root-projects", "y"),
		filepath.Join(state, "cli-runtimes", "v1", "abc"),
		filepath.Join(state, "ownership"),
		filepath.Join(browserconfig.SocketRoot, "o", "p0123"),
		filepath.Join(docs, "tmp", ".agent-browser", "o", "p0123"),
	}
	grants := append([]string{project, filepath.Join(docs, "Workflow", "wf")}, private...)

	kept := withoutAppPrivatePaths("slot01", grants, docs)
	if strings.Join(kept, "|") != project+"|"+filepath.Join(docs, "Workflow", "wf") {
		t.Fatalf("slot grants = %v", kept)
	}
	if got := withoutAppPrivatePaths("", grants, docs); len(got) != len(grants) {
		t.Fatalf("a service-account command's grants must be unchanged: %v", got)
	}
	// A state root that holds the workspace itself is not treated as private (it would take the project away).
	t.Setenv("AGENTWORKS_STATE_ROOT", root)
	if got := withoutAppPrivatePaths("slot01", []string{project}, docs); len(got) != 1 {
		t.Fatalf("a state root containing the docs root must not drop the project: %v", got)
	}
	// ...but the browser roots stay private whatever the layout.
	if got := withoutAppPrivatePaths("slot01", []string{private[1]}, docs); len(got) != 0 {
		t.Fatalf("the browser profile must never be granted to a slot: %v", got)
	}
}

func TestOnlyAPermissionErrorLetsTheLauncherSkipAGrant(t *testing.T) {
	if !grantStatSkippable(&fs.PathError{Op: "stat", Path: "/x", Err: fs.ErrPermission}) {
		t.Fatal("EACCES on a grant: skip it (grants less)")
	}
	for _, err := range []error{nil, fs.ErrNotExist, errors.New("input/output error"), &fs.PathError{Op: "stat", Path: "/x", Err: errors.New("too many levels of symbolic links")}} {
		if grantStatSkippable(err) {
			t.Fatalf("%v must not be skipped silently", err)
		}
	}
}
