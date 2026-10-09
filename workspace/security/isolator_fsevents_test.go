package security

import (
	"runtime"
	"strings"
	"testing"
)

// A command in the macOS sandbox may reach the FSEvents service (file watchers: Next.js, Vite, webpack), and still only the
// named Mach services, never arbitrary Mach IPC (PLAT-735).
func TestPrivateScratchProfileAllowsFileWatchersButNotArbitraryMachIPC(t *testing.T) {
	dir := t.TempDir()
	profile := (&Isolator{BaseDir: dir, WorkDir: dir, StrictAllowlist: true, AllowNetwork: true, PrivateScratch: true}).generateStrictSandboxProfile()
	if !strings.Contains(profile, `(global-name "com.apple.FSEvents")`) {
		t.Fatalf("file watching needs the FSEvents service:\n%s", profile)
	}
	if strings.Contains(profile, "(allow mach-lookup)\n") {
		t.Fatalf("a private-scratch profile must list its Mach services, not allow them all:\n%s", profile)
	}
}

// The command's temporary folder must be spelled with its real path on macOS: the sandbox allows writes to
// /private/tmp/... and refuses the same folder reached through the /tmp link, so TMPDIR=/tmp/... broke every temp-file write.
func TestSandboxTempFolderUsesItsRealPathOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("/tmp is a link only on macOS")
	}
	dir := t.TempDir()
	cmd, cleanup, err := (&Isolator{BaseDir: dir, WorkDir: dir, StrictAllowlist: true, AllowNetwork: true, PrivateScratch: true, ReadPaths: []string{dir}, WritePaths: []string{dir}}).ExecuteIsolated(t.Context(), `echo ok > "$TMPDIR/probe" && cat "$TMPDIR/probe"`, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("a command must be able to write to its own TMPDIR: %v %s", err, out)
	}
}
