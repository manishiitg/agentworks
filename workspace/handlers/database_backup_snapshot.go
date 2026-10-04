package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
	"github.com/spf13/viper"
)

const workflowDatabaseBackupSnapshotRelativePath = "backup/database/db.sqlite"
const workflowDatabaseBackupChecksumRelativePath = "backup/database/db.sqlite.sha256"

// CreateWorkflowDatabaseBackupSnapshot creates the durable database image used
// by workflow backup destinations. It deliberately does not expose a caller-
// selected output path: the source must be <workflow>/db/db.sqlite and the
// destination is always <workflow>/backup/database/db.sqlite.
//
// VACUUM INTO reads through SQLite, so committed WAL rows are included without
// granting the agent shell direct access to the live DB/WAL/SHM files. The
// completed image is integrity-checked and atomically replaces the prior
// snapshot only after every check passes.
func CreateWorkflowDatabaseBackupSnapshot(c *gin.Context) {
	var req models.WorkflowDatabaseBackupSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	cleanRequest, normalizeErr := normalizeOwnedPerUserDBPath(c, req.DBPath)
	if normalizeErr != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: normalizeErr.Error()})
		return
	}
	cleanRequest = strings.TrimSpace(filepath.ToSlash(filepath.Clean(filepath.FromSlash(cleanRequest))))
	// A Crew's database is managed wherever the Crew lives: its owner's tree or the shared Crew/ root (PLAT-442).
	isManagedRoot := strings.HasPrefix(cleanRequest, "Workflow/") || strings.HasPrefix(cleanRequest, "Chats/Work/projects/") ||
		strings.HasPrefix(cleanRequest, workspaceref.SharedCrewRoot+"/")
	if filepath.IsAbs(filepath.FromSlash(cleanRequest)) || !isManagedRoot || !strings.HasSuffix(cleanRequest, "/db/db.sqlite") || strings.HasPrefix(cleanRequest, "../") {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: "managed databases must use <workspace>/db/db.sqlite"})
		return
	}

	docsDir := viper.GetString("docs-dir")
	sourcePath, err := resolveUserPath(c, cleanRequest)
	if err != nil || !utils.IsValidFilePath(sourcePath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid db_path", Error: "path escapes the workspace boundary"})
		return
	}
	if info, statErr := os.Stat(sourcePath); statErr != nil || info.IsDir() {
		if statErr == nil {
			statErr = fmt.Errorf("database path is a directory")
		}
		c.JSON(http.StatusNotFound, models.APIResponse[any]{Success: false, Message: "Workflow database not found", Error: statErr.Error()})
		return
	}

	workflowRoot := filepath.Dir(filepath.Dir(sourcePath))
	destinationPath := filepath.Join(workflowRoot, filepath.FromSlash(workflowDatabaseBackupSnapshotRelativePath))
	if !utils.IsValidFilePath(destinationPath, docsDir) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid snapshot path", Error: "snapshot path escapes the workspace boundary"})
		return
	}
	result, err := materializeWorkflowDatabaseBackupSnapshot(c.Request.Context(), sourcePath, destinationPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to create workflow database backup snapshot", Error: err.Error()})
		return
	}
	result.SourceDBPath = cleanRequest
	workflowRelativeSnapshot := filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(cleanRequest)), workflowDatabaseBackupSnapshotRelativePath))
	result.SnapshotPath = workflowRelativeSnapshot
	checksumPath := filepath.Join(workflowRoot, filepath.FromSlash(workflowDatabaseBackupChecksumRelativePath))
	if err := writeWorkflowDatabaseBackupChecksum(checksumPath, result.SHA256); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to write workflow database backup checksum", Error: err.Error()})
		return
	}
	result.ChecksumPath = filepath.ToSlash(filepath.Join(filepath.Dir(filepath.Dir(cleanRequest)), workflowDatabaseBackupChecksumRelativePath))
	c.JSON(http.StatusOK, models.APIResponse[models.WorkflowDatabaseBackupSnapshotResult]{Success: true, Message: "Workflow database backup snapshot created", Data: result})
}

func materializeWorkflowDatabaseBackupSnapshot(ctx context.Context, sourcePath, destinationPath string) (models.WorkflowDatabaseBackupSnapshotResult, error) {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("create backup snapshot directory: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(destinationPath), ".db-backup-*.sqlite")
	if err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("reserve backup snapshot path: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("close backup snapshot placeholder: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("prepare backup snapshot path: %w", err)
	}
	defer func() {
		_ = os.Remove(tempPath)
		_ = os.Remove(tempPath + "-wal")
		_ = os.Remove(tempPath + "-shm")
	}()

	dsn := (&url.URL{Scheme: "file", Path: sourcePath}).String() + "?mode=ro&_pragma=busy_timeout(5000)"
	source, err := sql.Open("sqlite", dsn)
	if err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("open workflow database: %w", err)
	}
	source.SetMaxOpenConns(1)
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("read workflow database: %w", err)
	}
	if _, err := source.ExecContext(ctx, "VACUUM INTO ?", tempPath); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("materialize SQLite backup image: %w", err)
	}

	snapshotDSN := (&url.URL{Scheme: "file", Path: tempPath}).String() + "?mode=ro&_pragma=query_only(true)&_pragma=busy_timeout(5000)"
	snapshot, err := sql.Open("sqlite", snapshotDSN)
	if err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("open completed snapshot: %w", err)
	}
	snapshot.SetMaxOpenConns(1)
	var integrity string
	checkErr := snapshot.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	closeErr := snapshot.Close()
	if checkErr != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("integrity-check completed snapshot: %w", checkErr)
	}
	if closeErr != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("close completed snapshot: %w", closeErr)
	}
	if !strings.EqualFold(strings.TrimSpace(integrity), "ok") {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("completed snapshot failed integrity_check: %s", integrity)
	}

	file, err := os.Open(tempPath)
	if err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("open snapshot for hashing: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	closeErr = file.Close()
	if copyErr != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("hash completed snapshot: %w", copyErr)
	}
	if closeErr != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("close hashed snapshot: %w", closeErr)
	}
	if err := os.Chmod(tempPath, 0o444); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("mark completed snapshot read-only: %w", err)
	}
	if err := syncFile(tempPath); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("sync completed snapshot: %w", err)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("publish completed snapshot atomically: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destinationPath)); err != nil {
		return models.WorkflowDatabaseBackupSnapshotResult{}, fmt.Errorf("sync backup snapshot directory: %w", err)
	}

	return models.WorkflowDatabaseBackupSnapshotResult{
		SHA256:    hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes: size,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Integrity: "ok",
	}, nil
}

func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func writeWorkflowDatabaseBackupChecksum(destinationPath, hash string) error {
	tempFile, err := os.CreateTemp(filepath.Dir(destinationPath), ".db-backup-checksum-*")
	if err != nil {
		return fmt.Errorf("reserve checksum path: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := fmt.Fprintf(tempFile, "%s  db.sqlite\n", hash); err != nil {
		tempFile.Close()
		return fmt.Errorf("write checksum: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return fmt.Errorf("sync checksum: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close checksum: %w", err)
	}
	if err := os.Chmod(tempPath, 0o444); err != nil {
		return fmt.Errorf("mark checksum read-only: %w", err)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return fmt.Errorf("publish checksum atomically: %w", err)
	}
	if err := syncDirectory(filepath.Dir(destinationPath)); err != nil {
		return fmt.Errorf("sync checksum directory: %w", err)
	}
	return nil
}
