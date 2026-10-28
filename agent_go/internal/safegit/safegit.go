// Package safegit runs Git inside folders whose contents other people can
// write (shared Crews). Such a repo's own config and .gitattributes can name
// commands that Git would run during status or diff, so every call neutralizes
// them. All server-side Git on Crew folders must go through Command.
package safegit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Command builds a hardened git call in dir (a checkout whose .git is a
// directory). extraConfig holds key=value pairs such as
// "core.excludesFile=/path". A checkout whose .git is a file is refused here;
// use CommandIn with the Crew root.
//
// Filter and diff driver commands defined by the repo are blanked, so Git LFS
// filters do not run: LFS files show as pointer text in status and diff.
func Command(ctx context.Context, dir string, extraConfig []string, args ...string) *exec.Cmd {
	return CommandIn(ctx, dir, dir, extraConfig, args...)
}

func refused(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", "--version-refused-by-safegit")
	cmd.Env = env()
	return cmd
}

// confined resolves p and reports whether it lies inside root's real path.
func confined(root, p string) (string, bool) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	q, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(r, q)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return q, true
}

// linkedGitDir resolves a ".git" file (linked worktree or submodule) to its
// git dir. Both the git dir and its commondir must stay inside root.
func linkedGitDir(root, dir, file string) (string, bool) {
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > 4096 {
		return "", false
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", false
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	gitDir, ok := confined(root, target)
	if !ok {
		return "", false
	}
	if common, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(common))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		if _, ok := confined(root, c); !ok {
			return "", false
		}
	}
	return gitDir, true
}

// CommandIn is Command for checkouts that may have a .git file; every git
// dir it uses must resolve inside root.
func CommandIn(ctx context.Context, root, dir string, extraConfig []string, args ...string) *exec.Cmd {
	var full []string
	for _, c := range extraConfig {
		full = append(full, "-c", c)
	}
	full = append(full,
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.pager=cat",
		"-c", "core.quotepath=off",
		"-c", "core.sshCommand=",
		"-c", "safe.directory="+dir,
		"-c", "protocol.allow=never",
		"-c", "diff.external=",
		"-c", "gc.auto=0",
	)
	gitDir := filepath.Join(dir, ".git")
	if info, err := os.Lstat(gitDir); err == nil && info.Mode()&os.ModeSymlink == 0 {
		if !info.IsDir() {
			resolved, ok := linkedGitDir(root, dir, gitDir)
			if !ok {
				return refused(ctx)
			}
			gitDir = resolved
		}
		// Explicit git dir and work tree: the repo's own config (core.worktree)
		// or a .git file cannot redirect Git outside the folder.
		full = append(full, "--git-dir", gitDir, "--work-tree", dir)
		for _, c := range driverOverrides(ctx, dir, gitDir) {
			full = append(full, "-c", c)
		}
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = env()
	return cmd
}

func env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
}

// driverOverrides blanks every filter and diff driver command the repo's
// config defines. Reading config runs nothing.
func driverOverrides(ctx context.Context, dir, gitDir string) []string {
	cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory="+dir, "--git-dir", gitDir, "config", "--local", "--name-only", "--get-regexp", `^(filter|diff)\.`)
	cmd.Dir = dir
	cmd.Env = env()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var cfg []string
	for _, key := range bytes.Fields(out) {
		k := string(key)
		i, j := strings.Index(k, "."), strings.LastIndex(k, ".")
		if i == j {
			continue // filter.<x> without a driver name, e.g. diff.renames
		}
		kind, name := k[:i], k[i+1:j]
		if seen[kind+"."+name] {
			continue
		}
		seen[kind+"."+name] = true
		switch kind {
		case "filter":
			cfg = append(cfg, "filter."+name+".clean=", "filter."+name+".smudge=", "filter."+name+".process=", "filter."+name+".required=false")
		case "diff":
			cfg = append(cfg, "diff."+name+".textconv=", "diff."+name+".command=")
		}
	}
	return cfg
}
