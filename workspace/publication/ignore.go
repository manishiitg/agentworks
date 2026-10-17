package publication

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git's own ignore matcher avoids a second, subtly different .gitignore parser.
// No hooks, fsmonitor or project executable is run by this inspection.
func GitIgnored(ctx context.Context, root string) (map[string]bool, error) {
	out := map[string]bool{}
	var paths []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && ExcludedDirectory(rel) || !d.IsDir() && Excluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			paths = append(paths, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Cannot read this project's ignore rules. Check folder access and publish again: %w", err)
	}
	if len(paths) == 0 {
		return out, nil
	}
	input := strings.Join(paths, "\x00") + "\x00"
	run := func(gitDir string) ([]byte, error) {
		args := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=", "-C", root}
		if gitDir != "" {
			args = append(args, "-c", "core.bare=false", "--git-dir="+gitDir, "--work-tree="+root)
		}
		args = append(args, "check-ignore", "--no-index", "-z", "--stdin")
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Stdin = strings.NewReader(input)
		return cmd.Output()
	}
	ignored, err := run("")
	var status *exec.ExitError
	if errors.As(err, &status) && status.ExitCode() == 128 {
		// A repository failure (including nested submodules or bad local
		// configuration) must not discard its extra ignore rules.
		probe := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "rev-parse", "--show-toplevel")
		if _, probeErr := probe.Output(); probeErr == nil {
			return nil, fmt.Errorf("Project ignore rules could not be checked. Fix this project's Git configuration or publish its subproject separately.")
		}
		for dir := root; ; dir = filepath.Dir(dir) {
			if _, e := os.Lstat(filepath.Join(dir, ".git")); !errors.Is(e, os.ErrNotExist) {
				return nil, fmt.Errorf("Project ignore rules could not be checked. Fix this project's Git configuration and publish again.")
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}

		// Plain folders still use Git's actual matcher. This private empty
		// repository is only parser state, never a repository in the project.
		// Any other failure still returns an error instead of exporting ignored files.
		temp, e := os.MkdirTemp("", "agentworks-publication-ignore-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(temp)
		templates := filepath.Join(temp, "empty-templates")
		if e = os.Mkdir(templates, 0700); e != nil {
			return nil, e
		}
		gitDir := filepath.Join(temp, "parser.git")
		init := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=", "init", "--quiet", "--bare", "--template="+templates, gitDir)
		if e = init.Run(); e != nil {
			return nil, fmt.Errorf("Project ignore rules could not be checked. Install Git, reconnect your computer from Files, then publish again.")
		}
		ignored, err = run(gitDir)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !errors.As(err, &status) || status.ExitCode() != 1 {
			return nil, fmt.Errorf("Project ignore rules could not be checked. Install Git, reconnect your computer from Files, then publish again.")
		}
	}
	for _, p := range bytes.Split(ignored, []byte{0}) {
		if len(p) > 0 {
			out[string(p)] = true
		}
	}
	return out, nil
}
