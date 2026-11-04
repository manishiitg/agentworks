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
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

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
	dir, e := crewTeamLocalDir(owner, c, project)
	if e != nil {
		externalError(w, 400, "team_unavailable", e.Error())
		return
	}
	var t crewTeam
	err := crewTeamRemoteJSON(r.Context(), owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(project), nil, &t)
	if err == nil && !t.CanEditSharedFolder {
		externalError(w, 403, "team_folder_forbidden", "Ask the owner to allow you to edit the shared folder.")
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
	externalJSON(w, map[string]any{"project_id": project, "local_folder": b.Root, "local_project_id": b.LocalProject, "status": status, "message": message, "last_synced": b.LastSynced, "skipped": crewTeamSkipped(b.Root)})
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
