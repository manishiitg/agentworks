package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (api *StreamingAPI) handleDesktopTeamFolders(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	claims := GetUserFromContext(r.Context())
	roots, e := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if e != nil {
		externalError(w, 403, "team_forbidden", e.Error())
		return
	}
	b, e := crewroot.ReadSync(roots.ID)
	if e != nil || b == nil || b.ConnectionID != mux.Vars(r)["connection"] {
		externalError(w, 403, "team_forbidden", "Use this project's connected company.")
		return
	}
	rel := r.URL.Query().Get("path")
	folder := roots.Files
	if rel != "" {
		folder, e = crewTeamFolderPath(roots.Files, rel)
		if e != nil {
			externalError(w, 400, "invalid_folder", e.Error())
			return
		}
	}
	entries, e := os.ReadDir(folder)
	if e != nil {
		externalError(w, 409, "folder_unavailable", "This folder is unavailable.")
		return
	}
	folders := []string{}
	for _, entry := range entries {
		p := filepath.ToSlash(filepath.Join(rel, entry.Name()))
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && crewTeamAllowed(p+"/file.txt") && !publication.ExcludedDirectory(p) {
			folders = append(folders, p)
		}
	}
	// The size line before a choice (owner, 5 Nov): one folder's, or the Whole Crew's with whole=1.
	var count, bytes int64
	switch {
	case rel != "":
		count, bytes = crewTeamShareSize(roots.Files, crewTeamFolder{Folders: []string{rel}})
	case r.URL.Query().Get("whole") == "1":
		count, bytes = crewTeamShareSize(roots.Files, crewTeamFolder{Whole: true})
	}
	externalJSON(w, map[string]any{"folders": folders, "path": rel, "files": count, "bytes": bytes, "max_bytes": publication.MaxBytes, "max_files": publication.MaxFiles})
}

// crewTeamShareSize counts the files a share choice would sync from this Crew's folder, by the same rules as a sync.
func crewTeamShareSize(root string, share crewTeamFolder) (count, bytes int64) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if publication.ExcludedDirectory(rel) || !crewTeamAllowed(rel+"/file.txt") || !share.walks(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 && crewTeamAllowed(rel) && share.holds(rel) {
			if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
				count++
				bytes += info.Size()
			}
		}
		return nil
	})
	return count, bytes
}

// Only the desktop itself may inspect its saved account's local working path.
// Remote callers continue to use the folder-only server APIs.
func (api *StreamingAPI) handleDesktopTeamStatus(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	owner := GetUserFromContext(r.Context()).UserID
	c, e := desktopConnectionFor(owner, mux.Vars(r)["connection"])
	if e != nil {
		externalError(w, 404, "connection_missing", "Connect your company in the app first.")
		return
	}
	project := mux.Vars(r)["project"]
	var t crewTeam
	err := crewTeamRemoteJSON(r.Context(), owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &t)
	if err != nil {
		externalError(w, 409, "team_offline", "Your company is offline. Your downloaded folders remain on this computer.")
		return
	}
	if !t.FolderAccess {
		externalError(w, 403, "team_folder_forbidden", "Ask the owner to allow you to edit the shared folder.")
		return
	}
	dir, e := crewTeamLocalDir(owner, c, project, t.Folder.ID)
	if e != nil {
		externalError(w, 400, "team_unavailable", e.Error())
		return
	}
	b, e := readCrewTeamLocal(dir)
	if e != nil && err == nil {
		_, b, e = api.ensureCrewTeamLocal(r.Context(), owner, c, t)
	}
	if e != nil {
		externalError(w, 409, "team_unavailable", "Connect to your company once to download this shared folder.")
		return
	}
	status := b.Status
	message := b.Error
	if err != nil {
		status = "offline"
		message = "Saved on this Mac. Changes will send when connected."
	}
	// Skipped files, minus the ones synced on purpose; only single files can be chosen (exact paths, never folders).
	op := b.onPurpose()
	skipped, choosable := []string{}, []string{}
	for _, rel := range crewTeamSkipped(b.Root) {
		if op[rel] {
			continue
		}
		skipped = append(skipped, rel)
		if info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel))); err == nil && info.Mode().IsRegular() {
			choosable = append(choosable, rel)
		}
	}
	mine := []string{}
	for _, rel := range b.SyncAnyway {
		if !slices.Contains(b.StopSyncing, rel) {
			mine = append(mine, rel)
		}
	}
	externalJSON(w, map[string]any{"project_id": project, "local_folder": b.Root, "local_project_id": b.LocalProject, "status": status, "message": message, "last_synced": b.LastSynced, "file_count": len(b.LocalBase),
		"skipped": skipped, "skipped_files": choosable, "synced_on_purpose": mine, "previous_folders": crewTeamPreviousLocalFolders(dir, b), "progress": crewTeamRoundFor(dir)})
}

// setCrewTeamSyncAnyway is this person's own choice for one file that looks like it holds passwords or keys (owner,
// 5 Nov): on syncs it to everyone in the Team from the next round; off removes it from the shared folder and keeps it
// on this computer. Only files the folder skips can be chosen, by exact path.
func setCrewTeamSyncAnyway(ctx context.Context, dir, rel string, on bool) error {
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := readCrewTeamLocal(dir)
	if err != nil {
		return err
	}
	if on {
		info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() || !slices.Contains(crewTeamSkipped(b.Root), rel) || !crewTeamAllowed(rel, crewTeamOnPurpose{rel: true}) {
			return fmt.Errorf("choose one of the skipped files in this shared folder")
		}
		if !slices.Contains(b.SyncAnyway, rel) {
			b.SyncAnyway = append(b.SyncAnyway, rel)
		}
		b.StopSyncing = slices.DeleteFunc(b.StopSyncing, func(p string) bool { return p == rel })
	} else {
		if !slices.Contains(b.SyncAnyway, rel) && !slices.Contains(b.FolderOnPurpose, rel) {
			return fmt.Errorf("that file is not synced on purpose")
		}
		b.SyncAnyway = slices.DeleteFunc(b.SyncAnyway, func(p string) bool { return p == rel })
		if !slices.Contains(b.StopSyncing, rel) {
			b.StopSyncing = append(b.StopSyncing, rel)
		}
	}
	b.Status = "pending"
	if err = saveCrewTeamLocal(dir, b); err != nil {
		return err
	}
	log.Printf("[CREW_TEAM] local project=%s event=sync_anyway path=%q on=%t", b.Project, rel, on)
	return nil
}

func (api *StreamingAPI) handleDesktopTeamSyncAnyway(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	var input struct {
		Path string `json:"path"`
		On   bool   `json:"on"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&input) != nil || input.Path == "" {
		externalError(w, 400, "invalid_request", "Choose a skipped file.")
		return
	}
	dir, err := api.crewTeamLocalDirFor(r.Context(), GetUserFromContext(r.Context()).UserID, mux.Vars(r)["connection"], mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 409, "team_unavailable", err.Error())
		return
	}
	if err = setCrewTeamSyncAnyway(r.Context(), dir, input.Path, input.On); err != nil {
		externalError(w, 400, "sync_anyway_failed", err.Error())
		return
	}
	externalJSON(w, map[string]any{"path": input.Path, "on": input.On})
}

// crewTeamLocalDirFor is this computer's copy of a team's current shared folder.
func (api *StreamingAPI) crewTeamLocalDirFor(ctx context.Context, owner, connection, project string) (string, error) {
	c, err := desktopConnectionFor(owner, connection)
	if err != nil {
		return "", fmt.Errorf("Connect your company in the app first.")
	}
	var t crewTeam
	if err = crewTeamRemoteJSON(ctx, owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &t); err != nil {
		return "", fmt.Errorf("Your company is offline; try again when connected.")
	}
	if !t.FolderAccess {
		return "", fmt.Errorf("This team has no shared folder for you.")
	}
	dir, err := crewTeamLocalDir(owner, c, project, t.Folder.ID)
	if err != nil {
		return "", err
	}
	if _, err = readCrewTeamLocal(dir); err != nil {
		return "", fmt.Errorf("This shared folder is not on this computer yet.")
	}
	return dir, nil
}

// Names only: excluded bytes never enter shared content or diagnostics.
func crewTeamSkipped(root string) []string {
	out := []string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if publication.CredentialPath(rel) {
			out = append(out, rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && (publication.ExcludedDirectory(rel) || strings.HasPrefix(rel, "_system")) {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Previously downloaded files remain on this person's computer after a folder
// change. These paths are local status only, never another person's grant.
func crewTeamPreviousLocalFolders(dir string, current crewTeamLocal) []string {
	out := []string{}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		previous, err := readCrewTeamLocal(filepath.Join(filepath.Dir(dir), entry.Name()))
		if err == nil && previous.Project == current.Project && previous.Connection == current.Connection && previous.RemoteUser == current.RemoteUser && previous.FolderID != current.FolderID && previous.Root != current.Root {
			out = append(out, previous.Root)
		}
	}
	return out
}
