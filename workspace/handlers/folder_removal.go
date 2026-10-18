package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Move a deleted folder out of the served tree before recursive cleanup. A
// sandbox may own private directories the service cannot traverse; RemoveAll
// on the live path would remove the manifest before failing on those files.
func removeFolderAtomically(docsDir, folderPath string) (bool, error) {
	return removeFolderAtomicallyWithContext(context.Background(), docsDir, folderPath)
}

func removeFolderAtomicallyWithContext(ctx context.Context, docsDir, folderPath string) (bool, error) {
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
	releases, isRelay, err := relayDeletionReleases(root, target)
	if err != nil {
		return false, err
	}
	// A slot cannot enter service-owned deletion staging. Remove its private
	// Relay journals through the existing sandbox/slotctl path while reachable.
	privateCleanupPending, err := cleanupRelaySlotRunFiles(ctx, root, target)
	if err != nil {
		return false, fmt.Errorf("clean Relay run files before deletion: %w", err)
	}
	// This fresh service-owned 0700 directory is outside the documents API and
	// sandbox grants. A cross-device rename fails before touching the folder.
	staging, err := os.MkdirTemp(filepath.Dir(root), ".workspace-delete-")
	if err != nil {
		return false, fmt.Errorf("prepare folder deletion: %w", err)
	}
	stagedReleases := filepath.Join(staging, "releases")
	if releases != "" {
		if err := os.Rename(releases, stagedReleases); err != nil {
			_ = os.Remove(staging)
			return false, fmt.Errorf("move Flow releases for deletion: %w", err)
		}
	}
	if err := os.Rename(target, filepath.Join(staging, "folder")); err != nil {
		if releases != "" {
			if rollbackErr := os.Rename(stagedReleases, releases); rollbackErr != nil {
				log.Printf("[FOLDER_DELETE] cleanup_pending=true rollback failed; draft=%s releases=%s staged_releases=%s: %v", target, releases, stagedReleases, rollbackErr)
				return true, fmt.Errorf("move folder for deletion: %v; restore releases from %s to %s: %w", err, stagedReleases, releases, rollbackErr)
			}
		}
		_ = os.Remove(staging)
		return false, fmt.Errorf("move folder for deletion: %w", err)
	}
	if isRelay {
		log.Printf("[RELAY_DELETE] Draft and published namespace staged: draft=%s releases=%s staging=%s", target, releases, staging)
	}
	if privateCleanupPending {
		// An owner account no longer exists. Keep the journals reachable only
		// through private staging and give the administrator its exact path.
		log.Printf("[FOLDER_DELETE] cleanup_pending=true; private journal owner unavailable; draft=%s releases=%s staging=%s", target, releases, staging)
		return true, nil
	}
	if err := os.RemoveAll(staging); err != nil {
		log.Printf("[FOLDER_DELETE] Folder removed; administrator cleanup pending at %s: %v", staging, err)
		return true, nil
	}
	if isRelay {
		log.Printf("[RELAY_DELETE] Cleanup complete: draft=%s releases=%s cleanup_pending=false", target, releases)
	}
	return false, nil
}
