package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Sharing an Artifact with the Team (owner, 25 Nov): an Artifact is one static page, so its Team copy is a plain
// self-contained HTML file in the Team's shared folder, under artifacts/. The Team's folder sync carries it to every
// teammate, whose Artifacts page lists it under From the Team. No links, data or permissions of its own: being in the
// Team is the access, as for every file in that folder.
const dashboardTeamAction = "team_share"

// dashboardTeamMaxBytes keeps one shared page within what a shared folder holds per file.
const dashboardTeamMaxBytes = crewSharedFileMaxBytes

var (
	dashboardTeamName  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}\.html$`)
	dashboardTeamMeta  = regexp.MustCompile(`(?i)<meta\s+name="agentworks-(artifact|shared-by)"\s+content="([^"]*)"`)
	dashboardTeamTitle = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
)

// crewTeamArtifactsDir is artifacts/ inside the folder this project's Team shares (team/ when it is one of them).
func crewTeamArtifactsDir(project string) (string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !t.On || len(t.Folder.Folders) == 0 {
		return "", dashboardFailure(409, "This project has no Team with a shared folder. Turn on its Team first (Team › Share), then share the page.")
	}
	folder := t.Folder.Folders[0]
	if slices.Contains(t.Folder.Folders, "team") {
		folder = "team"
	}
	return path.Join(folder, "artifacts"), nil
}

func (api *StreamingAPI) handleDashboardTeamShare(w http.ResponseWriter, r *http.Request, claims *UserClaims, target dashboardTarget, mode, id string) {
	if target.Kind != "crew" {
		externalError(w, 409, "crew_only", "Sharing with the Team is for projects.")
		return
	}
	if !target.Edit || !dashboardAuthoringAllowed(claims, &target) {
		externalError(w, 403, "forbidden", "Only people who build this project can share its pages with the Team.")
		return
	}
	if id == "" || (mode != "on" && mode != "off") {
		externalError(w, 400, "invalid_arguments", "Choose the page by its dashboard_id and mode on or off.")
		return
	}
	roots, found, err := crewStorageRoots(target.Root)
	if err != nil || !found {
		externalError(w, 409, "project_unavailable", "This project's files can't be reached right now.")
		return
	}
	dir, err := crewTeamArtifactsDir(roots.ID)
	if err != nil {
		dashboardError(w, err)
		return
	}
	rel := path.Join(dir, id+".html")
	if !dashboardTeamName.MatchString(path.Base(rel)) {
		externalError(w, 400, "invalid_arguments", "That is not a page of this project.")
		return
	}
	files, err := os.OpenRoot(roots.Files)
	if err != nil {
		dashboardError(w, err)
		return
	}
	defer files.Close()
	if mode == "off" {
		if err = files.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
			dashboardError(w, err)
			return
		}
		log.Printf("[DASHBOARD_TEAM] action=off actor=%s project=%s dashboard=%s", sanitizeUserIDForPath(claims.UserID), target.ID, id)
		externalJSON(w, map[string]any{"dashboard_id": id, "team_shared": false})
		return
	}
	got, err := dashboardWorkspaceRequest(r.Context(), dashboards.Request{Root: target.Root, Action: "get", ID: id})
	if err != nil {
		dashboardError(w, err)
		return
	}
	if got.Snapshot == nil || !got.Snapshot.Artifact {
		externalError(w, 409, "not_artifact", "Only Artifacts can be shared with the Team: a dashboard shows live project data that a copy can't carry. Share the project, or make the page an Artifact.")
		return
	}
	page, err := dashboards.SelfContained(got.Snapshot.Files)
	if err != nil {
		dashboardError(w, err)
		return
	}
	page = dashboardTeamStamp(page, id, got.Snapshot.Title, firstNonEmptyTrimmed(claims.Username, claims.Email, claims.UserID))
	if len(page) > dashboardTeamMaxBytes {
		externalError(w, 413, "too_large", fmt.Sprintf("With its images this page is %d MiB; a Team copy may be at most %d MiB. Use smaller images and share again.", len(page)>>20, dashboardTeamMaxBytes>>20))
		return
	}
	if err = files.MkdirAll(dir, 0o755); err == nil {
		err = files.WriteFile(rel, []byte(page), 0o644)
	}
	if err != nil {
		dashboardError(w, err)
		return
	}
	log.Printf("[DASHBOARD_TEAM] action=on actor=%s project=%s dashboard=%s bytes=%d", sanitizeUserIDForPath(claims.UserID), target.ID, id, len(page))
	externalJSON(w, map[string]any{"dashboard_id": id, "team_shared": true, "path": rel, "note": "A copy is in the Team's shared folder; teammates see it under From the Team once it syncs. Share again after changing the page."})
}

// dashboardTeamStamp names the page, its title and who shared it inside the copy, so the copy explains itself.
func dashboardTeamStamp(page, id, title, by string) string {
	stamp := `<meta name="agentworks-artifact" content="` + html.EscapeString(id) + `">` +
		`<meta name="agentworks-shared-by" content="` + html.EscapeString(by) + `">`
	if !dashboardTeamTitle.MatchString(page) && title != "" {
		stamp += "<title>" + html.EscapeString(title) + "</title>"
	}
	if at := strings.Index(strings.ToLower(page), "<head>"); at >= 0 {
		return page[:at+6] + stamp + page[at+6:]
	}
	return stamp + page
}

// crewTeamArtifact is one page in the Team's shared artifacts/ folder.
type crewTeamArtifact struct {
	ID, Title, By, Path string
	Updated             time.Time
}

func crewTeamArtifacts(roots dashboardTeamRoots) []crewTeamArtifact {
	dir, err := crewTeamArtifactsDir(roots.ID)
	if err != nil {
		return nil
	}
	files, err := os.OpenRoot(roots.Files)
	if err != nil {
		return nil
	}
	defer files.Close()
	folder, err := files.Open(dir)
	if err != nil {
		return nil
	}
	entries, _ := folder.ReadDir(-1)
	folder.Close()
	out := []crewTeamArtifact{}
	for _, entry := range entries {
		if entry.IsDir() || !dashboardTeamName.MatchString(entry.Name()) {
			continue
		}
		rel := path.Join(dir, entry.Name())
		a := crewTeamArtifact{ID: strings.TrimSuffix(entry.Name(), ".html"), Path: rel}
		if info, err := entry.Info(); err == nil {
			a.Updated = info.ModTime()
		}
		if f, err := files.Open(rel); err == nil {
			head, _ := io.ReadAll(io.LimitReader(f, 8<<10))
			f.Close()
			for _, m := range dashboardTeamMeta.FindAllSubmatch(head, -1) {
				if strings.EqualFold(string(m[1]), "shared-by") {
					a.By = html.UnescapeString(string(m[2]))
				}
			}
			if m := dashboardTeamTitle.FindSubmatch(head); m != nil {
				a.Title = strings.TrimSpace(html.UnescapeString(string(bytes.TrimSpace(m[1]))))
			}
		}
		if a.Title == "" {
			a.Title = a.ID
		}
		out = append(out, a)
	}
	return out
}

type dashboardTeamRoots struct{ ID, Files string }

// crewTeamArtifactPage reads one Team copy for the Artifacts page to show.
func crewTeamArtifactPage(_ context.Context, roots dashboardTeamRoots, name string) ([]byte, error) {
	if !dashboardTeamName.MatchString(name) {
		return nil, dashboardFailure(400, "That is not a Team page.")
	}
	dir, err := crewTeamArtifactsDir(roots.ID)
	if err != nil {
		return nil, err
	}
	files, err := os.OpenRoot(roots.Files)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	page, err := files.ReadFile(path.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, dashboardFailure(404, "This page is no longer shared with the Team.")
	}
	return page, err
}
