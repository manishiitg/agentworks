package server

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

func normalizeWorkflowWorkspacePath(workspacePath string) (string, error) {
	cleaned := filepath.ToSlash(filepath.Clean(strings.Trim(strings.TrimSpace(workspacePath), "/")))
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("workspace_path is required")
	}
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, "\x00") {
		return "", fmt.Errorf("workspace_path must be workspace-relative")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", fmt.Errorf("workspace_path cannot contain ..")
		}
	}
	return cleaned, nil
}

// openWorkflowStoreDB opens a workflow's or Crew's db/db.sqlite. A Crew is
// addressed several ways; its store lives once, at its physical root.
func openWorkflowStoreDB(ctx context.Context, workspacePath string, create bool) (string, *sql.DB, error) {
	if ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), workspacePath); ok {
		workspacePath = ref.Root
	}
	normalized, err := normalizeWorkflowWorkspacePath(workspacePath)
	if err != nil {
		return "", nil, err
	}
	root, err := filepath.Abs(getWorkspaceDocsAbsPath())
	if err != nil {
		return "", nil, err
	}
	dbPath, err := fsutil.WorkspacePath(root, normalized+"/db/db.sqlite")
	if err != nil {
		return "", nil, err
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return "", nil, err
		}
	} else if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return normalized, nil, nil
		}
		return "", nil, err
	}
	// WAL pragmas live in the DSN so every pooled connection gets them.
	db, err := sql.Open("sqlite", sqliteopen.DSN(dbPath))
	if err != nil {
		return "", nil, err
	}
	return normalized, db, nil
}
