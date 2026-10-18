package server

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// This is operational file routing after the caller's existing history access
// checks. It never changes a transcript body, session identity or authority.
func chatHistoryStoragePath(input string) string {
	input = strings.TrimSpace(filepath.ToSlash(input))
	if filepath.IsAbs(input) {
		rel, err := filepath.Rel(fsutil.WorkspaceDocsRoot(), input)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return input
		}
		input = filepath.ToSlash(rel)
	}
	// An ownerless logical project path belongs to the caller's own tree.
	// It must never resolve by folder name to somebody else's Builder history.
	if !strings.HasPrefix(input, "_users/") && !strings.HasPrefix(input, "Crew/") {
		return input
	}
	return followCrewAlias("", input)
}

func chatHistoryLocalStoragePath(input string) string {
	current := chatHistoryStoragePath(input)
	if filepath.IsAbs(input) && !filepath.IsAbs(current) {
		return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(current))
	}
	return current
}

// Path-based native restoration historically accepts only the caller's own
// physical project. Validate a canonical spelling against that same identity;
// Builder history READ access remains with the separate existing read gates.
func ownCanonicalProjectRestorePath(user, cleaned string) (string, bool) {
	id, ok := projectIdentityOf(cleaned)
	if !ok || !id.Shared {
		return "", false
	}
	rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found || sanitizeUserIDForPath(rec.OwnerID) != sanitizeUserIDForPath(user) || validateProjectStorageRecord(rec) != nil {
		return "", false
	}
	root := workspaceref.SharedProjectPath(rec.Folder)
	if projectStorageRoot(rec) != root && !registryListHas(rec.Aliases, root) {
		return "", false
	}
	rest := strings.TrimPrefix(cleaned, root+"/")
	if !strings.HasPrefix(rest, "builder/conversation/") || !strings.HasSuffix(rest, ".json") {
		return "", false
	}
	return chatHistoryStoragePath(cleaned), true
}
