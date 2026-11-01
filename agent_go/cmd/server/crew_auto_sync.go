package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewAutoSyncStatus struct {
	State     string `json:"state"`
	Message   string `json:"message,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

var crewAutoStates sync.Map
var crewSyncWakeups sync.Map

func wakeCrewSync(id string) {
	if v, ok := crewSyncWakeups.Load(id); ok {
		select {
		case v.(chan struct{}) <- struct{}{}:
		default:
		}
	}
}

func crewAutoStatus(id string) *crewAutoSyncStatus {
	if value, ok := crewAutoStates.Load(id); ok {
		status := value.(crewAutoSyncStatus)
		return &status
	}
	return nil
}
func setCrewAutoStatus(id, state, message string) {
	crewAutoStates.Store(id, crewAutoSyncStatus{State: state, Message: message, CheckedAt: time.Now().UTC().Format(time.RFC3339)})
}

// Metadata is only a trigger optimization. A periodic full capture also checks
// same-size edits whose modification times were preserved by an outside tool.
func crewSyncStamp(roots crewroot.Roots) string {
	hash := sha256.New()
	for _, area := range []struct{ name, root string }{{"files", roots.Files}, {"resources", roots.Resources}} {
		if area.name == "files" && !roots.SeparateFiles() {
			continue
		}
		_ = filepath.WalkDir(area.root, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprint(hash, "unavailable", p)
				return nil
			}
			rel, _ := filepath.Rel(area.root, p)
			key := area.name + "/" + filepath.ToSlash(rel)
			if p != area.root && !crewroot.SyncContentAllowed(key, e.IsDir()) {
				if e.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info, err := e.Info(); err == nil {
				fmt.Fprintf(hash, "%s\x00%d:%d:%d\n", key, info.Size(), info.ModTime().UnixNano(), info.Mode())
			}
			return nil
		})
	}
	// The user's index/HEAD may change without changing working bytes.
	for _, name := range []string{"HEAD", "index", "packed-refs", "refs/heads"} {
		p := filepath.Join(roots.Files, ".git", name)
		_ = filepath.WalkDir(p, func(p string, e fs.DirEntry, err error) error {
			if err == nil {
				if info, e := e.Info(); e == nil {
					fmt.Fprintf(hash, "%s:%d:%d", p, info.Size(), info.ModTime().UnixNano())
				}
			}
			return nil
		})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type crewSyncScan struct {
	stamp string
	at    time.Time
}

var crewSyncScans sync.Map

func crewSyncNeedsCapture(roots crewroot.Roots) bool {
	stamp := crewSyncStamp(roots)
	if old, ok := crewSyncScans.Load(roots.ID); ok {
		scan := old.(crewSyncScan)
		if scan.stamp == stamp && time.Since(scan.at) < time.Minute {
			return false
		}
	}
	return true
}
func crewSyncRememberScan(roots crewroot.Roots, stamp string) {
	crewSyncScans.Store(roots.ID, crewSyncScan{stamp, time.Now()})
}

func (api *StreamingAPI) startDesktopCrewSync() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		return cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		type worker struct {
			stop context.CancelFunc
			done chan struct{}
		}
		workers := map[string]worker{}
		defer func() {
			for _, active := range workers {
				active.stop()
				<-active.done
			}
		}()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			for id, active := range workers {
				select {
				case <-active.done:
					active.stop()
					delete(workers, id)
				default:
				}
			}
			state, err := workflowCLIStateRoot()
			if err == nil {
				entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
				live := map[string]bool{}
				for _, entry := range entries {
					if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
						continue
					}
					id := strings.TrimSuffix(entry.Name(), ".json")
					b, e := crewroot.ReadSync(id)
					if e != nil || b == nil || b.Side != "local" {
						continue
					}
					live[id] = true
					if _, running := workers[id]; !running {
						child, stop := context.WithCancel(ctx)
						finished := make(chan struct{})
						workers[id] = worker{stop, finished}
						go func() {
							defer close(finished)
							api.watchDesktopCrewSync(child, id)
						}()
					}
				}
				for id, active := range workers {
					if !live[id] {
						active.stop()
						<-active.done
						delete(workers, id)
						crewAutoStates.Delete(id)
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (api *StreamingAPI) watchDesktopCrewSync(ctx context.Context, id string) {
	claims := &UserClaims{UserID: GetDefaultUserID(), Username: GetDefaultUserID()}
	roots, err := api.crewSyncRoots(ctx, claims, id)
	if err != nil {
		message := "Your Crew folder is unavailable. Reconnect its drive or restore the folder; sync will retry automatically."
		if previous := crewAutoStatus(id); previous == nil || previous.Message != message {
			log.Printf("[CREW_SYNC_AUTO] project=%s phase=root_unavailable retry=true reason=%q", id, err.Error())
		}
		setCrewAutoStatus(id, "attention", message)
		return
	}
	// Watches make ordinary saves prompt; the timer recovers missed events,
	// newly created directories, disconnected servers and sleeping laptops.
	watcher, _ := fsnotify.NewWatcher()
	var events <-chan fsnotify.Event
	var failures <-chan error
	if watcher != nil {
		defer watcher.Close()
		events = watcher.Events
		failures = watcher.Errors
	}
	addWatches := func() {
		if watcher == nil {
			return
		}
		for _, root := range []string{roots.Files, roots.Resources} {
			_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !e.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(root, p)
				if p != root && !crewroot.SyncContentAllowed("files/"+filepath.ToSlash(rel), true) {
					return filepath.SkipDir
				}
				_ = watcher.Add(p)
				return nil
			})
		}
	}
	addWatches()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	wake := make(chan struct{}, 1)
	crewSyncWakeups.Store(id, wake)
	defer crewSyncWakeups.Delete(id)
	dirty := true
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				events = nil
			} else {
				dirty = true
			}
			continue
		case <-failures:
			dirty = true
			failures = nil
		case <-wake:
			dirty = true
		case <-ticker.C:
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || b.Side != "local" {
			return
		}
		if b.PendingAction == "" && b.Editing == "local" && !dirty && !crewSyncNeedsCapture(roots) {
			continue
		}
		stamp := crewSyncStamp(roots)
		dirty = false
		action := b.PendingAction
		if action == "" {
			action = "update"
			if b.Editing == "server" {
				action = "download"
			}
		}
		setCrewAutoStatus(id, "syncing", "")
		attempt, cancel := context.WithTimeout(ctx, 2*time.Minute)
		attempt = context.WithValue(attempt, UserContextKey, claims)
		body, _ := json.Marshal(map[string]string{"action": action})
		r, _ := http.NewRequestWithContext(attempt, http.MethodPost, "http://localhost/", bytes.NewReader(body))
		r = mux.SetURLVars(r, map[string]string{"project": id, "connection": b.ConnectionID})
		capture := &internalResponseCapture{}
		start := time.Now()
		api.runDesktopCrewSync(capture, r, false)
		cancel()
		if capture.status >= 400 {
			var failure struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(capture.body.Bytes(), &failure)
			message := failure.Error.Message
			if message == "" {
				message = "Sync could not finish. Your changes have been kept."
			}
			old := crewAutoStatus(id)
			setCrewAutoStatus(id, "attention", message)
			if old == nil || old.Message != message {
				log.Printf("[CREW_AUTO_SYNC] project=%s pair=%s direction=%s state=retry reason=%q duration_ms=%d", id, b.PairID, action, message, time.Since(start).Milliseconds())
			}
			dirty = true
		} else {
			setCrewAutoStatus(id, "synced", "")
			crewSyncRememberScan(roots, stamp)
		}
		addWatches()
	}
}
