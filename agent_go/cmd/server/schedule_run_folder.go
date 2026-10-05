package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var scheduledRunFolderPattern = regexp.MustCompile(`^iteration-([0-9]+)-sched$`)
var numberedRunFolderPattern = regexp.MustCompile(`^iteration-([0-9]+)(?:-(?:hook|sched|slack-[a-f0-9]+))?$`)

// allocateScheduledRunFolder binds one saved-schedule occurrence to an
// immutable folder. Exclusive mkdir is the cross-process arbiter. The marker
// makes restart/capacity resume idempotent for the same durable run ID.
func allocateScheduledRunFolder(workspace, runID string) (string, error) {
	return allocateImmutableRunFolder(workspace, runID, "sched", ".schedule-run-id")
}

func allocateSlackRunFolder(workspace, runID string) (string, error) {
	sum := sha256.Sum256([]byte(runID))
	return allocateImmutableRunFolder(workspace, runID, fmt.Sprintf("slack-%x", sum[:8]), ".slack-run-id")
}

// Exclusive mkdir and a durable producer marker are shared by non-interactive
// producers. The monotonically increasing sequence includes every run family.
func allocateImmutableRunFolder(workspace, runID, suffix, markerName string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		return "", fmt.Errorf("schedule run id is required")
	}
	lock := scheduleRunFileLock(workspace + "/run-folder-allocation")
	lock.Lock()
	defer lock.Unlock()

	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// Serialize marker lookup and allocation across server processes as well.
	// Exclusive mkdir alone lets a retry race past a just-created marker and
	// incorrectly allocate a second folder for the same delivery.
	allocationLock, err := openRunAllocationLock(root)
	if err != nil {
		return "", err
	}
	defer allocationLock.Close()
	if err := syscall.Flock(int(allocationLock.Fd()), syscall.LOCK_EX); err != nil {
		return "", err
	}
	defer syscall.Flock(int(allocationLock.Fd()), syscall.LOCK_UN)
	if err = root.MkdirAll("runs", 0700); err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil {
		return "", err
	}
	n := 0
	if raw, readErr := root.ReadFile(".schedule-sequence"); readErr == nil {
		if previous, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil && previous > n {
			n = previous
		}
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-"+suffix) {
			raw, readErr := root.ReadFile("runs/" + entry.Name() + "/" + markerName)
			if readErr == nil && string(raw) == runID {
				return entry.Name(), nil
			}
		}
		if match := numberedRunFolderPattern.FindStringSubmatch(entry.Name()); len(match) > 0 {
			if value, parseErr := strconv.Atoi(match[1]); parseErr == nil && value > n {
				n = value
			}
		}
	}
	for {
		n++
		folder := fmt.Sprintf("iteration-%d-%s", n, suffix)
		err = root.Mkdir("runs/"+folder, 0700)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		// With slots on, a step runs as the owner's slot account (a member of the host's shared group, which the
		// setgid parent folder passes on): the run folder must be group-writable or its code steps cannot write
		// their outputs (PLAT-502). Without slots it stays owner-only.
		if slots.Enabled() {
			if err := root.Chmod("runs/"+folder, 0o770|os.ModeSetgid); err != nil {
				return "", err
			}
		}
		marker, openErr := root.OpenFile("runs/"+folder+"/"+markerName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return "", openErr
		}
		_, writeErr := marker.WriteString(runID)
		closeErr := marker.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if err := root.WriteFile(".schedule-sequence", []byte(strconv.Itoa(n)), 0600); err != nil {
			return "", err
		}
		return folder, nil
	}
}

// openRunAllocationLock creates or opens the workspace's allocation lock file.
// os.Root's create-or-open can report ENOENT when another process creates the
// same file at that moment (it tries open, then exclusive create); the folder
// exists, so a not-found here is only that lost race — retry briefly.
func openRunAllocationLock(root *os.Root) (*os.File, error) {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		var lock *os.File
		lock, err = root.OpenFile(".run-allocation.lock", os.O_CREATE|os.O_RDWR, 0600)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil, err
}
