package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/nativersync"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

var crewNativeLocks sync.Map

func crewNativeLock(roots crewroot.Roots, id string) *sync.Mutex {
	value, _ := crewNativeLocks.LoadOrStore(roots.Private+"/"+id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func pruneCrewNativeStage(stage string, b *musecli.NativeSessionBundle) error {
	return filepath.WalkDir(stage, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == stage || d.IsDir() {
			return nil
		}
		name, e := filepath.Rel(stage, path)
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 || b.Files[filepath.ToSlash(name)].Size == 0 {
			return os.Remove(path)
		}
		return nil
	})
}
func pruneOldCrewNativeStages(roots crewroot.Roots, id string) {
	root := filepath.Join(roots.Private, "native-sync")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if entry.Name() == id {
			continue
		}
		lock := crewNativeLock(roots, entry.Name())
		if !lock.TryLock() {
			continue
		}
		os.RemoveAll(filepath.Join(root, entry.Name()))
		lock.Unlock()
	}
}

// This private directory is outside both the project file snapshot and the
// CLI's writable home. Never use a path supplied by a transfer descriptor.
func crewNativeStage(roots crewroot.Roots, b *musecli.NativeSessionBundle) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	var total int64
	for _, f := range b.Files {
		total += f.Size
	}
	if total > nativersync.MaxBytes() {
		return "", fmt.Errorf("this native session exceeds the configured sync capacity")
	}
	root := roots.Private
	for _, part := range []string{"native-sync", b.SessionID, "journals"} {
		root = filepath.Join(root, part)
		if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private native session staging is unavailable")
		}
	}
	return root, nil
}

func stageCrewMuse(roots crewroot.Roots, b *musecli.NativeSessionBundle) error {
	if b == nil {
		return nil
	}
	lock := crewNativeLock(roots, b.SessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := b.ValidateFiles(); err != nil {
		return err
	}
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	for name := range b.Files {
		source := filepath.Join(b.Root, filepath.FromSlash(name))
		dest := filepath.Join(stage, filepath.FromSlash(name))
		if source == dest {
			continue
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		// Source files were checked by the provider exporter. Refuse any link
		// introduced after that check, including parent directory components.
		for p := source; p != b.Root; p = filepath.Dir(p) {
			i, e := os.Lstat(p)
			if e != nil || i.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("native journal changed during capture")
			}
		}
		in, e := os.Open(source)
		if e != nil {
			return e
		}
		out, e := os.CreateTemp(filepath.Dir(dest), ".capture-")
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.CopyN(out, in, b.Files[name].Size)
		in.Close()
		if closeErr := out.Close(); e == nil {
			e = closeErr
		}
		if e == nil {
			e = os.Rename(out.Name(), dest)
		}
		os.Remove(out.Name())
		if e != nil {
			return e
		}
	}
	b.Root = stage
	if err = pruneCrewNativeStage(stage, b); err != nil {
		return err
	}
	if err = b.ValidateFiles(); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), b, 0600)
}

func bindCrewMuseStage(roots crewroot.Roots, b *musecli.NativeSessionBundle) error {
	if b == nil {
		return nil
	}
	// Only an application-loaded complete mirror can set Root: it is excluded
	// from JSON. Keep its immutable files instead of rebinding to live staging.
	if b.Root != "" && crewroot.Within(filepath.Join(roots.Private, "native-mirrors"), b.Root) {
		return b.ValidateFiles()
	}
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	b.Root = stage
	return b.ValidateFiles()
}

func (api *StreamingAPI) handleCrewNativeSync(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "native_sync_unavailable", "Only this project's owner can sync its private session.")
		return
	}
	binding, err := crewroot.ReadSync(roots.ID)
	direction := r.URL.Query().Get("direction")
	if err != nil || binding == nil || binding.Side != "server" || binding.PairID != r.URL.Query().Get("pair") || (direction != "push" && direction != "pull") || (direction == "push" && binding.Editing != "local") {
		externalError(w, 409, "native_sync_unavailable", "Use the project's current sync connection.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	unlock, err := wf.LockWorkspace(ctx, roots.Files)
	if err != nil {
		externalError(w, 409, "native_sync_busy", err.Error())
		return
	}
	binding, err = crewroot.ReadSync(roots.ID)
	unlock()
	if err != nil || binding == nil || binding.PairID != r.URL.Query().Get("pair") || (direction == "push" && binding.Editing != "local") {
		externalError(w, 409, "native_sync_unavailable", "The active computer changed; sync again.")
		return
	}
	binary, err := nativersync.Binary()
	if err != nil {
		externalError(w, 503, "native_sync_unavailable", err.Error())
		return
	}
	log.Printf("[NATIVE_SYNC] project=%s helper=%s protocol=31", roots.ID, binary)
	conn, err := (&websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(15 * time.Minute))
	var b musecli.NativeSessionBundle
	if err = conn.ReadJSON(&b); err != nil {
		return
	}
	lock := crewNativeLock(roots, b.SessionID)
	if !lock.TryLock() {
		conn.WriteJSON(map[string]string{"error": "This session is syncing; retry shortly."})
		return
	}
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
	}()
	stage, err := crewNativeStage(roots, &b)
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}
	if direction == "pull" {
		var saved musecli.NativeSessionBundle
		raw, e := os.ReadFile(filepath.Join(filepath.Dir(stage), "manifest.json"))
		if e != nil || json.Unmarshal(raw, &saved) != nil || !reflect.DeepEqual(saved.Files, b.Files) || saved.Workspace != b.Workspace {
			conn.WriteJSON(map[string]string{"error": "The session changed; sync it again."})
			return
		}
	}
	if err = conn.WriteJSON(map[string]bool{"ready": true}); err != nil {
		return
	}
	stream := &nativersync.Socket{Conn: conn}
	err = nativersync.Server(ctx, stage, direction == "pull", stream)
	if err == nil && direction == "push" {
		b.Root = stage
		err = pruneCrewNativeStage(stage, &b)
		if err == nil {
			err = b.ValidateFiles()
		}
		lock.Unlock()
		locked = false
		if err == nil {
			briefUnlock, e := wf.LockWorkspace(ctx, roots.Files)
			if e != nil {
				err = e
			} else {
				current, e := crewroot.ReadSync(roots.ID)
				if e != nil || current == nil || current.PairID != binding.PairID || current.Editing != "local" {
					err = fmt.Errorf("the active computer changed; sync again")
				}
				if err == nil {
					if !lock.TryLock() {
						err = fmt.Errorf("the session changed; sync again")
					} else {
						locked = true
						err = b.ValidateFiles()
					}
				}
				if err == nil {
					err = fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), &b, 0600)
				}
				briefUnlock()
			}
		}
		if err == nil {
			pruneOldCrewNativeStages(roots, b.SessionID)
		}
	}
	result := map[string]any{"complete": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	conn.WriteJSON(result)
	log.Printf("[NATIVE_SYNC] project=%s provider=muse-cli native_session=%s direction=%s sent=%d received=%d complete=%t", roots.ID, b.SessionID, direction, stream.Sent, stream.Received, err == nil)
}

func desktopCrewNativeSync(ctx context.Context, roots crewroot.Roots, connection desktopConnection, binding *crewroot.SyncBinding, b *musecli.NativeSessionBundle, upload bool) error {
	if b != nil {
		lock := crewNativeLock(roots, b.SessionID)
		lock.Lock()
		defer lock.Unlock()
	}
	if b == nil {
		return nil
	}
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	if upload {
		if b.Root != stage {
			return fmt.Errorf("native export is not staged")
		}
		if err = b.ValidateFiles(); err != nil {
			return err
		}
	}
	server, err := desktopServerURL(connection.Server)
	if err != nil {
		return err
	}
	server.Scheme = map[string]string{"https": "wss", "http": "ws"}[server.Scheme]
	server.Path = "/api/crew-sync/" + url.PathEscape(binding.PeerProjectID) + "/native"
	direction := "pull"
	if upload {
		direction = "push"
	}
	server.RawQuery = url.Values{"pair": {binding.PairID}, "direction": {direction}}.Encode()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	header := http.Header{"Authorization": {"Bearer " + connection.Token}}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, server.String(), header)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return fmt.Errorf("native session sync could not connect: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(15 * time.Minute))
	go func() { <-ctx.Done(); conn.Close() }()
	if err = conn.WriteJSON(b); err != nil {
		return err
	}
	var ready struct {
		Ready bool
		Error string
	}
	if err = conn.ReadJSON(&ready); err != nil {
		return err
	}
	if !ready.Ready {
		return fmt.Errorf("native session sync: %s", ready.Error)
	}
	stream := &nativersync.Socket{Conn: conn}
	if err = nativersync.Client(ctx, stage, upload, stream); err != nil {
		return err
	}
	var result struct {
		Complete bool
		Error    string
	}
	if err = conn.ReadJSON(&result); err != nil {
		return err
	}
	if !result.Complete {
		return fmt.Errorf("native session sync: %s", result.Error)
	}
	b.Root = stage
	if err = pruneCrewNativeStage(stage, b); err != nil {
		return err
	}
	if err = b.ValidateFiles(); err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), b, 0600); err != nil {
		return err
	}
	pruneOldCrewNativeStages(roots, b.SessionID)
	log.Printf("[NATIVE_SYNC] project=%s provider=muse-cli native_session=%s direction=%s sent=%d received=%d complete=true", roots.ID, b.SessionID, direction, stream.Sent, stream.Received)
	return nil
}
