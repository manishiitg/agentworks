package workflowfiles

import (
	"fmt"
	"path/filepath"
	"strings"
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
	parts := strings.Split(filepath.ToSlash(rel), "/")
	start := 0
	if len(parts) > 2 && parts[0] == "_users" {
		start = 2
	}
	end := start
	if len(parts) > start+1 && (parts[start] == "Workflow" || parts[start] == "Crew") {
		end = start + 2
	} else if len(parts) > start+3 && parts[start] == "Chats" && parts[start+2] == "projects" {
		end = start + 4
	} else if len(parts) > start+1 {
		end = start + 1
	}
	return filepath.Join(docs, filepath.FromSlash(strings.Join(parts[:end], "/"))), nil
}
