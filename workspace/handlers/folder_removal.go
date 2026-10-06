package handlers

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Move a deleted folder out of the served tree before recursive cleanup. A
// sandbox may own private directories the service cannot traverse; RemoveAll
// on the live path would remove the manifest before failing on those files.
func removeFolderAtomically(docsDir, folderPath string) (bool, error) {
	root, err := filepath.Abs(docsDir)
	if err != nil {
		return false, err
	}
	target, err := filepath.Abs(folderPath)
	if err != nil {
		return false, err
	}
	if target == root {
		return false, fmt.Errorf("cannot delete the workspace documents root")
	}
	// This fresh service-owned 0700 directory is outside the documents API and
	// sandbox grants. A cross-device rename fails before touching the folder.
	staging, err := os.MkdirTemp(filepath.Dir(root), ".workspace-delete-")
	if err != nil {
		return false, fmt.Errorf("prepare folder deletion: %w", err)
	}
	if err := os.Rename(target, filepath.Join(staging, "folder")); err != nil {
		_ = os.Remove(staging)
		return false, fmt.Errorf("move folder for deletion: %w", err)
	}
	if err := os.RemoveAll(staging); err != nil {
		log.Printf("[FOLDER_DELETE] Folder removed; administrator cleanup pending at %s: %v", staging, err)
		return true, nil
	}
	return false, nil
}
