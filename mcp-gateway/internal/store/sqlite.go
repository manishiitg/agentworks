package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/access"
	_ "modernc.org/sqlite"
)

// Configuration snapshots keep related records and policy tombstones in one
// SQLite transaction. High-volume call audits remain separate.
type durableState struct {
	SecretResources map[string]SecretResource
	SecretGrants    map[string]map[string]bool
	Format          int
	Workspaces      map[string]Workspace
	Users           map[string]User
	Groups          map[string]Group
	Members         map[string]map[string]bool
	Connectors      map[string]Connector
	Bearers         map[string]string
	Tools           map[string]ToolSnapshot
	ToolVersions    map[string][]ToolSnapshot
	Grants          map[string]map[string]bool
	GroupGrants     map[string]map[string]bool
	GroupServers    map[string]map[string]bool
	Drafts          map[string]access.Package
	Live            map[string]access.Package
	Governed        map[string]map[string]bool
	History         map[string][]PolicyEvent
	Keys            map[string]APIKey
}
type sqlitePersistence struct {
	db           *sql.DB
	leaseDB      *sql.DB
	lease        *sql.Conn
	cipher       cipher.AEAD
	revision     int64
	sqlWorkspace string
	saved        []byte
	err          error
}

func (s *MemoryStore) durableState() durableState {
	return durableState{s.secretResources, s.secretGrants, 1, s.workspaces, s.users, s.groups, s.members, s.connectors, s.connectorBearer, s.tools, s.toolVersions, s.grants, s.groupGrants, s.groupServers, s.packageDrafts, s.packageLive, s.governedTools, s.policyEvents, s.apiKeys}
}
func (s *MemoryStore) restore(data []byte) error {
	var state durableState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Format != 1 || state.Workspaces == nil || state.Users == nil || state.Groups == nil || state.Members == nil || state.Connectors == nil || state.Bearers == nil || state.Tools == nil || state.ToolVersions == nil || state.Grants == nil || state.GroupGrants == nil || state.GroupServers == nil || state.Drafts == nil || state.Live == nil || state.Governed == nil || state.History == nil || state.Keys == nil {
		return errors.New("unsupported or incomplete gateway configuration")
	}
	if state.SecretResources == nil {
		state.SecretResources = map[string]SecretResource{}
	}
	if state.SecretGrants == nil {
		state.SecretGrants = map[string]map[string]bool{}
	}
	s.secretResources, s.secretGrants = state.SecretResources, state.SecretGrants
	s.workspaces, s.users, s.groups, s.members, s.connectors, s.connectorBearer = state.Workspaces, state.Users, state.Groups, state.Members, state.Connectors, state.Bearers
	s.tools, s.toolVersions, s.grants, s.groupGrants, s.groupServers = state.Tools, state.ToolVersions, state.Grants, state.GroupGrants, state.GroupServers
	s.packageDrafts, s.packageLive, s.governedTools, s.policyEvents, s.apiKeys = state.Drafts, state.Live, state.Governed, state.History, state.Keys
	// Compiled regexes are process-local; prepare them again after decoding.
	for id, p := range s.packageDrafts {
		s.packageDrafts[id] = access.Clone(p)
	}
	for id, p := range s.packageLive {
		s.packageLive[id] = access.Clone(p)
	}
	return nil
}

// NewSQLiteStore loads committed configuration; unreadable state fails startup
// instead of resetting permissions. Keep the database and its private key backed up.
func NewSQLiteStore(path string) (*MemoryStore, error) {
	return NewSQLiteStoreWithKey(path, path+".key")
}

// Keep the key in backend state when the encrypted database lives in a chat workspace.
func NewSQLiteStoreWithKey(path, keyPath string) (*MemoryStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	leaseDB, lease, err := acquireStoreLease(path + ".lock")
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			lease.Close()
			leaseDB.Close()
		}
	}()
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 {
			return nil, errors.New("gateway configuration key is missing")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		file, createErr := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			return nil, createErr
		}
		_, err = file.Write(key)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid gateway configuration key")
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Precreate with private permissions before SQLite writes any data.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*MemoryStore, error) { db.Close(); return nil, err }
	for _, q := range []string{"PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "CREATE TABLE IF NOT EXISTS gateway_configuration (id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, payload BLOB NOT NULL)"} {
		if _, err = db.Exec(q); err != nil {
			return fail(err)
		}
	}
	s := NewMemoryStore()
	p := &sqlitePersistence{db: db, cipher: aead, leaseDB: leaseDB, lease: lease}
	s.persistence = p
	var sealed []byte
	err = db.QueryRow("SELECT revision,payload FROM gateway_configuration WHERE id=1").Scan(&p.revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		p.saved, _ = json.Marshal(s.durableState())
		sealed, err = p.seal(p.saved)
		if err != nil {
			return fail(err)
		}
		if _, err = db.Exec("INSERT INTO gateway_configuration(id,revision,payload) VALUES(1,0,?)", sealed); err != nil {
			return fail(err)
		}
	} else if err != nil {
		return fail(err)
	} else {
		if len(sealed) < aead.NonceSize() {
			return fail(errors.New("invalid gateway configuration payload"))
		}
		p.saved, err = aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("caplayer-configuration-v1"))
		if err != nil {
			return fail(errors.New("cannot decrypt gateway configuration"))
		}
		if err = s.restore(p.saved); err != nil {
			return fail(err)
		}
	}
	opened = true
	return s, nil
}
func (p *sqlitePersistence) seal(data []byte) ([]byte, error) {
	nonce := make([]byte, p.cipher.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return p.cipher.Seal(nonce, nonce, data, []byte("caplayer-configuration-v1")), nil
}

// Called with the write lock held. Readers cannot observe a mutation until
// its commit succeeds; failed commits roll back memory and latch a denial.
func (s *MemoryStore) persistUnlock() {
	defer s.mu.Unlock()
	p := s.persistence
	if p == nil {
		return
	}
	if p.err != nil {
		_ = s.restore(p.saved)
		return
	}
	data, err := json.Marshal(s.durableState())
	if err == nil && bytes.Equal(data, p.saved) {
		return
	}
	if err == nil {
		err = p.commitState(context.Background(), data, s.durableState())
	}
	if err != nil {
		p.err = fmt.Errorf("gateway configuration persistence failed: %w", err)
		_ = s.restore(p.saved)
		return
	}
}
func (s *MemoryStore) PersistenceError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.persistence
	if p == nil {
		return nil
	}
	if p.err == nil && p.sqlWorkspace != "" {
		var revision int64
		if err := p.db.QueryRow("SELECT revision FROM gateway_configuration WHERE id=1").Scan(&revision); err != nil {
			p.err = err
		} else if revision != p.revision {
			p.err = errors.New("configuration changed outside gateway storage owner; restart required")
		}
	}
	return p.err
}

func (s *MemoryStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persistence == nil {
		return nil
	}
	p := s.persistence
	err := p.db.Close()
	_, _ = p.lease.ExecContext(context.Background(), "ROLLBACK")
	_ = p.lease.Close()
	_ = p.leaseDB.Close()
	return err
}

// A separate rollback-journal database holds a process-lifetime exclusive lock.
// Configuration commits can proceed normally while other gateway instances are
// rejected before they can serve stale permission snapshots.
func acquireStoreLease(path string) (*sql.DB, *sql.Conn, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	_ = f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	for _, q := range []string{"PRAGMA busy_timeout=0", "PRAGMA journal_mode=DELETE", "BEGIN EXCLUSIVE"} {
		if _, err = conn.ExecContext(context.Background(), q); err != nil {
			conn.Close()
			db.Close()
			return nil, nil, fmt.Errorf("gateway configuration already open or lock unavailable: %w", err)
		}
	}
	return db, conn, nil
}

// Move configuration using SQLite itself, including committed WAL contents.
// An active old instance must release its lease before relocation is allowed.
func MigrateSQLiteConfiguration(from, to, keyPath string) error {
	if from == to {
		return nil
	}
	if _, err := os.Stat(to); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	old, err := NewSQLiteStoreWithKey(from, keyPath)
	if err != nil {
		return err
	}
	defer old.Close()
	if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	leaseDB, lease, err := acquireStoreLease(to + ".lock")
	if err != nil {
		return err
	}
	defer leaseDB.Close()
	defer lease.Close()
	file, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	_ = file.Close()
	if _, err = old.persistence.db.Exec("VACUUM INTO ?", to); err != nil {
		_ = os.Remove(to)
		return err
	}
	return nil
}
