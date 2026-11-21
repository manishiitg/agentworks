// Package schedulehistory owns the location and deletion boundary shared by
// the agent's run store and the workspace service's authorized folder removal.
package schedulehistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

func DatabasePath(docs string) (string, error) {
	dir, err := sqliteopen.StateDirectory(docs, "schedule-history")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.sqlite"), nil
}

// PurgeTree is called only after authorized folder removal, never rename or
// move. A newly created project at the same path must not inherit old responses.
// It opens no new database when history has never been initialized.
func PurgeTree(ctx context.Context, docs, physical string) error {
	docs, err := sqliteopen.CanonicalPath(docs)
	if err != nil {
		return err
	}
	physical, err = sqliteopen.CanonicalPath(physical)
	if err != nil {
		return err
	}
	if physical == docs {
		return errors.New("run history cleanup must identify one project folder")
	}
	// Local project files outside docs hold no service history scopes.
	if !crewroot.Within(docs, physical) {
		return nil
	}
	file, err := DatabasePath(docs)
	if err != nil {
		return err
	}
	if info, e := os.Lstat(file); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	} else if !info.Mode().IsRegular() {
		return errors.New("run history must be an ordinary private database")
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(file)+"&_txlock=immediate")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var initialized bool
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='history_entries')`).Scan(&initialized); err != nil {
		return err
	}
	if !initialized {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prefix := physical + string(filepath.Separator)
	predicate := `scope=? OR instr(scope,?)=1`
	rows, err := tx.QueryContext(ctx, `SELECT scope FROM history_imports WHERE `+predicate+` UNION SELECT scope FROM history_entries WHERE `+predicate, physical, prefix, physical, prefix)
	if err != nil {
		return err
	}
	scopes := []string{physical}
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var removed int64
	for _, table := range []string{"history_entries", "history_imports"} {
		result, deleteErr := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+predicate, physical, prefix)
		if deleteErr != nil {
			return deleteErr
		}
		if table == "history_entries" {
			removed, err = result.RowsAffected()
			if err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Backups are never read as history. Even if private cleanup fails, the
	// committed row deletion prevents the new project's API reading old data.
	for _, scope := range scopes {
		if err = os.RemoveAll(filepath.Join(filepath.Dir(file), "imports", sqliteopen.PathHash(scope))); err != nil {
			log.Printf("[SCHEDULE_HISTORY] scope=%s event=scope_deleted rows_removed=%d backup_cleanup=failed", sqliteopen.PathHash(physical), removed)
			return fmt.Errorf("history rows removed; private backup cleanup failed: %w", err)
		}
	}
	log.Printf("[SCHEDULE_HISTORY] scope=%s event=scope_deleted rows_removed=%d backup_cleanup=done", sqliteopen.PathHash(physical), removed)
	return nil
}
