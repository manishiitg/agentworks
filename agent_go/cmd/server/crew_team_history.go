package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
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
	Chunks []transferChunk `json:"chunks,omitempty"`
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
func (s crewTeamFileState) versionAt(path string, revision uint64) *chunkedFile {
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if e.Path == path && e.Revision <= revision {
			if e.File == nil {
				return nil
			}
			return &chunkedFile{Hash: e.File.Hash, Size: e.File.Size, Exec: e.File.Executable, Chunks: e.Chunks}
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
	s, e := readCrewTeamFileStateLocked(project)
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
		// with_text (agents): both versions' text, the file now and the other person's from history.
		withText := r.URL.Query().Get("with_text") != ""
		store, _ := crewTeamChunkStore(project)
		out := []crewTeamClashView{}
		for i := len(s.Clashes) - 1; i >= 0; i-- {
			c := s.Clashes[i]
			if !c.Resolved && allowed(c.Path) {
				view := crewTeamClashViewOf(store, s, c, withText && store != "")
				view.Source = "team_folder"
				view.WinningAuthor = shareOwnerLabel(c.WinningAuthor)
				view.LosingAuthor = shareOwnerLabel(c.LosingAuthor)
				out = append(out, view)
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
		if s.HistoryFloor > 0 && (left <= s.HistoryFloor || right <= s.HistoryFloor) {
			writeCrewTeamCatchUp(w, project, claims, team, s, min(left, right))
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
		if s.HistoryFloor > 0 && revision <= s.HistoryFloor {
			writeCrewTeamCatchUp(w, project, claims, team, s, revision)
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
		if e = assembleChunked(store, *file, f); e != nil {
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
			// Content is a merged text an agent wrote; it is saved as a new revision by the caller.
			Content *string `json:"content"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 3*crewTeamMergeMaxBytes)).Decode(&in) != nil || !allowed(in.Path) || in.TransferID == "" {
			externalError(w, 400, "invalid_resolution", "Choose the file and which version to keep.")
			return
		}
		if _, e = crewTeamRequestAuthorize(r, claims, project, in.Path, true, true); e != nil {
			externalError(w, 403, "team_folder_forbidden", e.Error())
			return
		}
		how := "keep_current"
		var write *crewTeamChange
		before := ""
		if current := s.Versions[in.Path].File; current != nil {
			before = current.Hash
		}
		switch {
		case in.Content != nil:
			// The agent's merged text, written like any save so every copy downloads it.
			if len(*in.Content) > crewTeamMergeMaxBytes {
				externalError(w, 400, "invalid_resolution", "The merged text is over 2 MiB; sort this file out in the Files panel.")
				return
			}
			store, err := crewTeamChunkStore(project)
			if err != nil {
				externalError(w, 409, "team_restore_failed", err.Error())
				return
			}
			executable := false
			if current := s.Versions[in.Path].File; current != nil {
				executable = current.Executable
			}
			version, err := chunkReader(strings.NewReader(*in.Content), int64(len(*in.Content)), executable, func(id string, data []byte) error { return putChunk(store, id, data) })
			if err != nil {
				externalError(w, 409, "team_restore_failed", err.Error())
				return
			}
			how, write = "content", &crewTeamChange{Path: in.Path, Before: before, After: &version}
		case in.Keep == "other":
			// The other person's version: the clash's losing side, put back as a new revision.
			c, ok := s.crewTeamOpenClash(in.Path, in.WinningRevision)
			if !ok {
				externalError(w, 404, "clash_not_found", "There is no open clash for this file; list the clashes first.")
				return
			}
			in.WinningRevision = c.WinningRevision
			how, write = "keep_other", &crewTeamChange{Path: in.Path, Before: before}
			if !s.crewTeamClashRemoved(c) {
				write.After = s.versionAt(in.Path, c.LosingRevision)
			}
		case in.Keep != "current":
			raw, ok := strings.CutPrefix(in.Keep, "revision:")
			revision, err := strconv.ParseUint(raw, 10, 64)
			if !ok || err != nil || revision > s.Revision {
				externalError(w, 400, "invalid_revision", "Choose keep current or other, or one of the saved revisions.")
				return
			}
			if s.HistoryFloor > 0 && revision <= s.HistoryFloor {
				writeCrewTeamCatchUp(w, project, claims, team, s, revision)
				return
			}
			how, write = "revision", &crewTeamChange{Path: in.Path, Before: before, After: s.versionAt(in.Path, revision)}
		}
		if in.WinningRevision == 0 {
			in.WinningRevision = s.Revision
		}
		if write != nil {
			// A kept version's chunks are still in the store: restoring is a new revision with them.
			change := *write
			if change.After != nil && len(change.After.Chunks) == 0 && change.After.Size > 0 {
				externalError(w, 409, "team_history_unavailable", "This version is unavailable.")
				return
			}
			wire, _ := json.Marshal(change.After)
			if _, e = commitCrewTeamChanges(r.Context(), project, claims.UserID, in.TransferID, crewTransferDigest("restore\x00"+in.Path+"\x00"+in.Keep+"\x00"+string(wire)), []crewTeamChange{change}); e != nil {
				externalError(w, 409, "team_restore_failed", e.Error())
				return
			}
			s, e = readCrewTeamFileStateLocked(project)
			if e != nil {
				externalError(w, 409, "team_history_unavailable", e.Error())
				return
			}
		}
		resolved := 0
		for i := range s.Clashes {
			if s.Clashes[i].Path == in.Path && s.Clashes[i].WinningRevision <= in.WinningRevision && !s.Clashes[i].Resolved {
				s.Clashes[i].Resolved = true
				resolved++
			}
		}
		if e = saveCrewTeamFileState(project, s); e != nil {
			externalError(w, 409, "team_restore_failed", e.Error())
			return
		}
		if resolved > 0 {
			log.Printf("[CREW_TEAM_SYNC] clash_resolved project=%s path=%q by=%s how=%s clashes=%d revision=%d", project, in.Path, claims.UserID, how, resolved, s.Revision)
			publishCrewPanelChanged(project)
		}
		externalJSON(w, map[string]any{"resolved": true, "revision": s.Revision})
	}
}

// crewTeamCompareVersions is a unified diff of two versions of one file, built from the chunk store (git diff of two
// private temp files, as the history compare showed before).
func crewTeamCompareVersions(ctx context.Context, project, dir string, left, right *chunkedFile, rel string) (string, error) {
	store, err := crewTeamChunkStore(project)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, "compare-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	write := func(name string, v *chunkedFile) (string, error) {
		path := filepath.Join(tmp, name)
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if v != nil {
			if err = assembleChunked(store, *v, f); err != nil {
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
