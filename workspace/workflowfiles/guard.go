package workflowfiles

import (
	"fmt"
	"path"
	"strings"
)

// FolderGuard narrows a connection's file rights. A nil guard preserves the
// workflow grant; a present guard with no write paths grants no writes.
type FolderGuard struct {
	ReadPaths         []string `json:"read_paths,omitempty"`
	WritePaths        []string `json:"write_paths,omitempty"`
	ReadOnlyPaths     []string `json:"read_only_paths,omitempty"`
	BlockedWritePaths []string `json:"blocked_write_paths,omitempty"`
	BlockedPaths      []string `json:"blocked_paths,omitempty"`
}

func (g *FolderGuard) Validate() error {
	if g == nil {
		return nil
	}
	for _, paths := range [][]string{g.ReadPaths, g.WritePaths, g.ReadOnlyPaths, g.BlockedPaths, g.BlockedWritePaths} {
		if len(paths) > 200 {
			return fmt.Errorf("at most 200 paths per file guard")
		}
		for _, p := range paths {
			clean, err := CleanRelative(p)
			if err != nil || clean != p {
				return fmt.Errorf("file guards require canonical relative paths")
			}
		}
	}
	return nil
}
func within(p string, paths []string) bool {
	p = strings.ToLower(p)
	for _, prefix := range paths {
		prefix = strings.ToLower(prefix)
		if prefix == "." || p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
func (g *FolderGuard) Allows(p string, write bool) bool {
	if g == nil {
		return true
	}
	if within(p, g.BlockedPaths) {
		return false
	}
	if write {
		return within(p, g.WritePaths) && !within(p, g.ReadOnlyPaths) && !within(p, g.BlockedWritePaths)
	}
	return within(p, g.ReadPaths) || within(p, g.WritePaths) || within(p, g.ReadOnlyPaths)
}

// ProtectedWrite is an unconditional policy. Folder grants cannot override it.
func ProtectedWrite(p string) bool {
	if p == "." || Private(p) {
		return true
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		if part == "planning" || part == "runs" || part == "human_inputs" || part == "chat_history" || part == "knowledgebase" || part == "db" || part == "workflow.json" || part == "plan.json" || part == "step_config.json" || part == ".env" || part == "credentials.json" || part == "token.json" {
			return true
		}
		ext := path.Ext(part)
		if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" || strings.HasSuffix(part, "-wal") || strings.HasSuffix(part, "-shm") {
			return true
		}
	}
	return false
}

// AllowsTraversal permits discovery of ancestors without granting file content.
func (g *FolderGuard) AllowsTraversal(p string) bool {
	if g == nil || g.Allows(p, false) {
		return true
	}
	if within(p, g.BlockedPaths) {
		return false
	}
	for _, paths := range [][]string{g.ReadPaths, g.WritePaths, g.ReadOnlyPaths} {
		for _, allowed := range paths {
			if within(allowed, []string{p}) {
				return true
			}
		}
	}
	return false
}
