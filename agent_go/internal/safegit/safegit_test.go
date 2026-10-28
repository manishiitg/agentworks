package safegit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A shared Crew's repo config can name commands (clean filter, fsmonitor,
// textconv) that plain git runs during status/diff. The helper must not run
// them; the control run shows the same repo does fire them unhardened.
func TestRepoConfiguredCommandsDoNotRun(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "--quiet")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600)
	git("add", ".")
	git("commit", "--quiet", "-m", "init")
	markers := map[string]string{"filter": filepath.Join(t.TempDir(), "filter"), "textconv": filepath.Join(t.TempDir(), "textconv"), "fsmonitor": filepath.Join(t.TempDir(), "fsmonitor")}
	script := func(name, marker string) string {
		p := filepath.Join(t.TempDir(), name+".sh")
		os.WriteFile(p, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o700)
		return p
	}
	git("config", "filter.evil.clean", script("clean", markers["filter"]))
	git("config", "diff.evil.textconv", script("textconv", markers["textconv"]))
	git("config", "core.fsmonitor", script("fsmonitor", markers["fsmonitor"]))
	os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil diff=evil\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo changed\n"), 0o600)
	clear := func() {
		for _, m := range markers {
			os.Remove(m)
		}
	}
	run := func(cmd *exec.Cmd) {
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
		cmd.Run()
	}
	fired := func() int {
		n := 0
		for _, m := range markers {
			if _, err := os.Stat(m); err == nil {
				n++
			}
		}
		return n
	}
	// Control: the old, unhardened call fires the repo's commands.
	clear()
	run(exec.Command("git", "-C", dir, "status", "--porcelain"))
	run(exec.Command("git", "-C", dir, "diff", "HEAD", "--textconv"))
	if fired() == 0 {
		t.Fatal("control: unhardened git did not run any configured command; the test proves nothing")
	}
	clear()
	ctx := context.Background()
	for _, args := range [][]string{{"status", "--porcelain=v2", "-z"}, {"diff", "HEAD", "--numstat"}, {"diff", "HEAD"}, {"blame", "--line-porcelain", "--", "a.txt"}} {
		Command(ctx, dir, nil, args...).Run()
	}
	if n := fired(); n != 0 {
		t.Fatalf("%d repo-configured commands ran through the hardened helper", n)
	}
}
