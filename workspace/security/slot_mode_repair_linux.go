//go:build linux

package security

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// RepairGitModesArg is the Landlock launcher's subcommand that gives the slot's group back what the slot itself has
// on its own files inside the .git trees under the working folder (RepairOwnGitModes).
const RepairGitModesArg = "repair-git-modes"

// RepairOwnGitModes copies the owner's permission bits to the group on every file and folder inside a .git tree
// under root (linked worktrees' .git/worktrees included) that the calling account owns and whose group lacks one of
// them: 0600 becomes 0660, 0400 0440, 0700 0770. Older slot launches left git's files owner-only, which hides them
// from the platform's account in the slot's group. Bits for others are never changed, symlinks are never followed,
// files another account owns are left alone, and node_modules is not searched. Returns the number changed.
func RepairOwnGitModes(root string) (int, error) {
	uid := uint32(os.Getuid())
	changed := 0
	var firstErr error
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable: nothing of ours to repair there
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() && d.Name() == "node_modules" {
			return fs.SkipDir
		}
		if !inGitTree(root, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid {
			return nil
		}
		mode := st.Mode & 0o7777
		if want := groupLikeOwner(mode); want != mode {
			if err := syscall.Chmod(path, want); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("chmod %s: %w", path, err)
				}
				return nil
			}
			changed++
		}
		return nil
	})
	if err == nil {
		err = firstErr
	}
	return changed, err
}

// groupLikeOwner adds to the group the owner's read, write and search bits; nothing else changes.
func groupLikeOwner(mode uint32) uint32 { return mode | (mode&0o700)>>3 }

// inGitTree reports a path at or below a .git folder (or a worktree's .git file) relative to root.
func inGitTree(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == ".git" {
			return true
		}
	}
	return false
}

// RepairSlotGitModes runs RepairOwnGitModes as slot in dir, through slotctl and the slot's Landlock launcher (already on
// slotctl's allow-list), so only files that account owns change. Returns how many it changed.
func RepairSlotGitModes(ctx context.Context, slot, dir string) (int, error) {
	runner, err := landlockRunnerPath()
	if err != nil {
		return 0, err
	}
	home := slots.HomeOf(slot)
	if home == "" {
		home = dir
	}
	cmd := exec.CommandContext(ctx, runner, RepairGitModesArg)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
	wrapped, err := slots.WrapCommand(ctx, cmd, slot)
	if err != nil {
		return 0, err
	}
	wrapped.Dir = "/"
	out, runErr := wrapped.CombinedOutput()
	n := -1
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "files="); ok {
			n, _ = strconv.Atoi(value)
		}
	}
	if runErr != nil || n < 0 {
		return max(n, 0), fmt.Errorf("slot pass failed: %v: %s", runErr, strings.TrimSpace(string(out)))
	}
	return n, nil
}
