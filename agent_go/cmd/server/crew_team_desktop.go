package server

import (
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
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
	var count, bytes int64
	if rel != "" {
		_ = filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == folder {
				return nil
			}
			sub, _ := filepath.Rel(folder, p)
			sub = filepath.ToSlash(sub)
			if d.IsDir() {
				if publication.ExcludedDirectory(sub) || !crewTeamAllowed(sub+"/file.txt") {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink == 0 && crewTeamAllowed(sub) {
				if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
					count++
					bytes += info.Size()
				}
			}
			return nil
		})
	}
	externalJSON(w, map[string]any{"folders": folders, "path": rel, "files": count, "bytes": bytes})
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
	externalJSON(w, map[string]any{"project_id": project, "local_folder": b.Root, "local_project_id": b.LocalProject, "status": status, "message": message, "last_synced": b.LastSynced, "skipped": crewTeamSkipped(b.Root), "previous_folders": crewTeamPreviousLocalFolders(dir, b), "progress": crewTeamRoundFor(dir)})
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
