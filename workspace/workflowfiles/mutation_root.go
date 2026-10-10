package workflowfiles

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// MutationRoot groups managed edits by their workflow or product project.
// The same physical root must be used by the browser, MCP and Builder writers.
func MutationRoot(docs, file string) (string, error) {
	docs, err := filepath.Abs(docs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(docs, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("mutation outside documents")
	}
	slash := filepath.ToSlash(rel)
	prefix := "" // the "_users/<owner>/" in front of an owner's path is kept in the result
	if ref, ok := workspaceref.Parse(slash); ok && ref.HasOwner() && ref.Logical() != "" {
		prefix = strings.TrimSuffix(slash, ref.Logical())
		slash = ref.Logical()
	}
	parts := strings.Split(slash, "/")
	start := 0
	end := start
	if len(parts) > start+1 && (parts[start] == "Workflow" || parts[start] == "Crew") {
		end = start + 2
	} else if len(parts) > start+3 && parts[start] == "Chats" && parts[start+2] == "projects" {
		end = start + 4
	} else if len(parts) > start+1 {
		end = start + 1
	}
	return filepath.Join(docs, filepath.FromSlash(prefix+strings.Join(parts[:end], "/"))), nil
}
