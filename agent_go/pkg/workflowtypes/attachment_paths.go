package workflowtypes

import (
	"path/filepath"
	"strings"
)

// InternalAttachmentPath identifies platform runtime/cache trees, not user work.
// Check both the supplied and resolved path: a friendly symlink is not authority.
func InternalAttachmentPath(path string) bool {
	paths := []string{filepath.Clean(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		paths = append(paths, resolved)
	}
	for _, candidate := range paths {
		parts := strings.Split(filepath.ToSlash(candidate), "/")
		for i, part := range parts {
			switch part {
			case "cli-runtimes", "workflow-cli-runtimes", ".sandbox-cache":
				return true
			case "AgentWorks", "agentworks":
				if i+1 < len(parts) && parts[i+1] == "state" {
					return true
				}
			case "slots":
				if i+1 < len(parts) && (parts[i+1] == "state" || parts[i+1] == "run" || parts[i+1] == "home") {
					return true
				}
			}
		}
	}
	return false
}
