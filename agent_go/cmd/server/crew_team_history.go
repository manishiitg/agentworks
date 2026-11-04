package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamEvent struct {
	Source   string         `json:"source"`
	Path     string         `json:"path"`
	Kind     string         `json:"kind"`
	Revision uint64         `json:"revision"`
	Actor    string         `json:"actor"`
	Author   string         `json:"author,omitempty"`
	At       string         `json:"time"`
	File     *crewsync.File `json:"file,omitempty"`
}
type crewTeamClash struct {
	Source          string `json:"source"`
	Path            string `json:"path"`
	WinningRevision uint64 `json:"winning_revision"`
	LosingRevision  uint64 `json:"losing_revision"`
	WinningAuthor   string `json:"winning_author"`
	LosingAuthor    string `json:"losing_author"`
	Resolved        bool   `json:"resolved"`
}

func (s crewTeamFileState) fileAt(path string, revision uint64) *crewsync.File {
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if e.Path == path && e.Revision <= revision {
			return e.File
		}
	}
	return nil
}

func (api *StreamingAPI) handleCrewTeamHistory(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project, op := mux.Vars(r)["project"], mux.Vars(r)["op"]
	rel := r.URL.Query().Get("path")
	if rel == "" {
		rel = "."
	}
	if _, e := crewTeamRequestAuthorize(r, claims, project, rel, r.Method == http.MethodPost, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	dir, _ := crewTeamContentDir(project)
	lockDir, _ := crewTeamDir(project)
	unlock, e := wf.LockWorkspace(r.Context(), lockDir)
	if e != nil {
		externalError(w, 409, "team_history_unavailable", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamRequestAuthorize(r, claims, project, rel, r.Method == http.MethodPost, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	s, e := readCrewTeamFileState(project)
	if e != nil {
		externalError(w, 409, "team_history_unavailable", e.Error())
		return
	}
	allowed := func(p string) bool {
		if !crewTeamAllowed(p) {
			return false
		}
		return claims.AccessToken == nil || claims.AccessToken.FileGuard == nil || claims.AccessToken.FileGuard.Allows(p, false)
	}
	switch op {
	case "events":
		out := []crewTeamEvent{}
		for i := len(s.Events) - 1; i >= 0; i-- {
			event := s.Events[i]
			if allowed(event.Path) {
				event.Source = "team_folder"
				event.Author = shareOwnerLabel(event.Actor)
				event.File = nil
				out = append(out, event)
			}
		}
		externalJSON(w, map[string]any{"events": out, "revision": s.Revision})
	case "clashes":
		out := []crewTeamClash{}
		for i := len(s.Clashes) - 1; i >= 0; i-- {
			c := s.Clashes[i]
			if !c.Resolved && allowed(c.Path) {
				c.Source = "team_folder"
				c.WinningAuthor = shareOwnerLabel(c.WinningAuthor)
				c.LosingAuthor = shareOwnerLabel(c.LosingAuthor)
				out = append(out, c)
			}
		}
		externalJSON(w, map[string]any{"clashes": out, "revision": s.Revision})
	case "compare":
		left, e1 := strconv.ParseUint(r.URL.Query().Get("left"), 10, 64)
		right, e2 := strconv.ParseUint(r.URL.Query().Get("right"), 10, 64)
		if e1 != nil || e2 != nil || left > s.Revision || right > s.Revision || !allowed(rel) {
			externalError(w, 400, "invalid_revision", "Choose a file and two revisions from Sync.")
			return
		}
		store := filesnapshots.Store{Root: filepath.Join(dir, "files"), Private: filepath.Join(dir, "history")}
		diff, err := store.CompareFolderRevisions(r.Context(), fmt.Sprint(left), fmt.Sprint(right), crewTeamKey(rel))
		if err != nil {
			externalError(w, 409, "team_history_unavailable", "These versions are unavailable.")
			return
		}
		externalJSON(w, map[string]any{"path": rel, "left": left, "right": right, "diff": diff})
	case "history":
		revision, e := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
		if e != nil || revision > s.Revision || !allowed(rel) {
			externalError(w, 400, "invalid_revision", "Choose a file and revision from Sync.")
			return
		}
		file := s.fileAt(rel, revision)
		if file == nil {
			w.Header().Set("X-Team-Deleted", "true")
			w.WriteHeader(204)
			return
		}
		// Finish the read into private staging first, so a missing/corrupt object
		// cannot become a successful response containing incomplete bytes.
		f, e := os.CreateTemp(dir, "history-read-")
		if e != nil {
			externalError(w, 409, "team_history_unavailable", e.Error())
			return
		}
		defer os.Remove(f.Name())
		defer f.Close()
		store := filesnapshots.Store{Root: filepath.Join(dir, "files"), Private: filepath.Join(dir, "history")}
		if e = store.CopyFolderRevision(r.Context(), fmt.Sprint(revision), crewTeamKey(rel), f); e != nil {
			externalError(w, 409, "team_history_unavailable", "This version is unavailable.")
			return
		}
		if _, e = f.Seek(0, io.SeekStart); e != nil {
			externalError(w, 409, "team_history_unavailable", e.Error())
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Team-Hash", file.Hash)
		_, _ = io.Copy(w, f)
	case "resolve":
		var in struct {
			Path            string `json:"path"`
			Keep            string `json:"keep"`
			WinningRevision uint64 `json:"winning_revision"`
			TransferID      string `json:"transfer_id"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || !allowed(in.Path) || in.TransferID == "" {
			externalError(w, 400, "invalid_resolution", "Choose the file and which version to keep.")
			return
		}
		if _, e = crewTeamRequestAuthorize(r, claims, project, in.Path, true, true); e != nil {
			externalError(w, 403, "team_folder_forbidden", e.Error())
			return
		}
		if in.WinningRevision == 0 {
			in.WinningRevision = s.Revision
		}
		if in.Keep != "current" {
			raw, ok := strings.CutPrefix(in.Keep, "revision:")
			revision, err := strconv.ParseUint(raw, 10, 64)
			if !ok || err != nil || revision > s.Revision {
				externalError(w, 400, "invalid_revision", "Choose one of the saved revisions.")
				return
			}
			file := s.fileAt(in.Path, revision)
			change := crewsync.Change{Path: crewTeamKey(in.Path), Before: s.Versions[in.Path].File, After: file}
			if file != nil {
				f, err := os.CreateTemp(dir, "history-restore-")
				if err != nil {
					externalError(w, 409, "team_history_unavailable", err.Error())
					return
				}
				defer os.Remove(f.Name())
				store := filesnapshots.Store{Root: filepath.Join(dir, "files"), Private: filepath.Join(dir, "history")}
				err = store.CopyFolderRevision(r.Context(), fmt.Sprint(revision), change.Path, f)
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
				if err != nil {
					externalError(w, 409, "team_history_unavailable", "This version is unavailable.")
					return
				}
				change.DataPath = f.Name()
			}
			files := s.manifest()
			if file == nil {
				delete(files, change.Path)
			} else {
				files[change.Path] = *file
			}
			if _, e = applyCrewTeamChanges(r.Context(), project, claims.UserID, in.TransferID, crewTransferDigest("restore\x00"+in.Path+"\x00"+in.Keep), crewsync.Snapshot{Files: files, Changes: []crewsync.Change{change}}); e != nil {
				externalError(w, 409, "team_restore_failed", e.Error())
				return
			}
			s, e = readCrewTeamFileState(project)
			if e != nil {
				externalError(w, 409, "team_history_unavailable", e.Error())
				return
			}
		}
		for i := range s.Clashes {
			if s.Clashes[i].Path == in.Path && s.Clashes[i].WinningRevision <= in.WinningRevision {
				s.Clashes[i].Resolved = true
			}
		}
		if e = fsutil.WriteJSONAtomic(filepath.Join(dir, "files.json"), s, 0600); e != nil {
			externalError(w, 409, "team_restore_failed", e.Error())
			return
		}
		externalJSON(w, map[string]any{"resolved": true, "revision": s.Revision})
	}
}
