package step_based_workflow

import (
	"context"
	"database/sql"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
)

type workflowEvidenceDB interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func workflowEvidenceDBPath(workspacePath string) string {
	return filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/")), "db", "db.sqlite")
}
func openWorkflowEvidenceDB(ctx context.Context, workspacePath string, create bool) (*sql.DB, error) {
	_ = ctx // busy_timeout is now DSN-embedded (see sqliteopen.DSN), not a runtime PRAGMA on ctx
	dbPath := workflowEvidenceDBPath(workspacePath)
	if create {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(dbPath))
	if err != nil {
		return nil, err
	}

	return db, nil
}
func normalizedEvidenceStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out
}
