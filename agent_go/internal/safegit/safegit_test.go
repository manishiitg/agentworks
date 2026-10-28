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

// Linked worktrees and repos whose .git is a file keep their hostile config in
// the common/separate git dir, and their attributes in info/attributes or
// core.attributesFile. None of it may run; a gitdir outside the root is refused.
func TestGitFileCheckoutsDoNotRunConfiguredCommands(t *testing.T) {
	root := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "protocol.file.allow=always"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	main := filepath.Join(root, "main")
	os.MkdirAll(main, 0o700)
	git(main, "init", "--quiet")
	os.WriteFile(filepath.Join(main, "a.txt"), []byte("one\n"), 0o600)
	git(main, "add", ".")
	git(main, "commit", "--quiet", "-m", "init")
	wt := filepath.Join(root, "wt")
	git(main, "worktree", "add", "--quiet", "-b", "wtb", wt)
	sep := filepath.Join(root, "sep")
	sepGit := filepath.Join(root, "gitdirs", "sep")
	os.MkdirAll(sep, 0o700)
	os.MkdirAll(filepath.Dir(sepGit), 0o700)
	git(sep, "init", "--quiet", "--separate-git-dir", sepGit)
	os.WriteFile(filepath.Join(sep, "a.txt"), []byte("one\n"), 0o600)
	git(sep, "add", ".")
	git(sep, "commit", "--quiet", "-m", "init")

	markers := []string{}
	script := func(name string) string {
		marker := filepath.Join(t.TempDir(), name)
		markers = append(markers, marker)
		p := filepath.Join(t.TempDir(), name+".sh")
		os.WriteFile(p, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o700)
		return p
	}
	// Main repo: config in the common dir, attributes in info/attributes.
	git(main, "config", "filter.evil.clean", script("clean"))
	git(main, "config", "diff.evil.textconv", script("textconv"))
	git(main, "config", "core.fsmonitor", script("fsmonitor"))
	os.WriteFile(filepath.Join(main, ".git", "info", "attributes"), []byte("*.txt filter=evil diff=evil\n"), 0o600)
	// Separate git dir repo: attributes through core.attributesFile.
	attrs := filepath.Join(root, "attrs")
	os.WriteFile(attrs, []byte("*.txt filter=evil2 diff=evil2\n"), 0o600)
	git(sep, "config", "core.attributesFile", attrs)
	git(sep, "config", "filter.evil2.clean", script("clean2"))
	git(sep, "config", "diff.evil2.textconv", script("textconv2"))
	git(sep, "config", "core.fsmonitor", script("fsmonitor2"))
	for _, d := range []string{wt, sep} {
		os.WriteFile(filepath.Join(d, "a.txt"), []byte("one\ntwo changed\n"), 0o600)
	}
	clear := func() {
		for _, m := range markers {
			os.Remove(m)
		}
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
	plain := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
		cmd.Run()
	}
	for _, d := range []string{wt, sep} {
		clear()
		plain(d, "status", "--porcelain")
		plain(d, "diff", "HEAD", "--textconv")
		if fired() == 0 {
			t.Fatalf("control: unhardened git in %s ran nothing; the test proves nothing", filepath.Base(d))
		}
	}
	clear()
	ctx := context.Background()
	for _, d := range []string{wt, sep} {
		for _, args := range [][]string{{"status", "--porcelain=v2", "-z"}, {"diff", "HEAD", "--numstat"}, {"diff", "HEAD"}, {"blame", "--line-porcelain", "--", "a.txt"}} {
			CommandIn(ctx, root, d, nil, args...).Run()
		}
	}
	if n := fired(); n != 0 {
		t.Fatalf("%d repo-configured commands ran for .git-file checkouts", n)
	}
	// A .git file naming a git dir outside the root is refused, not followed.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "HEAD"), []byte("ref: refs/heads/x\n"), 0o600)
	bad := filepath.Join(root, "bad")
	os.MkdirAll(bad, 0o700)
	os.WriteFile(filepath.Join(bad, ".git"), []byte("gitdir: "+outside+"\n"), 0o600)
	if err := CommandIn(ctx, root, bad, nil, "rev-parse", "HEAD").Run(); err == nil {
		t.Fatal("a .git file pointing outside the root must be refused")
	}
}
