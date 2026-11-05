package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
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
	// Chunks are that version's content in the Team's chunk store (crew_team_chunks.go).
	Chunks []crewTeamChunk `json:"chunks,omitempty"`
}
type crewTeamClash struct {
	Source          string `json:"source"`
	Path            string `json:"path"`
	WinningRevision uint64 `json:"winning_revision"`
	LosingRevision  uint64 `json:"losing_revision"`
	WinningAuthor   string `json:"winning_author"`
	LosingAuthor    string `json:"losing_author"`
	Resolved        bool   `json:"resolved"`
	// Merged: the two versions were merged by lines; Hunks overlapping parts took the winning (later) side.
	Merged bool `json:"merged,omitempty"`
	Hunks  int  `json:"hunks,omitempty"`
}

// versionAt is a file's version at a revision, from the events; nil when it did not exist then.
func (s crewTeamFileState) versionAt(path string, revision uint64) *crewTeamFileVersion {
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if e.Path == path && e.Revision <= revision {
			if e.File == nil {
				return nil
			}
			return &crewTeamFileVersion{Hash: e.File.Hash, Size: e.File.Size, Exec: e.File.Executable, Chunks: e.Chunks}
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
	team, _ := readCrewTeam(project)
	allowed := func(p string) bool {
		// History and restores are of files the caller may read now: inside what the team shares, not private.
		if !crewTeamAllowed(p, s.onPurpose()) || !team.Folder.holds(p) {
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
		diff, err := crewTeamCompareVersions(r.Context(), project, dir, s.versionAt(rel, left), s.versionAt(rel, right), rel)
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
		file := s.versionAt(rel, revision)
		if file == nil {
			w.Header().Set("X-Team-Deleted", "true")
			w.WriteHeader(204)
			return
		}
		store, err := crewTeamChunkStore(project)
		if err != nil {
			externalError(w, 409, "team_history_unavailable", err.Error())
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
		if e = crewTeamAssemble(store, *file, f); e != nil {
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
			// The kept version's chunks are still in the store: restoring is a new revision with them.
			before := ""
			if current := s.Versions[in.Path].File; current != nil {
				before = current.Hash
			}
			change := crewTeamChange{Path: in.Path, Before: before, After: s.versionAt(in.Path, revision)}
			if change.After != nil && len(change.After.Chunks) == 0 && change.After.Size > 0 {
				externalError(w, 409, "team_history_unavailable", "This version is unavailable.")
				return
			}
			if _, e = commitCrewTeamChanges(r.Context(), project, claims.UserID, in.TransferID, crewTransferDigest("restore\x00"+in.Path+"\x00"+in.Keep), []crewTeamChange{change}); e != nil {
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

// crewTeamCompareVersions is a unified diff of two versions of one file, built from the chunk store (git diff of two
// private temp files, as the history compare showed before).
func crewTeamCompareVersions(ctx context.Context, project, dir string, left, right *crewTeamFileVersion, rel string) (string, error) {
	store, err := crewTeamChunkStore(project)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "compare-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	write := func(name string, v *crewTeamFileVersion) (string, error) {
		path := filepath.Join(tmp, name)
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if v != nil {
			if err = crewTeamAssemble(store, *v, f); err != nil {
				return "", err
			}
		}
		return path, nil
	}
	a, err := write("a", left)
	if err != nil {
		return "", err
	}
	b, err := write("b", right)
	if err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", a, b).Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) { // 1 means "they differ"
		return "", err
	}
	text := strings.ReplaceAll(string(out), a, rel)
	text = strings.ReplaceAll(text, b, rel)
	if len(text) > 1<<20 {
		text = text[:1<<20]
	}
	return text, nil
}
