// Package schedulehistory owns the location and deletion boundary shared by
// the agent's run store and the workspace service's authorized folder removal.
package schedulehistory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// PhysicalPath also resolves ancestors of a missing path. Otherwise a new
// private-state folder could evade the docs boundary through a parent symlink.
func PhysicalPath(value string) (string, error) {
	full, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	parent := full
	suffix := []string{}
	for {
		real, err := filepath.EvalSymlinks(parent)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				real = filepath.Join(real, suffix[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if info, e := os.Lstat(parent); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = next
	}
}

func DatabasePath(docs string) (string, error) {
	docs, err := PhysicalPath(docs)
	if err != nil {
		return "", err
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	state, err = PhysicalPath(state)
	if err != nil {
		return "", err
	}
	if crewroot.Within(docs, state) {
		return "", errors.New("the app's private data folder must be outside the workspace documents")
	}
	return filepath.Join(state, "schedule-history", Hash(docs), "history.sqlite"), nil
}

// PurgeTree is called only after authorized folder removal, never rename or
// move. A newly created project at the same path must not inherit old responses.
// It opens no new database when history has never been initialized.
func PurgeTree(ctx context.Context, docs, physical string) error {
	docs, err := PhysicalPath(docs)
	if err != nil {
		return err
	}
	physical, err = PhysicalPath(physical)
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
	for _, table := range []string{"history_entries", "history_imports"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+predicate, physical, prefix); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Backups are never read as history. Even if private cleanup fails, the
	// committed row deletion prevents the new project's API reading old data.
	for _, scope := range scopes {
		if err = os.RemoveAll(filepath.Join(filepath.Dir(file), "imports", Hash(scope))); err != nil {
			return fmt.Errorf("history rows removed; private backup cleanup failed: %w", err)
		}
	}
	return nil
}
