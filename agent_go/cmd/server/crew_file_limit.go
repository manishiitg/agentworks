package server

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The project's "too many files to track" verdict, shown on the Git row with
// ways to fix it (#3878, approved mockup). The server decided it while saving
// replies (filesnapshots.FileLimit); this only reads it, checks again, or moves
// one top-level folder out of the project.
//
//	op=file_limit        GET: the stored verdict
//	op=file_limit_check  POST: count again now
//	op=file_limit_move   POST folder=<top-level folder>: move it next to the
//	                     project, into "<project>-moved" (owner's default, #3878)
//	op=file_limit_ignore POST folder=<top-level folder>: add "/<folder>/" to the
//	                     project's own .gitignore, so file tracking leaves it
//	                     out; removing the line brings it back (#3878)
func (api *StreamingAPI) serveCrewFileLimit(w http.ResponseWriter, r *http.Request, roots crewroot.Roots, op string, store filesnapshots.Store) {
	owner := crewProjectOwnedByCaller(GetUserIDFromContext(r.Context()), roots.Virtual)
	reply := func(report filesnapshots.FileLimitReport, known bool, extra map[string]any) {
		out := map[string]any{"known": known, "report": report, "can_fix": owner && roots.Local}
		for k, v := range extra {
			out[k] = v
		}
		externalJSON(w, out)
	}
	if op == "file_limit" {
		report, known := store.FileLimit()
		reply(report, known, nil)
		return
	}
	if r.Method != http.MethodPost {
		externalError(w, 405, "method_not_allowed", "Use POST to check again, move or ignore a folder.")
		return
	}
	if op == "file_limit_check" {
		report, err := store.CheckFileLimit(r.Context())
		if err != nil {
			externalError(w, 500, "check_failed", "Could not count this project's files. Try again.")
			return
		}
		markCrewPanelChangesDirty(roots.Virtual)
		reply(report, true, nil)
		return
	}
	// Moving or ignoring changes the project's files: the owner's click is the approval.
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:write") {
		externalError(w, 403, "insufficient_scope", "Moving or ignoring a folder needs crews:write.")
		return
	}
	if !owner {
		externalError(w, 403, "forbidden", "Only the project's owner can move or ignore its folders. Ask them.")
		return
	}
	if !roots.Local {
		externalError(w, 400, "not_available", "Moving or ignoring folders is available for projects in a folder on your Mac. Remove or archive the folder from Files instead.")
		return
	}
	if err := crewroot.CheckSyncWrite(workspaceGitDocsRoot(), store.Root); err != nil {
		externalError(w, http.StatusConflict, "editing_elsewhere", err.Error())
		return
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	from := filepath.Join(store.Root, folder)
	if folder == "" || strings.ContainsAny(folder, `/\`) || folder == "." || folder == ".." || strings.HasPrefix(folder, ".") {
		externalError(w, 400, "invalid_folder", "Choose one of the folders listed on the Git row.")
		return
	}
	if info, err := os.Lstat(from); err != nil || !info.IsDir() {
		externalError(w, 404, "not_found", fmt.Sprintf("There is no folder %q in this project any more. Check again.", folder))
		return
	}
	if op == "file_limit_ignore" {
		if err := ignoreProjectFolder(store.Root, folder); err != nil {
			log.Printf("[FILE_LIMIT] ignore_failed project=%s folder=%q err=%v", roots.ID, folder, err)
			externalError(w, 500, "ignore_failed", "Could not update the project's .gitignore. Try again.")
			return
		}
		log.Printf("[FILE_LIMIT] ignored project=%s folder=%q", roots.ID, folder)
		report, err := store.CheckFileLimit(r.Context())
		markCrewPanelChangesDirty(roots.Virtual)
		extra := map[string]any{"ignored": folder}
		if err != nil {
			reply(filesnapshots.FileLimitReport{}, false, extra)
			return
		}
		reply(report, true, extra)
		return
	}
	// A linked worktree keeps its repository's absolute path; moving it by
	// renaming would break it. Git has to move those (git worktree move).
	if worktree := linkedWorktreeInside(from); worktree != "" {
		rel, _ := filepath.Rel(store.Root, worktree)
		externalError(w, http.StatusConflict, "has_worktrees", fmt.Sprintf("%s has a Git worktree inside (%s). Ask the agent to move it with git worktree move, then try again.", folder, filepath.ToSlash(rel)))
		return
	}
	targetDir := store.Root + "-moved"
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		externalError(w, 500, "move_failed", "Could not make the folder to move into.")
		return
	}
	to := filepath.Join(targetDir, folder)
	for i := 2; ; i++ {
		if _, err := os.Lstat(to); os.IsNotExist(err) {
			break
		}
		to = filepath.Join(targetDir, fmt.Sprintf("%s-%d", folder, i))
	}
	if err := os.Rename(from, to); err != nil {
		log.Printf("[FILE_LIMIT] move_failed project=%s folder=%q err=%v", roots.ID, folder, err)
		externalError(w, 500, "move_failed", fmt.Sprintf("Could not move %s. Close anything using it and try again.", folder))
		return
	}
	log.Printf("[FILE_LIMIT] moved project=%s folder=%q to=%q", roots.ID, folder, to)
	report, err := store.CheckFileLimit(r.Context())
	markCrewPanelChangesDirty(roots.Virtual)
	moved := filepath.Base(targetDir) + "/" + filepath.Base(to)
	if err != nil {
		reply(filesnapshots.FileLimitReport{}, false, map[string]any{"moved_to": moved})
		return
	}
	reply(report, true, map[string]any{"moved_to": moved})
}

// linkedWorktreeInside returns the first linked worktree (a folder whose .git
// is a file) under dir, looking a few levels deep where worktrees live.
func linkedWorktreeInside(dir string) string {
	found := ""
	_ = filepath.WalkDir(dir, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			return nil
		}
		if rel, _ := filepath.Rel(dir, p); strings.Count(rel, string(filepath.Separator)) >= 3 {
			return filepath.SkipDir
		}
		if info, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			if !info.IsDir() {
				found = p
			}
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

// ignoreProjectFolder adds "/<folder>/" to the project's own .gitignore, once.
// File tracking honours that file (filesnapshots); deleting the line undoes it.
func ignoreProjectFolder(root, folder string) error {
	path := filepath.Join(root, ".gitignore")
	line := "/" + folder + "/"
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	add := line + "\n"
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		add = "\n" + add
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(add); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
