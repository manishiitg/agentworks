package crewsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Transfer Git objects through Git, never .git/config, hooks, credential files,
// worktree pointer files or the user's global configuration.
type GitSnapshot struct {
	Head            string `json:"head"`
	Before          string `json:"before,omitempty"`
	BeforeBranch    string `json:"before_branch,omitempty"`
	BeforeIndexHash string `json:"before_index_hash,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Bundle          []byte `json:"bundle,omitempty"`
	IndexPatch      []byte `json:"index_patch,omitempty"`
}

func gitCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	base := []string{"-c", "core.hooksPath=", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "protocol.file.allow=always", "-C", root}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	return cmd
}

func CaptureGit(ctx context.Context, root string, previous *GitSnapshot) (*GitSnapshot, error) {
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return nil, nil
	}
	top, err := gitCommand(ctx, root, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect this project's Git repository")
	}
	actual, _ := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	expected, _ := filepath.EvalSymlinks(root)
	if actual != expected {
		return nil, fmt.Errorf("open the Git repository's root folder before syncing")
	}
	head, err := gitCommand(ctx, root, "rev-parse", "--verify", "HEAD").Output()
	if err != nil {
		return nil, nil
	} // An unborn repository has files but no history yet.
	branch, _ := gitCommand(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	patch, err := gitCommand(ctx, root, "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot capture staged Git changes")
	}
	before := ""
	beforeIndex := ""
	beforeBranch := ""
	if previous != nil {
		before = previous.Head
		beforeIndex = gitPatchHash(previous.IndexPatch)
		beforeBranch = previous.Branch
	}
	out := &GitSnapshot{BeforeBranch: beforeBranch, BeforeIndexHash: beforeIndex, Head: strings.TrimSpace(string(head)), Before: before, Branch: strings.TrimSpace(string(branch)), IndexPatch: patch}
	if len(patch) > 64<<20 {
		return nil, fmt.Errorf("staged Git changes exceed 64 MiB")
	}
	if out.Head == before {
		return out, nil
	}
	tmp, err := os.MkdirTemp("", "agentworks-git-sync-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	archive := filepath.Join(tmp, "project.bundle")
	args := []string{"bundle", "create", archive, "HEAD"}
	// One current branch is transferred. Other local branches/remotes remain
	// local, preserving the user's existing repository rather than mirroring it.
	if before != "" && gitCommand(ctx, root, "merge-base", "--is-ancestor", before, out.Head).Run() == nil {
		args = append(args, "^"+before)
	}
	if err = gitCommand(ctx, root, args...).Run(); err != nil {
		return nil, fmt.Errorf("cannot bundle this Git branch")
	}
	info, err := os.Stat(archive)
	if err != nil {
		return nil, err
	}
	if info.Size() > 256<<20 {
		return nil, fmt.Errorf("Git history exceeds 256 MiB")
	}
	out.Bundle, err = os.ReadFile(archive)
	return out, err
}

var gitObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func gitPatchHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func CheckGit(ctx context.Context, root string, snapshot *GitSnapshot) error {
	if snapshot == nil {
		return nil
	}
	if !gitObjectID.MatchString(snapshot.Head) || snapshot.Before != "" && !gitObjectID.MatchString(snapshot.Before) || len(snapshot.Bundle) > 256<<20 || len(snapshot.IndexPatch) > 64<<20 {
		return fmt.Errorf("invalid Git snapshot")
	}
	if snapshot.Branch != "" && gitCommand(ctx, root, "check-ref-format", "refs/heads/"+snapshot.Branch).Run() != nil {
		return fmt.Errorf("invalid Git branch")
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		if snapshot.Before != "" {
			return fmt.Errorf("the destination Git repository was removed; restore it before syncing")
		}
		return nil
	}
	head, err := gitCommand(ctx, root, "rev-parse", "--verify", "HEAD").Output()
	current := strings.TrimSpace(string(head))
	if err == nil && current != snapshot.Before && current != snapshot.Head {
		return fmt.Errorf("Git changed here; resolve the branch changes before syncing")
	}
	currentBranch, _ := gitCommand(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	branch := strings.TrimSpace(string(currentBranch))
	if snapshot.Before != "" && branch != snapshot.BeforeBranch && branch != snapshot.Branch {
		return fmt.Errorf("Git branch changed here; resolve it before syncing")
	}
	if snapshot.Branch != "" {
		worktrees, e := gitCommand(ctx, root, "worktree", "list", "--porcelain").Output()
		if e != nil {
			return fmt.Errorf("cannot inspect this repository's worktrees")
		}
		at := ""
		actual, _ := filepath.EvalSymlinks(root)
		for _, line := range strings.Split(string(worktrees), "\n") {
			if strings.HasPrefix(line, "worktree ") {
				at = strings.TrimPrefix(line, "worktree ")
			}
			if line == "branch refs/heads/"+snapshot.Branch {
				resolved, _ := filepath.EvalSymlinks(at)
				if resolved != actual {
					return fmt.Errorf("the incoming branch is checked out in another worktree; choose a different branch before syncing")
				}
			}
		}
		ref, e := gitCommand(ctx, root, "rev-parse", "--verify", "refs/heads/"+snapshot.Branch).Output()
		value := strings.TrimSpace(string(ref))
		if e == nil && value != snapshot.Before && value != snapshot.Head {
			return fmt.Errorf("the destination branch has different work; resolve it before syncing")
		}
	}
	// Preserve independently staged changes, including changes with identical
	// working-tree bytes. The transfer never silently resets an outside index.
	patch, err := gitCommand(ctx, root, "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv").Output()
	if err != nil {
		return fmt.Errorf("cannot inspect staged changes before syncing")
	}
	if !(snapshot.BeforeIndexHash == "" && len(patch) == 0) && gitPatchHash(patch) != snapshot.BeforeIndexHash && string(patch) != string(snapshot.IndexPatch) {
		return fmt.Errorf("staged changes exist here; keep or resolve them before syncing")
	}
	return nil
}

func ApplyGit(ctx context.Context, root string, snapshot *GitSnapshot) error {
	if snapshot == nil {
		return nil
	}
	if err := CheckGit(ctx, root, snapshot); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		if err = gitCommand(ctx, root, "init", "--quiet").Run(); err != nil {
			return fmt.Errorf("cannot initialize the server Git repository")
		}
	}
	if len(snapshot.Bundle) > 0 {
		tmp, err := os.MkdirTemp("", "agentworks-git-sync-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		archive := filepath.Join(tmp, "project.bundle")
		if err = os.WriteFile(archive, snapshot.Bundle, 0600); err != nil {
			return err
		}
		if err = gitCommand(ctx, root, "bundle", "verify", archive).Run(); err != nil {
			return fmt.Errorf("Git bundle does not match this repository")
		}
		if err = gitCommand(ctx, root, "fetch", "--no-tags", archive, "HEAD").Run(); err != nil {
			return fmt.Errorf("cannot import the Git branch")
		}
	}
	if gitCommand(ctx, root, "cat-file", "-e", snapshot.Head+"^{commit}").Run() != nil {
		return fmt.Errorf("Git snapshot is missing its commit")
	}
	if snapshot.Branch != "" {
		if err := gitCommand(ctx, root, "update-ref", "refs/heads/"+snapshot.Branch, snapshot.Head).Run(); err != nil {
			return fmt.Errorf("cannot update the synced Git branch")
		}
		if err := gitCommand(ctx, root, "symbolic-ref", "HEAD", "refs/heads/"+snapshot.Branch).Run(); err != nil {
			return fmt.Errorf("cannot select the synced Git branch")
		}
	} else if err := gitCommand(ctx, root, "update-ref", "--no-deref", "HEAD", snapshot.Head).Run(); err != nil {
		return fmt.Errorf("cannot restore detached Git state")
	}
	// read-tree changes the index only. Working files are applied by the
	// checked content transfer, never checkout/reset --hard or a Git filter.
	if err := gitCommand(ctx, root, "read-tree", snapshot.Head).Run(); err != nil {
		return fmt.Errorf("cannot restore the Git index")
	}
	if len(snapshot.IndexPatch) > 0 {
		cmd := gitCommand(ctx, root, "apply", "--cached", "--binary", "-")
		cmd.Stdin = strings.NewReader(string(snapshot.IndexPatch))
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cannot restore staged Git changes")
		}
	}
	return nil
}
