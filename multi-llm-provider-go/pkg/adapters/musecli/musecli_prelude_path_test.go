package musecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sweep prelude's `exec "$@"` runs where PATH is only the system folders; a bare "muse" there was not found and every confined launch died at once
// (Excellence, 2026-10-04). The launch must carry the absolute path this process resolves.
func TestPreludeArgvCarriesTheAbsoluteMuseBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "muse")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

	argv := musePreludeArgv(nil, []string{"muse", "--trust-workspace", "--model", "m"})
	want := []string{"sh", "-c", museSweepScript, "muse-sweep"}
	for i, w := range want {
		if argv[i] != w {
			t.Fatalf("prelude arg %d = %q, want %q (%v)", i, argv[i], w, argv)
		}
	}
	// argv[4] is the temp folder; the command follows it.
	if argv[5] != bin || argv[6] != "--trust-workspace" || argv[7] != "--model" || argv[8] != "m" {
		t.Fatalf("command after the prelude = %v, want %s and the original flags", argv[5:], bin)
	}

	// Only the bare name is replaced: a flag value or another argument named like it, and a later "muse", stay.
	argv = museArgvWithAbsoluteBinary([]string{"env", "XDG_CONFIG_HOME=/x", "muse", "--provider", "muse"})
	if argv[2] != bin || argv[4] != "muse" {
		t.Fatalf("only the first bare muse is replaced: %v", argv)
	}
	// The caller's slice is not modified.
	in := []string{"muse"}
	_ = museArgvWithAbsoluteBinary(in)
	if in[0] != "muse" {
		t.Fatalf("input modified: %v", in)
	}
}

func TestPreludeArgvKeepsTheBareNameWhenMuseIsNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	argv := museArgvWithAbsoluteBinary([]string{"muse", "--x"})
	if argv[0] != "muse" || argv[1] != "--x" {
		t.Fatalf("unchanged expected: %v", argv)
	}
}

// Runs the prelude the way a slot does: PATH is only the system folders, so only the absolute path put in by museArgvWithAbsoluteBinary can find the CLI.
// Both launch shapes: the exec lane starts with muse, the tmux lanes with `env KEY=VALUE ... muse`.
func TestPreludeLaunchRunsMuseWhenPathHasNoMuseFolder(t *testing.T) {
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/bin/sh\necho \"$@\" > '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "muse"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The service's PATH has the install folder; the sweep also scans TMPDIR, so point it at an empty folder.
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("TMPDIR", t.TempDir())

	shapes := map[string][]string{
		"exec lane": {"muse", "--trust-workspace", "hello"},
		"tmux lane": {"env", "XDG_CONFIG_HOME=/tmp/c", "XDG_DATA_HOME=/tmp/d", "muse", "--trust-workspace", "hello"},
	}
	for name, argv := range shapes {
		_ = os.Remove(out)
		launch := musePreludeArgv(nil, argv)
		cmd := exec.CommandContext(context.Background(), launch[0], launch[1:]...)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: prelude launch failed: %v\n%s", name, err, output)
		}
		got, err := os.ReadFile(out)
		if err != nil || strings.TrimSpace(string(got)) != "--trust-workspace hello" {
			t.Fatalf("%s: the fake muse did not run with its args: %v %q", name, err, got)
		}
	}
}
