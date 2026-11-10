package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

var conversationRegistryConnections = struct {
	sync.Mutex
	byFile map[string]*sql.DB
}{byFile: map[string]*sql.DB{}}

// activateConversationRegistryRows is NOT called by the live store yet. The
// eventual activation must route every reader AND writer through rows together.
// Holding the old registry writer mutex across import prevents an older writer
// in this process from landing a JSON change between import and activation.
// Never call this as a preliminary import while JSON writers remain enabled.
func activateConversationRegistryRows(ctx context.Context, userID string) (conversationRegistryRows, error) {
	logical := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(logical)
	mutex.Lock()
	defer mutex.Unlock()
	docs, err := sqliteopen.CanonicalPath(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return conversationRegistryRows{}, err
	}
	source, err := fsutil.WorkspacePath(docs, logical)
	if err != nil {
		return conversationRegistryRows{}, err
	}
	parent, err := sqliteopen.CanonicalPath(filepath.Dir(source))
	if err != nil {
		return conversationRegistryRows{}, err
	}
	source = filepath.Join(parent, filepath.Base(source))
	dir, err := sqliteopen.StateDirectory(docs, "conversation-registry")
	if err != nil {
		return conversationRegistryRows{}, err
	}
	db, err := openConversationRegistryDatabase(ctx, dir)
	if err != nil {
		return conversationRegistryRows{}, err
	}
	rows := conversationRegistryRows{db: db, namespace: source}
	if err = rows.importFile(ctx, source, filepath.Join(dir, "imports", sqliteopen.PathHash(source))); err != nil {
		return conversationRegistryRows{}, err
	}
	return rows, nil
}

func openConversationRegistryDatabase(ctx context.Context, dir string) (*sql.DB, error) {
	file := filepath.Join(dir, "registry.sqlite")
	conversationRegistryConnections.Lock()
	defer conversationRegistryConnections.Unlock()
	if db := conversationRegistryConnections.byFile[file]; db != nil {
		return db, nil
	}
	if err := agentworksclient.EnsurePrivateDirectory(dir); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	if info, e := os.Lstat(file); e == nil && !info.Mode().IsRegular() {
		return nil, errors.New("chat bindings must use an ordinary private database")
	} else if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(file)+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err = initializeConversationRegistryRows(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	conversationRegistryConnections.byFile[file] = db
	return db, nil
}

func (r conversationRegistryRows) importFile(ctx context.Context, source, importDir string) error {
	var pending int
	err := r.db.QueryRowContext(ctx, `SELECT cleanup_pending FROM registry_imports WHERE namespace=?`, r.namespace).Scan(&pending)
	if err == nil && pending == 0 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = agentworksclient.EnsurePrivateDirectory(importDir); err != nil {
		return err
	}
	if err = os.Chmod(importDir, 0700); err != nil {
		return err
	}
	unlock, err := wf.LockWorkspace(ctx, importDir)
	if err != nil {
		return err
	}
	defer unlock()
	// Take the SQL writer before source IO; deletion/other row writers cannot
	// interleave the first read and the committed namespace import.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT cleanup_pending FROM registry_imports WHERE namespace=?`, r.namespace).Scan(&pending)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM registry_entries WHERE namespace=?) OR EXISTS(SELECT 1 FROM registry_previous WHERE namespace=?) OR EXISTS(SELECT 1 FROM registry_agents WHERE namespace=?)`, r.namespace, r.namespace, r.namespace).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return errors.New("saved chat bindings need administrator repair before import; keep the original registry file")
		}
		var raw []byte
		info, e := os.Lstat(source)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil {
			if !info.Mode().IsRegular() {
				return errors.New("the saved chat registry must be an ordinary file")
			}
			raw, e = os.ReadFile(source)
			if e != nil {
				return e
			}
			pending = 1
		}
		document, e := decodeConversationRegistrySnapshot(string(raw))
		if e != nil {
			return fmt.Errorf("saved chat bindings could not be imported; keep the original registry file: %w", e)
		}
		if err = r.writeSnapshot(ctx, tx, document, string(raw)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO registry_imports(namespace,cleanup_pending) VALUES (?,?)`, r.namespace, pending); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		log.Printf("[CONVERSATION_REGISTRY] namespace=%s event=import current=%d previous_slots=%d identities=%d source_bytes=%d", sqliteopen.PathHash(r.namespace), len(document.Entries), len(document.Previous), len(document.Agents), len(raw))
	} else {
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	if pending != 0 {
		backup := filepath.Join(importDir, productConversationRegistryFile+".imported")
		if err = sqliteopen.RetireImportedJSON(source, backup); err != nil {
			if pending > 0 || errors.Is(err, sqliteopen.ErrImportSourceChanged) {
				next := -1
				if errors.Is(err, sqliteopen.ErrImportSourceChanged) {
					next = 0
				}
				if _, markErr := r.db.ExecContext(ctx, `UPDATE registry_imports SET cleanup_pending=? WHERE namespace=?`, next, r.namespace); markErr != nil {
					return markErr
				}
				log.Printf("[CONVERSATION_REGISTRY] namespace=%s event=source_retirement_failed bindings=sqlite error=%v", sqliteopen.PathHash(r.namespace), err)
			}
			return nil
		}
		if _, err = r.db.ExecContext(ctx, `UPDATE registry_imports SET cleanup_pending=0 WHERE namespace=?`, r.namespace); err != nil {
			return err
		}
		log.Printf("[CONVERSATION_REGISTRY] namespace=%s event=source_retired", sqliteopen.PathHash(r.namespace))
	}
	return nil
}
