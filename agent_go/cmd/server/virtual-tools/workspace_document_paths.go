package virtualtools

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

func normalizeWorkspaceDocumentPath(inputPath string) string {
	trimmed := strings.TrimSpace(inputPath)
	if trimmed == "" {
		return ""
	}
	if !filepath.IsAbs(trimmed) {
		return path.Clean(filepath.ToSlash(trimmed))
	}

	cleanAbs := filepath.Clean(trimmed)
	for _, prefix := range workspaceDocumentRoots() {
		if prefix == "" {
			continue
		}
		if cleanAbs == prefix {
			return "."
		}
		if strings.HasPrefix(cleanAbs, prefix+string(os.PathSeparator)) {
			rel, err := filepath.Rel(prefix, cleanAbs)
			if err == nil {
				return path.Clean(filepath.ToSlash(rel))
			}
		}
	}
	return path.Clean(filepath.ToSlash(trimmed))
}

func normalizeRequiredAbsoluteWorkspaceDocumentPath(inputPath, fieldName string) (string, error) {
	trimmed := strings.TrimSpace(inputPath)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("%s must be a full absolute path under the workspace docs root; got relative path %q", fieldName, inputPath)
	}
	relativePath := normalizeWorkspaceDocumentPath(trimmed)
	if relativePath == "" || strings.HasPrefix(relativePath, "/") {
		return "", fmt.Errorf("%s must be under an allowed workspace docs root; got %q. Allowed roots: %s", fieldName, filepath.Clean(trimmed), strings.Join(workspaceDocumentRoots(), ", "))
	}
	return relativePath, nil
}

func workspaceDocumentRoots() []string {
	roots := make([]string, 0, 8)
	if envRoot := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")); envRoot != "" {
		roots = append(roots, envRoot)
	} else if root := strings.TrimSpace(fsutil.WorkspaceDocsRoot()); root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "workspace-docs")
			if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
				roots = append(roots, candidate)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	roots = append(roots, "/app/workspace-docs", "/workspace-docs")
	return dedupeWorkspaceDocumentRoots(roots)
}

func dedupeWorkspaceDocumentRoots(roots []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	return out
}

func workspaceAbsolutePath(relativePath string) (string, error) {
	return fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), relativePath)
}
