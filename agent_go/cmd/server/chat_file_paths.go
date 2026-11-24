package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type chatFileTarget struct {
	Path    string `json:"path"`
	Preview bool   `json:"preview,omitempty"`
}

func chatFileVisible(rel string) bool {
	return rel != "." && rel != crewroot.TeamSetupFile && !sharedProjectHiddenPath(rel) && !wf.Private(rel)
}

// Only metadata for exact files, not a search. In particular this cannot turn
// a path from the transcript into a grant to read a different Crew.
func (api *StreamingAPI) handleChatFilePaths(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.AccessToken != nil || claims.Scope != "" ||
		claims.Provider == "bot_route" || claims.ExecutionPrincipal != nil || userAccessForClaims(claims).Disabled {
		externalError(w, http.StatusForbidden, "forbidden", "Open this chat in AgentWorks to find its files.")
		return
	}
	var request struct {
		Workspace string   `json:"workspace_path"`
		Paths     []string `json:"paths"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request) != nil || len(request.Paths) > 24 {
		externalError(w, http.StatusBadRequest, "invalid_paths", "Choose up to 24 file paths.")
		return
	}
	ctx := r.Context()
	root, clean := cleanWorkspaceReadPath(request.Workspace, false)
	ref, found := resolveCrewPath(ctx, claims.UserID, root)
	if !clean || !found || ref.Rest != "" || !userAllowedProduct(claims, "work") || crewAccessFor(claims, ref) != crewAccessOwner ||
		!workspaceReadAllowed(ctx, claims, root, logicalPathIsCaller, false) {
		externalError(w, http.StatusForbidden, "forbidden", "You cannot browse this project's files.")
		return
	}
	docs := fsutil.WorkspaceDocsRoot()
	resolver, err := crewroot.NewResolver(docs)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "files_unavailable", "Project files are unavailable.")
		return
	}
	roots, bound, err := resolver.Lookup(ref.Root)
	if err != nil || !bound {
		externalError(w, http.StatusNotFound, "files_unavailable", "Project files are unavailable.")
		return
	}
	guard := externalFileGuard(ctx, false)
	targets := make(map[string]chatFileTarget, len(request.Paths))
	var attachRoots []string
	// Attached folders are the viewer's existing account grants. No Team
	// member's grants are borrowed, and a scoped file guard is not bypassed.
	if guard == nil {
		for _, candidate := range request.Paths {
			if filepath.IsAbs(candidate) && !crewroot.Within(roots.Files, candidate) && !crewroot.Within(roots.Resources, candidate) && !crewroot.Within(docs, candidate) {
				grants, _ := workFolderAccessForClaims(ctx, claims)
				for _, grant := range workproduct.AvailableAttachedGrants(grants) {
					if dir, err := filepath.EvalSymlinks(grant.Path); err == nil {
						attachRoots = append(attachRoots, dir)
					}
				}
				break
			}
		}
	}

	for _, raw := range request.Paths {
		if len(raw) == 0 || len(raw) > 2048 || ctx.Err() != nil {
			continue
		}
		absolute := filepath.IsAbs(raw)
		clean, valid := cleanWorkspaceReadPath(strings.TrimPrefix(raw, "/"), false)
		if !valid || clean != strings.TrimPrefix(raw, "/") {
			continue
		}
		if absolute {
			if mapped, other, bound, err := resolver.Reverse(raw); bound || err != nil {
				if err == nil && other.Virtual == roots.Virtual && chatCrewFileExists(resolver, roots, guard, mapped, raw) {
					targets[raw] = chatFileTarget{Path: mapped, Preview: !strings.HasPrefix(mapped, roots.FilesPath()+"/")}
				}
			} else if !crewroot.Within(docs, raw) {
				for _, dir := range attachRoots {
					rel, err := filepath.Rel(dir, raw)
					if err == nil && crewroot.Within(dir, raw) && chatFileVisible(filepath.ToSlash(rel)) && chatRegularFile(dir, rel) {
						targets[raw] = chatFileTarget{Path: raw, Preview: true}
						break
					}
				}
			}
			continue
		}
		// A qualified workspace spelling is accepted only for this Crew.
		candidates := []string{path.Join(roots.FilesPath(), clean)}
		switch {
		case strings.HasPrefix(clean, "Crew/"):
			candidates = []string{clean}
		case strings.HasPrefix(clean, crewroot.FilesSegment+"/"):
			candidates = []string{path.Join(roots.Virtual, clean)}
		case roots.SeparateFiles():
			candidates = append(candidates, path.Join(roots.Virtual, clean))
		}
		for _, candidate := range candidates {
			if chatCrewFileExists(resolver, roots, guard, candidate, "") {
				targets[raw] = chatFileTarget{Path: candidate, Preview: !strings.HasPrefix(candidate, roots.FilesPath()+"/")}
				break
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	externalJSON(w, targets)
}

func chatCrewFileExists(resolver *crewroot.Resolver, roots crewroot.Roots, guard *wf.FolderGuard, candidate, absolute string) bool {
	rel, ok := strings.CutPrefix(candidate, roots.Virtual+"/")
	if !ok || !chatFileVisible(rel) || !guard.Allows(rel, false) ||
		!chatFileVisible(strings.TrimPrefix(rel, crewroot.FilesSegment+"/")) {
		return false
	}
	physical, bound, err := resolver.Resolve(candidate)
	if err != nil || !bound || (absolute != "" && physical != absolute) {
		return false
	}
	if settings := roots.SettingsDir(); settings != "" && crewroot.Within(settings, physical) {
		return false
	}
	for _, dir := range []string{roots.Resources, roots.Files} {
		if crewroot.Within(dir, physical) {
			relative, err := filepath.Rel(dir, physical)
			return err == nil && chatRegularFile(dir, relative)
		}
	}
	return false
}

func chatRegularFile(dir, rel string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()
	if externalNoSymlinks(root, rel) != nil {
		return false
	}
	info, err := root.Lstat(rel)
	return err == nil && info.Mode().IsRegular()
}
