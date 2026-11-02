package crewsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
	BundlePath      string `json:"-"`
	IndexPath       string `json:"-"`
	IndexHash       string `json:"index_hash,omitempty"`
}

func gitCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	base := []string{"-c", "core.bigFileThreshold=16m", "-c", "core.packedGitWindowSize=16m", "-c", "core.packedGitLimit=32m", "-c", "pack.windowMemory=16m", "-c", "pack.deltaCacheSize=16m", "-c", "pack.threads=1", "-c", "core.hooksPath=", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "protocol.file.allow=always", "-C", root}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GIT_") {
			cmd.Env = append(cmd.Env, item)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	return cmd
}

func CaptureGit(ctx context.Context, root string, previous *GitSnapshot, privateStageRoot string) (*GitSnapshot, error) {
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
	if privateStageRoot == "" {
		return nil, fmt.Errorf("private sync staging directory is required")
	}
	patch, err := os.CreateTemp(privateStageRoot, "index-")
	if err != nil {
		return nil, err
	}
	keepPatch := false
	defer func() {
		patch.Close()
		if !keepPatch {
			_ = os.Remove(patch.Name())
		}
	}()
	indexHash, indexSize, err := captureIndex(ctx, root, patch)
	if err != nil {
		return nil, err
	}
	if err = patch.Close(); err != nil {
		return nil, err
	}
	before := ""
	beforeIndex := ""
	beforeBranch := ""
	if previous != nil {
		before = previous.Head
		beforeIndex = previous.IndexHash
		if beforeIndex == "" {
			beforeIndex = gitPatchHash(nil)
		}
		beforeBranch = previous.Branch
	}
	out := &GitSnapshot{BeforeBranch: beforeBranch, BeforeIndexHash: beforeIndex, Head: strings.TrimSpace(string(head)), Before: before, Branch: strings.TrimSpace(string(branch)), IndexPath: patch.Name(), IndexHash: indexHash}
	// Empty staging has no binary payload; HEAD probes do not create empty files.
	if indexSize == 0 {
		if err = os.Remove(patch.Name()); err != nil {
			return nil, err
		}
		out.IndexPath = ""
	}
	if out.Head == before {
		keepPatch = true
		return out, nil
	}
	tmp, err := os.MkdirTemp(privateStageRoot, "git-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()
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
	if err = os.Chmod(archive, 0600); err != nil {
		return nil, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return nil, err
	}
	if info.Size() > 256<<20 {
		return nil, fmt.Errorf("Git history exceeds 256 MiB")
	}
	out.BundlePath = archive
	keep = true
	keepPatch = true
	return out, nil
}

var gitObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func gitPatchHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// Git writes its patch through a bounded pipe instead of buffering Output().
// The bound still applies when Git reports a much larger binary staged file.
type limitedWriter struct {
	dst       io.Writer
	remaining int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("staged Git changes exceed 64 MiB")
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func captureIndex(ctx context.Context, root string, dst io.Writer) (string, int64, error) {
	h := sha256.New()
	w := &limitedWriter{io.MultiWriter(dst, h), 64 << 20}
	cmd := gitCommand(ctx, root, "diff", "--cached", "--binary", "--no-ext-diff", "--no-textconv")
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("cannot capture staged Git changes: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), (64 << 20) - w.remaining, nil
}

func CheckGit(ctx context.Context, root string, snapshot *GitSnapshot) error {
	if snapshot == nil {
		return nil
	}
	if !gitObjectID.MatchString(snapshot.Head) || snapshot.Before != "" && !gitObjectID.MatchString(snapshot.Before) {
		return fmt.Errorf("invalid Git snapshot")
	}
	if snapshot.IndexPath != "" {
		info, err := os.Lstat(snapshot.IndexPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
			return fmt.Errorf("invalid Git index artifact")
		}
		f, err := os.Open(snapshot.IndexPath)
		if err != nil {
			return err
		}
		err = CopyChecked(ctx, io.Discard, f, File{Hash: snapshot.IndexHash, Size: info.Size()})
		f.Close()
		if err != nil {
			return err
		}
	} else if snapshot.IndexHash != gitPatchHash(nil) {
		return fmt.Errorf("Git index artifact is missing")
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
	patchHash, size, err := captureIndex(ctx, root, io.Discard)
	if err != nil {
		return err
	}
	if !(snapshot.BeforeIndexHash == "" && size == 0) && patchHash != snapshot.BeforeIndexHash && patchHash != snapshot.IndexHash {
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
	if snapshot.BundlePath != "" {
		archive := snapshot.BundlePath
		info, err := os.Lstat(archive)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return fmt.Errorf("invalid Git bundle file")
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
	if snapshot.IndexPath != "" && snapshot.IndexHash != gitPatchHash(nil) {
		cmd := gitCommand(ctx, root, "apply", "--cached", "--binary", "-")
		f, err := os.Open(snapshot.IndexPath)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd.Stdin = f
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cannot restore staged Git changes")
		}
	}
	return nil
}
