//go:build linux

package security

import (
	"os"
	"path/filepath"
	"testing"
)

// A deployment-installed CLI tool outside the standard system dirs (e.g.
// Dominion's /srv/dominion/tools/bin, holding the alpaca CLI) needs an
// explicit grant or every exec of it from a landlocked step fails with
// "Permission denied" (rc=126) -- not a missing-file error, an exec-rights
// one. Confirmed live: PLACE_PAPER_TRADES on Dominion hit exactly this on
// every run since the alpaca CLI was installed there.
func TestLandlockSystemReadPathsIncludesExtraDeploymentToolPaths(t *testing.T) {
	toolsDir := t.TempDir()
	binDir := filepath.Join(toolsDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", binDir)

	paths := landlockSystemReadPaths()

	found := false
	for _, p := range paths {
		if p == binDir {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q (from SANDBOX_EXTRA_SYSTEM_PATHS) in %v", binDir, paths)
	}
}

func TestLandlockSystemReadPathsAcceptsMultipleColonSeparatedExtraPaths(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", dirA+":"+dirB)

	paths := landlockSystemReadPaths()

	for _, want := range []string{dirA, dirB} {
		found := false
		for _, p := range paths {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q in %v", want, paths)
		}
	}
}

// A path that doesn't exist on disk is silently dropped by
// existingCanonicalPaths -- same behaviour as every other baseline entry --
// rather than failing the whole ruleset.
func TestLandlockSystemReadPathsDropsAMissingExtraPath(t *testing.T) {
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", "/does/not/exist/at/all")
	paths := landlockSystemReadPaths()
	for _, p := range paths {
		if p == "/does/not/exist/at/all" {
			t.Fatalf("a missing path must not appear in the resolved list: %v", paths)
		}
	}
}

func TestLandlockSystemReadPathsUnsetLeavesBaselineUnchanged(t *testing.T) {
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", "")
	withoutExtra := landlockSystemReadPaths()
	if len(withoutExtra) == 0 {
		t.Fatal("expected the standard system baseline even with no extra paths configured")
	}
}

// PLAT-283: the baseline used to enumerate a dozen /etc entries, so any tool
// that read one more config file died with EACCES -- a real pip install
// inside the shipped launcher failed on /etc/debian_version, and with just
// the distro files added, on /etc/mime.types next. All of /etc is granted
// read-only now; the write baseline must not follow.
func TestLandlockSystemReadPathsGrantAllOfEtcReadOnly(t *testing.T) {
	t.Setenv("SANDBOX_EXTRA_SYSTEM_PATHS", "")

	readable := false
	for _, p := range landlockSystemReadPaths() {
		if p == "/etc" {
			readable = true
			break
		}
	}
	if !readable {
		t.Fatal("expected all of /etc in the Landlock read baseline")
	}
	// A rule on /etc does not reach a symlink's target: on systemd-resolved
	// hosts /etc/resolv.conf points into /run, and granting "/etc" alone
	// killed every DNS lookup in the sandbox. The resolved target must be
	// granted explicitly.
	if target, err := filepath.EvalSymlinks("/etc/resolv.conf"); err == nil {
		found := false
		for _, p := range landlockSystemReadPaths() {
			if p == target {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected the resolved /etc/resolv.conf target %q in the read baseline", target)
		}
	}
	for _, p := range landlockSystemWritePaths(false, false) {
		if p == "/etc" || filepath.HasPrefix(p, "/etc/") {
			t.Fatalf("/etc must never be in the Landlock write baseline, got %v", landlockSystemWritePaths(false, false))
		}
	}
}

// A shared browser profile is never launched against the bare configured
// path -- HeadlessArgsForSession always uses a `<profile>-users/<id>` or
// `<profile>-workflows/<id>` sibling instead. Landlock rules don't follow
// sibling paths that merely share a string prefix, so the write baseline
// must grant those siblings explicitly or Chrome can't create its
// ProcessSingleton socket ("Failed to create socket directory"), confirmed
// live the moment SparkQuill first enabled AGENT_BROWSER_SHARED_PROFILE.
func TestLandlockSystemWritePathsCoverSharedProfileUserAndWorkflowSiblings(t *testing.T) {
	base := t.TempDir()
	profile := filepath.Join(base, "browser-profile")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_BROWSER_SHARED_PROFILE", profile)

	// The sibling roots are created on demand, so a deployment that never
	// had a project browser still grants the first one.
	want := []string{profile, profile + "-users", profile + "-workflows", profile + "-projects"}
	got := landlockSystemWritePaths(false, false)
	for _, w := range want {
		found := false
		for _, p := range got {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q in the Landlock write baseline, got %v", w, got)
		}
	}
}
