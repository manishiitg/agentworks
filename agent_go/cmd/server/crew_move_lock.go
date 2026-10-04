package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 4: the server and the Crew move command must never both write a Crew. While the command copies a Crew
// it records the folder in <state root>/migrations/crew-move/active.json; the server reads that marker (cached for a
// second) and refuses to bind a turn to that Crew, and the workspace proxy refuses writes to its paths, until the
// marker is cleared. A marker whose process is gone (a crash) stops counting, so a dead command never locks a Crew.

const crewMoveActiveFile = "active.json"

type crewMoveActive struct {
	Folder    string `json:"folder"`
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	StartedAt string `json:"started_at"`
}

func crewMoveStateDir(stateRoot string) string {
	return filepath.Join(filepath.Clean(stateRoot), filepath.FromSlash(crewMoveJournalSubdir))
}

func writeCrewMoveActive(stateRoot, folder string) error {
	dir := crewMoveStateDir(stateRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	host, _ := os.Hostname()
	encoded, err := json.Marshal(crewMoveActive{Folder: folder, PID: os.Getpid(), Host: host, StartedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	return writeRegistryFileAtomically(filepath.Join(dir, crewMoveActiveFile), append(encoded, '\n'))
}

func clearCrewMoveActive(stateRoot, folder string) {
	path := filepath.Join(crewMoveStateDir(stateRoot), crewMoveActiveFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var active crewMoveActive
	if json.Unmarshal(raw, &active) == nil && active.Folder == folder {
		_ = os.Remove(path)
	}
}

var crewMoveCache struct {
	mu     sync.Mutex
	read   time.Time
	root   string
	active crewMoveActive
	live   bool
}

// crewMoveInProgress reports whether the Crew move command is moving the Crew with this folder name right now.
func crewMoveInProgress(folder string) bool {
	if folder == "" {
		return false
	}
	if testing.Testing() && strings.TrimSpace(os.Getenv("AGENTWORKS_STATE_ROOT")) == "" {
		return false
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return false
	}
	crewMoveCache.mu.Lock()
	defer crewMoveCache.mu.Unlock()
	if crewMoveCache.root != root || time.Since(crewMoveCache.read) > time.Second {
		crewMoveCache.root, crewMoveCache.read, crewMoveCache.live = root, time.Now(), false
		if raw, err := os.ReadFile(filepath.Join(crewMoveStateDir(root), crewMoveActiveFile)); err == nil {
			var active crewMoveActive
			if json.Unmarshal(raw, &active) == nil && crewMoveProcessAlive(active) {
				crewMoveCache.active, crewMoveCache.live = active, true
			}
		}
	}
	return crewMoveCache.live && crewMoveCache.active.Folder == folder
}

// crewMoveProcessAlive: a marker from another host is trusted (we cannot look), one from this host counts only while
// its process exists.
func crewMoveProcessAlive(active crewMoveActive) bool {
	host, _ := os.Hostname()
	if active.Host != "" && host != "" && active.Host != host {
		return true
	}
	if active.PID <= 0 {
		return false
	}
	err := syscall.Kill(active.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// errCrewBeingMoved is the refusal a turn or write gets while its Crew is being moved.
var errCrewBeingMoved = fmt.Errorf("this Crew is being moved to its new location; try again in a few minutes")

// crewMoveBlocksPath reports whether a workspace path lies in a Crew that is being moved (any spelling).
func crewMoveBlocksPath(clean string) bool {
	folder, _, ok := workspaceref.MustParse(clean).AnyCrewProject()
	return ok && crewMoveInProgress(folder)
}

// lockCrewMove takes the host-wide lock of the Crew move command (<state root>/migrations/crew-move/.lock, an exclusive flock
// that vanishes with the process, so a crash never leaves it stuck). A second command is refused, not queued.
func lockCrewMove(stateRoot string) (unlock func(), err error) {
	dir := crewMoveStateDir(stateRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another Crew move is running on this host (the lock %s is held): wait for it or find it with ps", filepath.Join(dir, ".lock"))
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}, nil
}
