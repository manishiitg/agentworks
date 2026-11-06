package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filelock"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"io/fs"
	"os"

	"strings"
	"time"
)

// allocateScheduledRunFolder binds one saved-schedule occurrence to an
// immutable folder. Exclusive mkdir is the cross-process arbiter. The marker
// makes restart/capacity resume idempotent for the same durable run ID.
func allocateScheduledRunFolder(workspace, runID string) (string, error) {
	return allocateImmutableRunFolder(workspace, runID, "sched", ".schedule-run-id")
}

func allocateSlackRunFolder(workspace, runID string) (string, error) {
	return allocateImmutableRunFolder(workspace, runID, "slack", ".slack-run-id")
}

// Every producer uses a permanent folder derived from its durable invocation ID.
// No directory scan, numeric sequence, or archive rename is needed.
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
	if err := lockProjectFile(allocationLock); err != nil {
		return "", err
	}
	defer filelock.Unlock(allocationLock)
	if err = root.MkdirAll("runs", 0700); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(suffix + "\x00" + runID))
	folder := fmt.Sprintf("run-%x", sum[:16])
	markerPath := "runs/" + folder + "/" + markerName
	if raw, readErr := root.ReadFile(markerPath); readErr == nil {
		if string(raw) != runID {
			return "", fmt.Errorf("run folder identity mismatch")
		}
		return folder, nil
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return "", readErr
	}
	if err := root.MkdirAll("runs/"+folder, 0700); err != nil {
		return "", err
	}
	// The owner's execution slot must be able to write its own run outputs.
	if slots.Enabled() {
		if err := root.Chmod("runs/"+folder, 0o770|os.ModeSetgid); err != nil {
			return "", err
		}
	}
	if err := root.WriteFile(markerPath, []byte(runID), 0600); err != nil {
		return "", err
	}
	return folder, nil
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
