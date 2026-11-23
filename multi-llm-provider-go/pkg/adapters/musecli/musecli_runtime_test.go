package musecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A private home deep in a runtime folder is past the unix socket path limit:
// Muse gets a short link to its run folder, the same one on every turn.
func TestMuseRuntimeDirIsShortAndStable(t *testing.T) {
	short := t.TempDir()
	dir, err := museRuntimeDirFor(short)
	if err != nil || dir == "" {
		t.Fatalf("short home: %q, %v", dir, err)
	}
	if len(dir) <= museShortSocketLimit {
		if dir != filepath.Join(short, "run") {
			t.Fatalf("a short home uses its own run folder, got %q", dir)
		}
	}
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", 80), ".sandbox-cache", "cli-home", "muse-cli")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := museRuntimeDirFor(deep)
	if err != nil || first == "" {
		t.Fatalf("deep home: %q, %v", first, err)
	}
	if len(first)+len("/muse/ms-12345678.sock.lease") > 104 {
		t.Fatalf("runtime folder %q (%d bytes) leaves no room for Muse's socket", first, len(first))
	}
	t.Cleanup(func() { _ = os.Remove(first) })
	if target, err := filepath.EvalSymlinks(first); err != nil || target != mustEval(t, filepath.Join(deep, "run")) {
		t.Fatalf("link %q resolves to %q (%v), want the run folder in the private home", first, target, err)
	}
	if info, err := os.Stat(filepath.Join(deep, "run")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("run folder must be private: %v %v", info, err)
	}
	if second, err := museRuntimeDirFor(deep); err != nil || second != first {
		t.Fatalf("not stable across turns: %q vs %q (%v)", second, first, err)
	}
	if none, err := museRuntimeDirFor("  "); err != nil || none != "" {
		t.Fatalf("no private home means no runtime folder: %q, %v", none, err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The launch prelude removes old probe folders and then becomes the real
// command; a fresh probe (a start in progress) and other files stay.
func TestMuseSweepPreludeRemovesOldProbesThenRunsTheCommand(t *testing.T) {
	tmp := t.TempDir()
	old := filepath.Join(tmp, museProbePrefix+"old")
	fresh := filepath.Join(tmp, museProbePrefix+"fresh")
	other := filepath.Join(tmp, "something-else")
	for _, d := range []string{old, fresh, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	marker := filepath.Join(t.TempDir(), "ran")
	argv := musePreludeArgv(&llmtypes.CallOptions{}, []string{"sh", "-c", "echo $1 > " + marker, "x", "real-command-ran"})
	out, err := exec.CommandContext(context.Background(), argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("prelude: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(marker); strings.TrimSpace(string(data)) != "real-command-ran" {
		t.Errorf("the real command did not run with its own arguments: %q", data)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an old probe folder survived the sweep")
	}
	for _, keep := range []string{fresh, other} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s must stay: %v", keep, err)
		}
	}
	// A missing temp folder must not stop the launch.
	argv = musePreludeArgv(&llmtypes.CallOptions{}, []string{"true"})
	argv[4] = filepath.Join(tmp, "does-not-exist")
	if out, err := exec.CommandContext(context.Background(), argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Errorf("a missing temp folder broke the launch: %v\n%s", err, out)
	}
}

// A refused message ("Message not sent -- another run is still starting")
// leaves the pane changed, so it must be recognised by its notice.
func TestMuseRejectedNotice(t *testing.T) {
	for pane, want := range map[string]bool{
		"... ! Message not sent — another run is still starting\n> hello": true,
		"! message not sent": true,
		"Ran 3 commands\n> ": false,
		"":                   false,
	} {
		if got := museRejectedNotice(pane); got != want {
			t.Errorf("museRejectedNotice(%q) = %v, want %v", pane, got, want)
		}
	}
}

func TestMuseRuntimeEnvDisablesAutoUpdateWhenConfined(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	if env := museRuntimeEnv(opts); env != nil {
		t.Fatalf("an unconfined launch keeps Muse's defaults, got %v", env)
	}
}
