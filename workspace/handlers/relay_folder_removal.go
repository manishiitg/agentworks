package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
)

// Only the exact draft root may bring its server-derived sibling namespace into
// deletion. Manifest fields and directory entries cannot select another release.
func relayDeletionReleases(docsRoot, target string) (string, bool, error) {
	rel, err := filepath.Rel(docsRoot, target)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if err != nil || len(parts) != 2 || parts[0] != "Workflow" || strings.HasPrefix(parts[1], ".") {
		return "", false, nil
	}
	if _, err := realRelayDeletionDir(docsRoot, target); err != nil {
		return "", false, err
	}
	root, err := os.OpenRoot(target)
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	raw, err := root.ReadFile("workflow.json")
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var manifest struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", false, err
	}
	if manifest.Kind != "relay" {
		return "", false, nil
	}
	releases := filepath.Join(docsRoot, filepath.FromSlash(workspaceref.RelayReleaseRoot(filepath.ToSlash(rel))))
	for _, dir := range []string{filepath.Dir(releases), releases} {
		exists, err := realRelayDeletionDir(docsRoot, dir)
		if err != nil {
			return "", true, err
		}
		if !exists {
			return "", true, nil
		}
	}
	return releases, true, nil
}

func realRelayDeletionDir(docsRoot, dir string) (bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("Flow deletion requires a real directory: %s", dir)
	}
	rel, err := filepath.Rel(docsRoot, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, fmt.Errorf("Flow deletion path is outside the documents root: %s", dir)
	}
	expectedRoot, err := filepath.EvalSymlinks(docsRoot)
	if err != nil {
		return false, err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false, err
	}
	if resolved != filepath.Join(expectedRoot, rel) {
		return false, fmt.Errorf("Flow deletion refuses redirected directories: %s", dir)
	}
	return true, nil
}
