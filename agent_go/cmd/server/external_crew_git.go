package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Git review reads for chat and MCP (PLAT-746-200): the same reads as the Files
// review panel (/api/workspace-git) on one Crew's files, through the files
// actions git_status, git_diff, git_branch_compare and git_branch_diff. The
// Crew is resolved with the connection's Crew bounds like every Crew tool; a
// connection limited to some folders sees only repositories and files inside
// them, and private areas stay hidden. Pull request lookups use the Crew
// owner's GitHub connection, as the panel does.
var externalCrewGitOps = map[string]string{
	"crew_git_status":         "status",
	"crew_git_diff":           "diff",
	"crew_git_branch_compare": "branch_compare",
	"crew_git_branch_diff":    "branch_diff",
}

func (api *StreamingAPI) externalCrewGit(w http.ResponseWriter, r *http.Request, crewPath, name string, args map[string]any) {
	roots, found, err := crewStorageRoots(crewPath)
	if err != nil || !found {
		externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil {
		if !claims.AccessToken.Allows("crews:read") || !claims.AccessToken.AllowsCrew(roots.ID) {
			externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
			return
		}
	}
	guard := externalFileGuard(r.Context(), false)
	if err := guard.Validate(); err != nil {
		externalError(w, 403, "forbidden", "This connection cannot read these files.")
		return
	}
	prefix := ""
	if roots.SeparateFiles() {
		prefix = crewroot.FilesSegment + "/"
	}
	// readable is the Crew file-read rule for a path relative to the files root;
	// "" (a repository at the Crew root) needs a connection without folder limits.
	readable := func(rel string) bool {
		if rel == "" {
			return guard == nil
		}
		if !guard.Allows(prefix+rel, false) || crewCallerPrivatePath(prefix+rel) {
			return false
		}
		_, ok := confineSharedProjectPath(roots.Virtual, prefix+rel)
		return ok
	}
	join := func(repo, file string) string {
		if repo == "" {
			return file
		}
		return repo + "/" + strings.TrimSuffix(file, "/")
	}
	op := externalCrewGitOps[name]
	realBase, ok := workspaceGitConfined(roots.Files, roots.Files)
	if _, lookErr := exec.LookPath("git"); !ok || lookErr != nil {
		if op == "status" {
			externalJSON(w, map[string]any{"repos": []workspaceGitRepo{}})
		} else {
			externalError(w, 404, "not_found", "This Crew has no Git repository to read.")
		}
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), workspaceGitTimeout)
	defer cancel()
	if op == "status" {
		repos := []workspaceGitRepo{}
		for _, repo := range workspaceGitStatusRepos(ctx, realBase) {
			if !readable(repo.Root) {
				continue
			}
			files := repo.Files[:0]
			for _, file := range repo.Files {
				if readable(join(repo.Root, file.Path)) {
					files = append(files, file)
				}
			}
			repo.Files = files
			repos = append(repos, repo)
		}
		externalJSON(w, map[string]any{"repos": repos})
		return
	}
	repoRel := strings.Trim(externalArg(args, "repo"), "/")
	if !readable(repoRel) {
		externalError(w, 404, "not_found", "Repository not found or not allowed for this connection.")
		return
	}
	if file := externalArg(args, "file"); file != "" && !readable(join(repoRel, file)) {
		externalError(w, 404, "not_found", "File not found or not allowed for this connection.")
		return
	}
	repoDir, status, message := workspaceGitResolveRepo(realBase, repoRel)
	if status != 0 {
		externalError(w, status, "not_found", message)
		return
	}
	q := url.Values{"op": {op}}
	for _, key := range []string{"file", "base", "base_sha", "head_sha"} {
		if value := externalArg(args, key); value != "" {
			q.Set(key, value)
		}
	}
	read := r.Clone(r.Context())
	read.URL = &url.URL{Path: "/api/workspace-git", RawQuery: q.Encode()}
	recorder := httptest.NewRecorder()
	serveWorkspaceGitRead(recorder, read, ctx, repoDir, roots.Owner)
	if recorder.Code != http.StatusOK {
		var refusal struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &refusal)
		externalError(w, recorder.Code, "git_refused", firstNonEmptyTrimmed(refusal.Error, "Git could not read this."))
		return
	}
	if op == "branch_compare" {
		var compare workspaceGitBranchCompare
		if err := json.Unmarshal(recorder.Body.Bytes(), &compare); err != nil {
			externalFailure(w, err)
			return
		}
		files := compare.Files[:0]
		for _, file := range compare.Files {
			if readable(join(repoRel, file.Path)) {
				files = append(files, file)
			}
		}
		compare.Files = files
		externalJSON(w, compare)
		return
	}
	var value any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		externalFailure(w, err)
		return
	}
	externalJSON(w, value)
}
