package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"

	_ "modernc.org/sqlite"
)

// createReadonlyDBSnapshot creates a standalone SQLite image containing all
// committed rows visible through the source database, including rows currently
// resident in its WAL. The destination lives under this step's writable output
// folder, so the sandbox never needs access to the source WAL/SHM sidecars.
func createReadonlyDBSnapshot(ctx context.Context, docsDir string, env map[string]string, guard *models.FolderGuardConfig) (string, func(), error) {
	source := filepath.Clean(strings.TrimSpace(env["DB_PATH"]))
	outputDir := filepath.Clean(strings.TrimSpace(env["STEP_OUTPUT_DIR"]))
	if source == "." || source == "" {
		return "", func() {}, fmt.Errorf("DB_PATH is required for a database snapshot")
	}
	if outputDir == "." || outputDir == "" {
		return "", func() {}, fmt.Errorf("STEP_OUTPUT_DIR is required for a database snapshot")
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(outputDir) {
		return "", func() {}, fmt.Errorf("DB_PATH and STEP_OUTPUT_DIR must be absolute workspace paths")
	}
	if !utils.IsValidFilePath(source, docsDir) || !utils.IsValidFilePath(outputDir, docsDir) {
		return "", func() {}, fmt.Errorf("database snapshot paths must remain inside the workspace")
	}
	if filepath.Base(source) != "db.sqlite" || filepath.Base(filepath.Dir(source)) != "db" {
		return "", func() {}, fmt.Errorf("DB_PATH must identify the workflow db/db.sqlite")
	}
	if guard == nil || !guard.Enabled || !pathCoveredByWriteGuard(outputDir, docsDir, guard.WritePaths) {
		return "", func() {}, fmt.Errorf("STEP_OUTPUT_DIR is not authorized by the shell write guard")
	}
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		return "", func() {}, fmt.Errorf("workflow database is unavailable at DB_PATH: %s", source)
	}

	snapshotDir := filepath.Join(outputDir, ".runtime")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return "", func() {}, fmt.Errorf("create database snapshot directory: %w", err)
	}
	placeholder, err := os.CreateTemp(snapshotDir, "db-read-snapshot-*.sqlite")
	if err != nil {
		return "", func() {}, fmt.Errorf("reserve database snapshot path: %w", err)
	}
	destination := placeholder.Name()
	if closeErr := placeholder.Close(); closeErr != nil {
		_ = os.Remove(destination)
		return "", func() {}, fmt.Errorf("close database snapshot placeholder: %w", closeErr)
	}
	// VACUUM INTO requires that the destination does not yet exist.
	if err := os.Remove(destination); err != nil {
		return "", func() {}, fmt.Errorf("prepare database snapshot path: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(destination)
		_ = os.Remove(destination + "-wal")
		_ = os.Remove(destination + "-shm")
	}

	dsn := (&url.URL{Scheme: "file", Path: source}).String() + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("open workflow database for snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("read workflow database for snapshot: %w", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("materialize workflow database snapshot: %w", err)
	}
	if err := os.Chmod(destination, 0o444); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("mark workflow database snapshot read-only: %w", err)
	}
	return destination, cleanup, nil
}

func pathCoveredByWriteGuard(candidate, docsDir string, writePaths []string) bool {
	candidate = filepath.Clean(candidate)
	for _, granted := range writePaths {
		granted = filepath.Clean(strings.TrimSpace(granted))
		if granted == "." || granted == "" {
			continue
		}
		if !filepath.IsAbs(granted) {
			if resolved, bound, err := crewroot.Resolve(docsDir, granted); err != nil {
				continue
			} else if bound {
				granted = resolved
			} else {
				granted = filepath.Join(docsDir, granted)
			}
		}
		rel, err := filepath.Rel(granted, candidate)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
