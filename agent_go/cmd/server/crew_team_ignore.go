package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// crewTeamIgnored is what the shared folder's own .gitignore files leave out, nothing else (review 5 Nov): never this
// machine's global excludes, a parent folder's .gitignore or a repository's .git/info/exclude, which differ from one
// copy to another. Git's own matcher reads the rules through a private empty repository with no user or system
// configuration, so every copy of the same folder ignores the same files.
func crewTeamIgnored(ctx context.Context, root string) (map[string]bool, error) {
	out := map[string]bool{}
	var paths []string
	rules := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		if d.IsDir() {
			if !crewsync.Allowed(crewTeamKey(rel), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".gitignore" {
			rules = true
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil || !rules || len(paths) == 0 {
		return out, err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("this shared folder has a .gitignore but Git isn't installed; install Git or remove the .gitignore")
	}
	temp, err := os.MkdirTemp("", "agentworks-team-ignore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	templates := filepath.Join(temp, "templates")
	if err = os.Mkdir(templates, 0o700); err != nil {
		return nil, err
	}
	gitDir := filepath.Join(temp, "parser.git")
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "HOME="+temp, "XDG_CONFIG_HOME="+temp)
	init := exec.CommandContext(ctx, "git", "init", "--quiet", "--bare", "--template="+templates, gitDir)
	init.Env = env
	if err = init.Run(); err != nil {
		return nil, fmt.Errorf("the shared folder's .gitignore could not be read: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.excludesFile="+os.DevNull, "-c", "core.bare=false", "-c", "core.fsmonitor=false",
		"--git-dir="+gitDir, "--work-tree="+root, "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	ignored, err := cmd.Output()
	var status *exec.ExitError
	if err != nil && !(errors.As(err, &status) && status.ExitCode() == 1) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the shared folder's .gitignore could not be read: %w", err)
	}
	for _, p := range bytes.Split(ignored, []byte{0}) {
		if len(p) > 0 {
			out[string(p)] = true
		}
	}
	return out, nil
}
