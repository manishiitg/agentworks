package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// One Team tab on the Mac app and the website (owner, 6 Nov: "why two different ui"): its folder picker and sizes go
// through the server for every viewer. The server reads where the Crew is: a Crew on the server from its own folder,
// a Mac Crew from its owner's Mac over the Mac online relay while it is on. Off, the sizes come from the Team's store
// here and the screen says the Mac is off.

const crewTeamMacOffFolders = "Your Mac is offline, so its folders can't be listed. Open the Mac app, or type a folder name:"

// crewTeamForTab is the Team the caller may open on the Team tab: its owner and anyone in the Team (the /tab check).
func crewTeamForTab(w http.ResponseWriter, r *http.Request, write bool) (crewTeam, *UserClaims, bool) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, write) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return crewTeam{}, nil, false
	}
	owner := ownerOfCrewProjectID(project)
	t, err := readCrewTeam(project)
	if os.IsNotExist(err) && crewRegistryOwnedByCaller(claims.UserID, crewProjectRootByID(project)) {
		t, err = crewTeam{ProjectID: project, OwnerID: owner, People: []crewTeamPerson{}}, nil
	}
	if err != nil || !crewTeamCanManage(t, claims.UserID) || t.OwnerID != owner || !crewTeamAccountActive(owner) {
		externalError(w, 403, "team_forbidden", "This Crew isn't shared with you right now. Ask its owner to add you to its Team.")
		return crewTeam{}, nil, false
	}
	return t, claims, true
}

// crewTeamFolderListing is one folder of a Crew's files (rel, "" for the Crew itself) as the picker shows it: its
// subfolders the Team may share, and the size a share choice would sync (rel, or the Whole Crew with whole).
func crewTeamFolderListing(root, rel string, whole bool) (map[string]any, int, error) {
	folder := root
	if rel != "" {
		var err error
		if folder, err = crewTeamFolderPath(root, rel); err != nil {
			return nil, 400, err
		}
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, 409, fmt.Errorf("This folder is unavailable.")
	}
	folders := []string{}
	for _, entry := range entries {
		p := filepath.ToSlash(filepath.Join(rel, entry.Name()))
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && crewTeamAllowed(p+"/file.txt") && !publication.ExcludedDirectory(p) {
			folders = append(folders, p)
		}
	}
	var count, bytes int64
	switch {
	case rel != "":
		count, bytes = crewTeamShareSize(root, crewTeamFolder{Folders: []string{rel}})
	case whole:
		count, bytes = crewTeamShareSize(root, crewTeamFolder{Whole: true})
	}
	return crewTeamSizeReply(map[string]any{"folders": folders, "path": rel}, count, bytes), 200, nil
}

// crewTeamSizeReply adds a size to a folder listing.
func crewTeamSizeReply(out map[string]any, count, bytes int64) map[string]any {
	out["files"], out["bytes"] = count, bytes
	return out
}

// crewTeamTooBigLine is the one line that refuses sharing more than the Team's limits ("" when it fits).
func crewTeamTooBigLine(what string, count, bytes int64) string {
	switch {
	case count > crewTeamMaxFiles:
		return fmt.Sprintf("%s has %d files; the Team can share at most %d. Share folders instead.", what, count, crewTeamMaxFiles)
	case bytes > crewTeamMaxBytes:
		return fmt.Sprintf("%s is %.1f GB; the Team can share at most %d GB. Share folders instead.", what, float64(bytes)/(1<<30), crewTeamMaxBytes>>30)
	}
	return ""
}

// crewTeamWholeSize measures the Whole Crew where the Crew is: its folder on the server, or its owner's Mac over the
// Mac online relay. ok is false when it cannot be measured now (the Mac is off).
func crewTeamWholeSize(ctx context.Context, owner, project string) (count, bytes int64, ok bool) {
	if !crewRelayTwin(project) {
		roots, found, err := crewStorageRoots(crewProjectRootByID(project))
		if err != nil || !found {
			return 0, 0, false
		}
		count, bytes = crewTeamShareSize(roots.Files, crewTeamFolder{Whole: true})
		return count, bytes, true
	}
	pair, found := crewMacPairFor(owner, project)
	if !found {
		return 0, 0, false
	}
	result := callCrewMacPair(ctx, owner, project, pair, crewChatCall{Operation: "team_folders", Query: url.Values{"whole": {"1"}}}, 20*time.Second)
	var size struct {
		Files int64 `json:"files"`
		Bytes int64 `json:"bytes"`
	}
	if result.Status != http.StatusOK || json.Unmarshal(result.Body, &size) != nil {
		return 0, 0, false
	}
	return size.Files, size.Bytes, true
}

// handleCrewTeamFolders is GET /crew-teams/{project}/folders?path=&whole=1, the Team tab's folder picker and sizes.
func (api *StreamingAPI) handleCrewTeamFolders(w http.ResponseWriter, r *http.Request) {
	t, _, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	rel := strings.Trim(strings.TrimSpace(r.URL.Query().Get("path")), "/")
	whole := r.URL.Query().Get("whole") == "1"
	w.Header().Set("Cache-Control", "no-store")
	if !crewRelayTwin(t.ProjectID) {
		roots, found, err := crewStorageRoots(crewProjectRootByID(t.ProjectID))
		if err != nil || !found {
			externalError(w, 409, "folder_unavailable", "This Crew's folder is unavailable.")
			return
		}
		out, status, err := crewTeamFolderListing(roots.Files, rel, whole)
		if err != nil {
			externalError(w, status, "invalid_folder", err.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	result := crewChatResult{Status: http.StatusServiceUnavailable}
	if pair, ok := crewMacPairFor(t.OwnerID, t.ProjectID); ok {
		result = callCrewMacPair(r.Context(), t.OwnerID, t.ProjectID, pair, crewChatCall{Operation: "team_folders", Path: rel, Query: url.Values{"whole": {map[bool]string{true: "1"}[whole]}}}, 20*time.Second)
	}
	switch result.Status {
	case http.StatusOK:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(result.Body)
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		externalJSON(w, crewTeamStoreListing(t, rel, whole))
	default:
		externalError(w, result.Status, "invalid_folder", crewRelayErrorText(result.Body, "This folder is unavailable."))
	}
}

// crewTeamStoreListing is a Mac Crew's listing while its Mac is off: no folders, the size as the Team's store here
// keeps it, and the line that says why.
func crewTeamStoreListing(t crewTeam, rel string, whole bool) map[string]any {
	var count, bytes int64
	if s, err := readCrewTeamFileState(t.ProjectID); err == nil && (rel != "" || whole) {
		share := crewTeamFolder{Whole: true}
		if rel != "" {
			share = crewTeamFolder{Folders: []string{rel}}
		}
		for _, v := range s.held(share) {
			count++
			bytes += v.File.Size
		}
	}
	return crewTeamSizeReply(map[string]any{"folders": []string{}, "path": rel, "offline": crewTeamMacOffFolders}, count, bytes)
}

func crewRelayErrorText(body []byte, fallback string) string {
	var out struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &out) == nil && strings.TrimSpace(out.Error) != "" {
		return out.Error
	}
	return fallback
}

// desktopCrewTeamFolders is the Mac side of the listing above, for this Mac's own paired Crew (project is local).
func (api *StreamingAPI) desktopCrewTeamFolders(ctx context.Context, claims *UserClaims, project string, call crewChatCall) crewChatResult {
	reply := func(status int, v any) crewChatResult {
		data, _ := json.Marshal(v)
		return crewChatResult{ID: call.ID, Status: status, Body: data}
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return reply(409, map[string]string{"error": "This Crew's folder is unavailable on the Mac."})
	}
	out, status, err := crewTeamFolderListing(roots.Files, strings.Trim(call.Path, "/"), call.Query.Get("whole") == "1")
	if err != nil {
		return reply(status, map[string]string{"error": err.Error()})
	}
	return reply(200, out)
}
