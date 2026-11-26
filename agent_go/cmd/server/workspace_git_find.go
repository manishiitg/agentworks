package server

import (
	"bytes"
	"context"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/safegit"
)

// workspaceGitFoundFile is one project file found by op=find: Path is relative
// to workspace_path, Repo is its repository folder and File its path in it.
type workspaceGitFoundFile struct {
	Path string `json:"path"`
	Repo string `json:"repo"`
	File string `json:"file"`
}

const workspaceGitFindMaxPerRepo = 200000

// serveWorkspaceGitFind answers op=find from each repository's own file list
// (git ls-files: tracked plus new files that are not ignored), never a folder
// walk, so it stays fast on a large project with many checkouts.
//
//	mode=path  q is a relative path, possibly shortened at the front
//	           (cmd/server/main.go matches app/cmd/server/main.go).
//	mode=name  q is part of a file name (Ctrl+K file search).
//
// Main checkouts come before linked worktrees, so a path means the repository
// people work in rather than another agent's worktree. repos is 0 when the
// project has no repository; the caller then uses the folder search.
func serveWorkspaceGitFind(w http.ResponseWriter, ctx context.Context, realBase, q, mode string, limit int) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 300 || strings.ContainsAny(q, "\x00\\") {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
		return
	}
	if mode == "path" {
		clean, ok := workspaceGitRelFile(strings.TrimPrefix(q, "./"))
		if !ok {
			writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path"})
			return
		}
		q = clean
	} else {
		mode = "name"
		q = strings.ToLower(q)
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	dirs, _ := workspaceGitFindRepos(realBase)
	type repoDir struct {
		dir, root string
		worktree  bool
	}
	repos := make([]repoDir, 0, len(dirs))
	for _, dir := range dirs {
		real, ok := workspaceGitConfined(realBase, dir)
		if !ok {
			continue
		}
		rel, _ := filepath.Rel(realBase, real)
		if rel == "." {
			rel = ""
		}
		gitDir, common, ok := safegit.CheckoutInside(realBase, real)
		repos = append(repos, repoDir{dir: real, root: filepath.ToSlash(rel), worktree: ok && filepath.Clean(gitDir) != filepath.Clean(common)})
	}
	sort.SliceStable(repos, func(i, j int) bool { return !repos[i].worktree && repos[j].worktree })

	type hit struct {
		file  workspaceGitFoundFile
		order int
		rank  int
	}
	var hits []hit
	for order, repo := range repos {
		if ctx.Err() != nil {
			break
		}
		out, err := workspaceGitCommand(ctx, repo.dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for count, raw := range bytes.Split(out, []byte{0}) {
			if count >= workspaceGitFindMaxPerRepo {
				break
			}
			file := string(raw)
			if file == "" || seen[file] {
				continue
			}
			seen[file] = true
			rank := -1
			if mode == "path" {
				if file == q {
					rank = 0
				} else if strings.HasSuffix(file, "/"+q) {
					rank = 1
				}
			} else {
				name, full := strings.ToLower(path.Base(file)), strings.ToLower(file)
				switch {
				case name == q:
					rank = 0
				case strings.HasPrefix(name, q):
					rank = 1
				case strings.Contains(name, q):
					rank = 2
				case strings.Contains(full, q):
					rank = 3
				}
			}
			if rank < 0 {
				continue
			}
			full := file
			if repo.root != "" {
				full = repo.root + "/" + file
			}
			hits = append(hits, hit{file: workspaceGitFoundFile{Path: full, Repo: repo.root, File: file}, order: order, rank: rank})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if mode == "path" && a.order != b.order {
			return a.order < b.order
		}
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return len(a.file.Path) < len(b.file.Path)
	})
	files := []workspaceGitFoundFile{}
	for i := 0; i < len(hits) && i < limit; i++ {
		files = append(files, hits[i].file)
	}
	writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"files": files, "repos": len(repos)})
}
