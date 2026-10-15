package pulsestore

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
)

// MigrateWorkspaceDatabases performs the deployment-time pass. Lazy Ensure
// remains mandatory because another laptop may not have been online during it.
func MigrateWorkspaceDatabases(ctx context.Context, root string) (BatchResult, error) {
	var report BatchResult
	skippedFolders := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// The root itself must be readable. Any other folder the server cannot open (a build cache, a
			// sandbox home owned by another account) is not a workspace record: note it and keep scanning, so one
			// unreadable folder never stops the whole deployment-time pass.
			if path == root {
				return walkErr
			}
			skippedFolders++
			log.Printf("[PULSE MIGRATION] skipped %s: %v", path, walkErr)
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			name := entry.Name()
			// Frozen Flow releases (.relay_releases) run with their own workspace, so they are scanned like any other.
			if name == ".git" || name == "node_modules" || name == ".backups" || name == "backup" ||
				(path != root && strings.HasPrefix(name, ".") && name != ".relay_releases") {
				return filepath.SkipDir
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			first := relative
			if separator := len(relative); separator > 0 {
				if index := indexPathSeparator(relative); index >= 0 {
					first = relative[:index]
				}
			}
			// These roots are leaked absolute/temp paths created by old tests or
			// path-normalization bugs, not workspace records. Never mutate them in
			// a deployment migration.
			if first == "var" || first == "Users" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "db.sqlite" || filepath.Base(filepath.Dir(path)) != "db" {
			return nil
		}
		report.Scanned++
		result, err := MigratePath(ctx, path)
		if err != nil {
			report.Failures = append(report.Failures, BatchFailure{Path: path, Error: err.Error()})
			return nil
		}
		switch {
		case result.Applied:
			report.Migrated++
			report.Results = append(report.Results, result)
		case result.AlreadyCurrent:
			report.Current++
		default:
			report.Skipped++
		}
		return nil
	})
	log.Printf("[PULSE MIGRATION] done scanned=%d migrated=%d current=%d skipped=%d failed=%d unreadable_folders=%d",
		report.Scanned, report.Migrated, report.Current, report.Skipped, len(report.Failures), skippedFolders)
	return report, err
}

func indexPathSeparator(path string) int {
	for index, character := range path {
		if character == '/' || character == '\\' {
			return index
		}
	}
	return -1
}
