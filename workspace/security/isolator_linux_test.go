//go:build linux

package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLandlockPolicySupportsExternalReadOnlyAndReadWriteFolders(t *testing.T) {
	workspaceRoot := t.TempDir()
	readOnlyRoot := t.TempDir()
	readWriteRoot := t.TempDir()

	isolator := &Isolator{
		BaseDir:           workspaceRoot,
		WorkDir:           workspaceRoot,
		ReadPaths:         []string{workspaceRoot, readOnlyRoot, readWriteRoot},
		WritePaths:        []string{workspaceRoot, readWriteRoot},
		BlockedWritePaths: []string{readOnlyRoot},
	}

	policy, err := isolator.landlockPolicy()
	if err != nil {
		t.Fatalf("landlockPolicy() rejected explicit external folder grants: %v", err)
	}

	contains := func(paths []string, want string) bool {
		want = canonicalPath(want)
		for _, path := range paths {
			if path == want {
				return true
			}
		}
		return false
	}

	if !contains(policy.ReadPaths, readOnlyRoot) {
		t.Error("read-only external folder is missing from Landlock read grants")
	}
	if contains(policy.WritePaths, readOnlyRoot) {
		t.Error("read-only external folder unexpectedly received a Landlock write grant")
	}
	if !contains(policy.ReadPaths, readWriteRoot) || !contains(policy.WritePaths, readWriteRoot) {
		t.Error("read-write external folder must receive both Landlock read and write grants")
	}
}

func TestMountNamespaceFallbackSharesExternalBrowserSocketFolder(t *testing.T) {
	if !mountNamespaceAvailable() {
		t.Skip("mount namespaces are unavailable")
	}
	root := t.TempDir()
	work := filepath.Join(root, "Workflow", "example")
	if err := os.MkdirAll(work, 0755); err != nil {
		t.Fatal(err)
	}
	socketDir := t.TempDir()
	iso := &Isolator{
		BaseDir:    root,
		WorkDir:    work,
		ReadPaths:  []string{work},
		WritePaths: []string{work, socketDir},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := fmt.Sprintf(`printf live > %q/stream-metadata`, socketDir)
	cmd, cleanup, err := iso.executeIsolatedMountNamespace(ctx, command, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandboxed browser failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(socketDir, "stream-metadata"))
	if err != nil || string(data) != "live" {
		t.Fatalf("workspace service cannot see browser metadata: data=%q err=%v", data, err)
	}
}

func TestLandlockEnforcesExternalFolderAccess(t *testing.T) {
	workspaceRoot := t.TempDir()
	readOnlyRoot := t.TempDir()
	readWriteRoot := t.TempDir()
	readOnlyFile := filepath.Join(readOnlyRoot, "source.txt")
	if err := os.WriteFile(readOnlyFile, []byte("external-folder-readable"), 0644); err != nil {
		t.Fatalf("seed read-only folder: %v", err)
	}

	isolator := &Isolator{
		BaseDir:           workspaceRoot,
		WorkDir:           workspaceRoot,
		ReadPaths:         []string{workspaceRoot, readOnlyRoot, readWriteRoot},
		WritePaths:        []string{workspaceRoot, readWriteRoot},
		BlockedWritePaths: []string{readOnlyRoot},
	}

	command := fmt.Sprintf(
		"test \"$(cat %q)\" = external-folder-readable && "+
			"if echo denied > %q 2>/dev/null; then exit 41; fi && "+
			"echo allowed > %q",
		readOnlyFile,
		filepath.Join(readOnlyRoot, "denied.txt"),
		filepath.Join(readWriteRoot, "allowed.txt"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skipf("Landlock is unavailable in this Linux environment: ABI=%d err=%v", abi, err)
	}
	policy, err := isolator.landlockPolicy()
	if err != nil {
		t.Fatalf("landlockPolicy() failed: %v", err)
	}
	cmd, cleanup, err := isolator.landlockCommand(ctx, policy, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if strings.Contains(err.Error(), "SANDBOX_UNAVAILABLE") {
			t.Skipf("Landlock launcher is unavailable in this environment: %v", err)
		}
		t.Fatalf("landlockCommand() failed: %v", err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("external folder policy was not enforced: %v\noutput: %s", err, output)
	}

	if _, err := os.Stat(filepath.Join(readOnlyRoot, "denied.txt")); !os.IsNotExist(err) {
		t.Errorf("read-only external folder accepted a write; stat error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(readWriteRoot, "allowed.txt"))
	if err != nil {
		t.Fatalf("read-write external folder rejected a write: %v", err)
	}
	if strings.TrimSpace(string(contents)) != "allowed" {
		t.Fatalf("read-write external folder content = %q, want allowed", contents)
	}
}

// The pure routing tests live in sandbox_tool_env_test.go (no build tag, so
// they run on every platform); this file keeps only what needs a real
// Landlock kernel.

// TestLandlockCommandCanInstallIntoItsOwnCache is the live reproduction of
// the PLAT-283 incident on the Dominion Hetzner deployment (2026-08-31 /
// 2026-09-05): a workflow step tried "pip3 install yfinance" and, after
// working around PEP 668's externally-managed-environment refusal with
// --break-system-packages, hit a Landlock permission denial writing pip's
// download cache -- and a bare "python3 -m venv" failed the same way, since
// both default to caching under $HOME, which sits outside every step's
// write grant. This does not require python3: it reproduces the exact
// failure shape directly by writing into $PIP_CACHE_DIR the way pip itself
// would, which packageManagerCacheEnv must now route into the granted
// write path instead of the sandbox's unwritable default $HOME.
func TestLandlockCommandCanInstallIntoItsOwnCache(t *testing.T) {
	workspaceRoot := t.TempDir()
	isolator := &Isolator{
		BaseDir:    workspaceRoot,
		WorkDir:    workspaceRoot,
		ReadPaths:  []string{workspaceRoot},
		WritePaths: []string{workspaceRoot},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if abi, err := landlockABI(); err != nil || abi < 1 {
		t.Skipf("Landlock is unavailable in this Linux environment: ABI=%d err=%v", abi, err)
	}
	policy, err := isolator.landlockPolicy()
	if err != nil {
		t.Fatalf("landlockPolicy() failed: %v", err)
	}
	command := `test -n "$PIP_CACHE_DIR" && echo cached > "$PIP_CACHE_DIR/probe.whl"`
	cmd, cleanup, err := isolator.landlockCommand(ctx, policy, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if strings.Contains(err.Error(), "SANDBOX_UNAVAILABLE") {
			t.Skipf("Landlock launcher is unavailable in this environment: %v", err)
		}
		t.Fatalf("landlockCommand() failed: %v", err)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("writing to the redirected pip cache was denied inside the sandbox: %v\noutput: %s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, ".cache", "pip", "probe.whl")); statErr != nil {
		t.Errorf("redirected pip cache write did not land in the granted write path: %v", statErr)
	}
}

// TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy is the
// proper, repeatable server-side reproduction of the live incident on the
// Dominion Hetzner deployment 2026-08-28/29 -- run this directly on any
// Linux deployment host to verify the fix instead of hand-crafting a curl
// request against a running service:
//
//	cd workspace && go test ./security/... -run TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy -v
//
// Exercises the exact policy shape that broke: write access to a folder
// except one of its own subfolders (BlockedWritePaths nested inside
// WritePaths). Landlock's purely-additive rule model cannot express that
// carve-out and correctly refuses it (landlockPolicy() returning
// "blocked-write path overlaps writable path" is Landlock behaving
// correctly, not a bug); the real check is whether ExecuteIsolated's
// end-to-end dispatch still falls through to the mount-namespace fallback
// and that fallback actually enforces the policy -- writes inside the
// granted folder succeed, writes inside its blocked-write subfolder fail.
// Skips (not fails) when Landlock is unavailable in this environment (e.g.
// inside an already-namespaced CI runner) or the overlap isn't rejected,
// since neither means this fix regressed -- it means this run's
// environment can't exercise the fallback path at all.
func TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy(t *testing.T) {
	root := t.TempDir()
	writableDir := filepath.Join(root, "workflow")
	blockedSubdir := filepath.Join(writableDir, "planning")
	for _, dir := range []string{writableDir, blockedSubdir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	isolator := &Isolator{
		ReadPaths:         []string{writableDir},
		WritePaths:        []string{writableDir},
		BlockedWritePaths: []string{blockedSubdir},
		WorkDir:           writableDir,
		// BaseDir must be set explicitly: the mount-namespace backend's
		// script bind-mounts getBaseDir() to preserve the original
		// workspace, and its unset default (/app/workspace-docs, the
		// containerized-deployment path) does not exist on this test host.
		BaseDir: root,
	}

	// Landlock now carries the blocked subfolder as a read-only overlay the
	// launcher mounts in its namespaces; without them, dispatch falls back
	// to the mount-namespace backend. Either backend must enforce it.
	if policy, err := isolator.landlockPolicy(); err != nil {
		t.Fatalf("landlockPolicy() error = %v, want the subfolder as a read-only overlay", err)
	} else if len(policy.ReadOnlyOverlays) != 1 || policy.ReadOnlyOverlays[0] != canonicalPath(blockedSubdir) {
		t.Fatalf("ReadOnlyOverlays = %v, want [%s]", policy.ReadOnlyOverlays, blockedSubdir)
	}

	command := fmt.Sprintf(
		"echo top-level-ok > %q && if echo denied > %q 2>/dev/null; then exit 41; fi",
		filepath.Join(writableDir, "ok.txt"),
		filepath.Join(blockedSubdir, "denied.txt"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", command})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Skipf("ExecuteIsolated could not select any backend in this environment (%v) -- not what this fix changes; run on the actual deployment host to verify it", err)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mount-namespace fallback did not enforce the policy correctly: %v\noutput: %s", err, output)
	}
	if _, statErr := os.Stat(filepath.Join(writableDir, "ok.txt")); statErr != nil {
		t.Errorf("write to the granted folder should have succeeded: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(blockedSubdir, "denied.txt")); statErr == nil {
		t.Error("SECURITY VIOLATION: write to the blocked-write subfolder succeeded")
	}
}

// TestMountNamespaceFallbackHandlesFileReadPath is the live reproduction of
// a second real production incident on the Dominion Hetzner deployment
// 2026-08-31: every execute_shell_command call for a step whose scope
// included Workflow/tectonicusadaytrading/schedule-runs.json (a plain file,
// not a directory) failed unconditionally with `mount(2) system call
// failed: Not a directory`, regardless of the command actually requested.
//
// generateMountScript's ReadPaths/WritePaths/BlockedWritePaths loops
// assumed every configured path was a directory: `mkdir -p "$absPath"`
// always creates a directory node (absPath never pre-exists -- step 3
// already tmpfs-hid the whole BaseDir), so bind-mounting a *file* source
// onto that freshly-created *directory* target failed at the kernel level.
// Combined with `set -e`, this aborted the whole script every time.
//
// Calls executeIsolatedMountNamespace directly (an unexported, same-package
// method) rather than going through ExecuteIsolated's Landlock-first
// selection, since Landlock's own path handling was never buggy (it stats
// each path and correctly masks directory-only rights for files) -- this
// test needs to exercise the mount-namespace fallback specifically,
// regardless of whether this environment's Landlock would normally be
// chosen first.
func TestMountNamespaceFallbackHandlesFileReadPath(t *testing.T) {
	root := t.TempDir()
	workflowDir := filepath.Join(root, "workflow")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", workflowDir, err)
	}
	scheduleFile := filepath.Join(workflowDir, "schedule-runs.json")
	if err := os.WriteFile(scheduleFile, []byte(`{"schedules":[]}`), 0644); err != nil {
		t.Fatalf("seed schedule-runs.json: %v", err)
	}

	isolator := &Isolator{
		ReadPaths:  []string{workflowDir, scheduleFile},
		WritePaths: []string{workflowDir},
		WorkDir:    workflowDir,
		// BaseDir must be set explicitly: see the identical note in
		// TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy.
		BaseDir: root,
	}

	command := fmt.Sprintf(`test "$(cat %q)" = '{"schedules":[]}'`, scheduleFile)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd, cleanup, err := isolator.executeIsolatedMountNamespace(ctx, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Skipf("mount-namespace fallback is unavailable in this environment (%v) -- not what this fix changes; run on the actual deployment host to verify it", err)
	}

	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("execute_shell_command-equivalent failed with a file in ReadPaths (the exact live incident): %v\noutput: %s", err, output)
	}
}
