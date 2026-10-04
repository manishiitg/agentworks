package knowledgebase

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

type Identity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}
type Service struct {
	cfg           Config
	db            *sql.DB
	live, private string
}
type folderRegistry struct {
	ID        string     `json:"id"`
	Path      string     `json:"path"`
	Entries   []Entry    `json:"entries"`
	Deletions []Deletion `json:"deletions"`
}
type Entry struct {
	ID              string   `json:"entry_id"`
	FolderID        string   `json:"folder_id"`
	FolderPath      string   `json:"folder_path"`
	Path            string   `json:"path"`
	Filename        string   `json:"filename"`
	Type            string   `json:"type"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
	Sequence        uint64   `json:"sequence"`
	ContentSequence uint64   `json:"content_sequence"`
	Fingerprint     string   `json:"fingerprint"`
	Version         string   `json:"version"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	CreatedBy       string   `json:"created_by"`
	UpdatedBy       string   `json:"updated_by"`
}
type Deletion struct {
	ID         string `json:"deletion_id"`
	EntryID    string `json:"entry_id"`
	FolderID   string `json:"folder_id"`
	FolderPath string `json:"folder_path"`
	Path       string `json:"path"`
	Sequence   uint64 `json:"sequence"`
	Actor      string `json:"actor"`
	CreatedAt  string `json:"created_at"`
}
type Activity struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	Actor      string `json:"actor"`
	FolderPath string `json:"folder_path"`
	Path       string `json:"path,omitempty"`
	EntryID    string `json:"entry_id,omitempty"`
	At         string `json:"at"`
}
type accessChange struct {
	IdentityID         string `json:"identity_id,omitempty"`
	FolderPath         string `json:"folder_path,omitempty"`
	Role               int    `json:"role,omitempty"`
	Revoke             bool   `json:"revoke,omitempty"`
	FolderID           string `json:"folder_id,omitempty"`
	ACLSequence        int    `json:"acl_sequence,omitempty"`
	SecurityGeneration int    `json:"security_generation"`
	OnlyGeneration     bool   `json:"only_generation,omitempty"`
}

type fileChange struct {
	Access *accessChange `json:"access,omitempty"`
	Path   string        `json:"path"`
	Data   []byte        `json:"data,omitempty"`
	Delete bool          `json:"delete,omitempty"`
}
type journal struct {
	Changes []fileChange `json:"changes"`
}
type requestRecord struct {
	Hash   string `json:"hash"`
	At     string `json:"at"`
	Result any    `json:"result"`
}

func New(cfg Config) (*Service, error) {
	if cfg.Root == "" {
		return nil, errors.New("knowledgebase root is required")
	}
	if cfg.OrganizationID == "" {
		cfg.OrganizationID = "default"
	}
	if cfg.BackupBranch == "" {
		cfg.BackupBranch = "main"
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	cfg.Root = root
	s := &Service{cfg: cfg, live: filepath.Join(root, "live"), private: filepath.Join(root, "private")}
	for _, p := range []string{root, s.live, s.private, filepath.Join(s.private, "requests"), filepath.Join(s.private, "activity"), filepath.Join(s.private, "receipts")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return nil, err
		}
		if err := safeRegularDir(p); err != nil {
			return nil, err
		}
		if err := os.Chmod(p, 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(s.private, "access.sqlite"))
	if err != nil {
		return nil, err
	}
	s.db = db
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; CREATE TABLE IF NOT EXISTS grants(identity_id TEXT NOT NULL,folder_path TEXT NOT NULL,role INTEGER NOT NULL,PRIMARY KEY(identity_id,folder_path)); CREATE TABLE IF NOT EXISTS security(key TEXT PRIMARY KEY,value INTEGER NOT NULL); INSERT OR IGNORE INTO security VALUES('generation',1);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	os.Chmod(filepath.Join(s.private, "access.sqlite"), 0600)
	unlock, err := s.lock(context.Background(), false)
	if err != nil {
		db.Close()
		return nil, err
	}
	defer unlock()
	if err = s.recover(); err != nil {
		db.Close()
		return nil, err
	}
	bindPath := filepath.Join(s.private, "organization.json")
	if b, e := os.ReadFile(bindPath); e == nil {
		var bind map[string]string
		if json.Unmarshal(b, &bind) != nil || bind["organization_id"] != cfg.OrganizationID {
			db.Close()
			return nil, errors.New("knowledgebase root belongs to another organization")
		}
	} else if !os.IsNotExist(e) {
		db.Close()
		return nil, e
	} else if e = atomicJSON(bindPath, map[string]string{"organization_id": cfg.OrganizationID}); e != nil {
		db.Close()
		return nil, e
	}
	if _, err = os.Stat(s.registryPath("")); os.IsNotExist(err) {
		r := folderRegistry{ID: "folder_" + uuid.NewString(), Entries: []Entry{}, Deletions: []Deletion{}}
		if err = atomicJSON(s.registryPath(""), r); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}
func (s *Service) Close() error { return s.db.Close() }
func safeRegularDir(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("knowledgebase directory must not be a symlink")
	}
	return nil
}
func (s *Service) lock(ctx context.Context, try bool) (func(), error) {
	f, err := os.OpenFile(filepath.Join(s.private, "content.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			f.Close()
			return nil, err
		}
		if try {
			f.Close()
			return nil, retryErr("REQUEST_IN_PROGRESS", "An operation is in progress; retry shortly.", 1)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func retryErr(code, msg string, n int) *Error {
	return &Error{Code: code, Message: msg, Retryable: true, RetryAfter: &n}
}
func atomicJSON(p string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWrite(p, b)
}
func atomicWrite(p string, b []byte) error {
	if err := durableMkdir(filepath.Dir(p)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".kb-tmp-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, p); err != nil {
		return err
	}
	return syncDir(filepath.Dir(p))
}

// Persist new directory entries in their parents before acknowledging a journal.
func durableMkdir(p string) error {
	if fi, e := os.Lstat(p); e == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe internal directory")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	parent := filepath.Dir(p)
	if err := durableMkdir(parent); err != nil {
		return err
	}
	if err := os.Mkdir(p, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	return syncDir(parent)
}
func syncDir(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Service) apply(j journal) error {
	for _, c := range j.Changes {
		if c.Access != nil {
			if e := s.applyAccess(*c.Access); e != nil {
				return e
			}
			continue
		}
		if !strings.HasPrefix(c.Path, s.cfg.Root+string(filepath.Separator)) {
			return errors.New("invalid internal journal path")
		}
		if c.Delete {
			if err := os.Remove(c.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := syncDir(filepath.Dir(c.Path)); err != nil {
				return err
			}
		} else if err := atomicWrite(c.Path, c.Data); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) recover() error {
	p := filepath.Join(s.private, "mutation-journal.json")
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j journal
	if err = json.Unmarshal(b, &j); err != nil {
		return err
	}
	if err = s.apply(j); err != nil {
		return err
	}
	if err = os.Remove(p); err != nil {
		return err
	}
	return syncDir(s.private)
}
func (s *Service) transact(changes []fileChange) error {
	p := filepath.Join(s.private, "mutation-journal.json")
	j := journal{Changes: changes}
	if err := atomicJSON(p, j); err != nil {
		return err
	}
	return s.recover()
}
func jsonChange(p string, v any) fileChange {
	b, _ := json.Marshal(v)
	return fileChange{Path: p, Data: b}
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func stamp() string          { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *Service) registryPath(folder string) string {
	return filepath.Join(s.live, filepath.FromSlash(folder), ".kb-registry.json")
}
func (s *Service) readRegistry(folder string) (folderRegistry, error) {
	var r folderRegistry
	b, err := os.ReadFile(s.registryPath(folder))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
func (s *Service) registries() ([]folderRegistry, error) {
	var out []folderRegistry
	err := filepath.WalkDir(s.live, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected symbolic link in knowledgebase")
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != ".kb-registry.json" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		var r folderRegistry
		if e = json.Unmarshal(b, &r); e != nil {
			return e
		}
		out = append(out, r)
		return nil
	})
	return out, err
}
func (s *Service) Call(ctx context.Context, p Principal, tool string, args map[string]any) (any, error) {
	if args == nil {
		args = map[string]any{}
	}
	known := false
	mutates := false
	for _, d := range ToolDefinitions() {
		if d.Name == tool {
			known = true
			mutates = d.Mutates
			break
		}
	}
	if !known {
		return nil, badArg("Unknown knowledge-base tool.")
	}
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			return nil, e
		}
	}
	if p.IdentityID == "" {
		return nil, kbErr("UNAUTHENTICATED", "Authentication is required.")
	}
	if p.IsAdmin && !strings.HasPrefix(p.IdentityID, "service_") && !s.identityKnown(p.IdentityID) {
		if err := s.registerAdministrator(ctx, p.IdentityID); err != nil {
			return nil, err
		}
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		return nil, kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	if p.AccessOnly && tool != "manage_knowledgebase_access" && tool != "get_knowledgebase_access" && tool != "list_knowledgebase_folders" {
		return nil, kbErr("FORBIDDEN", "This connection is limited to access management.")
	}
	if tool == "manage_knowledgebase_access" && p.Caps != nil {
		return nil, kbErr("FORBIDDEN", "Content connections cannot manage access.")
	}
	if tool == "manage_knowledgebase_access" && stringArg(args, "action") == "list" {
		mutates = false
	}
	if tool == "commit_knowledgebase" || tool == "push_knowledgebase" {
		return s.backupCall(ctx, p, tool, args)
	}
	if tool == "manage_knowledgebase_access" && mutates {
		unlockSecurity, e := s.publicationLock()
		if e != nil {
			return nil, e
		}
		defer unlockSecurity()
	}
	unlock, err := s.lock(ctx, mutates)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = s.recover(); err != nil {
		return nil, err
	}
	if !s.IdentityActive(ctx, p.IdentityID) {
		return nil, kbErr("FORBIDDEN", "This identity is disabled or unavailable.")
	}
	if p.Recheck != nil {
		if e := p.Recheck(ctx); e != nil {
			return nil, e
		}
	}
	var reqPath, hash string
	if mutates {
		reqPath, hash, err = s.requestPath(p, tool, args)
		if err != nil {
			return nil, err
		}
		if cached, ok, e := s.cachedRequest(p, tool, args, reqPath, hash); e != nil || ok {
			return cached, e
		}
	}
	result, changes, activity, err := s.execute(ctx, p, tool, args)
	if err != nil {
		return nil, err
	}
	if mutates {
		changes = append(changes, jsonChange(reqPath, requestRecord{Hash: hash, At: stamp(), Result: result}))
		if activity != nil {
			changes = append(changes, jsonChange(filepath.Join(s.private, "activity", activity.ID+".json"), activity))
		}
		if err = s.transact(changes); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *Service) requestPath(p Principal, tool string, args map[string]any) (string, string, error) {
	id, ok := args["request_id"].(string)
	if !ok || len(id) < 1 || len(id) > 128 {
		return "", "", badArg("request_id must contain 1–128 ASCII letters, digits, underscores, or hyphens.")
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return "", "", badArg("Invalid request_id.")
		}
	}
	b, _ := json.Marshal(args)
	key := digest([]byte(s.cfg.OrganizationID + "\x00" + p.IdentityID + "\x00" + tool + "\x00" + id))
	return filepath.Join(s.private, "requests", key+".json"), digest(b), nil
}
func (s *Service) cachedRequest(p Principal, tool string, args map[string]any, path, hash string) (any, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var r requestRecord
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, false, err
	}
	at, _ := time.Parse(time.RFC3339Nano, r.At)
	if time.Since(at) > 7*24*time.Hour {
		return nil, false, nil
	}
	if r.Hash != hash {
		return nil, false, kbErr("REQUEST_ID_REUSE", "This request ID was used with different arguments.")
	}
	if err = s.authorizeRetry(p, tool, args, r.Result); err != nil {
		return nil, false, err
	}
	return r.Result, true, nil
}
func (s *Service) authorizeRetry(p Principal, tool string, args map[string]any, result any) error {
	switch tool {
	case "create_knowledgebase", "create_knowledgebase_folder":
		_, e := s.resolveFolder(p, args, roleEditor)
		return e
	case "update_knowledgebase":
		_, _, e := s.resolveEntry(p, args, roleEditor)
		return e
	case "delete_knowledgebase":
		m := asMap(result)
		folder := stringArg(m, "folder_path")
		return s.require(p, folder, roleEditor)
	case "manage_knowledgebase_access":
		return s.require(p, stringArg(args, "folder_path"), roleOwner)
	}
	return nil
}
func stringArg(a map[string]any, k string) string { v, _ := a[k].(string); return v }

// A durable journal precedes the grant transaction. Replay uses fixed values,
// so a crash after SQLite commit but before writing its request outcome is safe.
func (s *Service) applyAccess(change accessChange) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if !change.OnlyGeneration {
		current := 1
		if e = tx.QueryRow(`SELECT value FROM security WHERE key=?`, "acl:"+change.FolderID).Scan(&current); e != nil && e != sql.ErrNoRows {
			return e
		}
		if current > change.ACLSequence {
			return errors.New("journal ACL sequence regressed")
		}
		if change.Revoke {
			_, e = tx.Exec(`DELETE FROM grants WHERE identity_id=? AND folder_path=?`, change.IdentityID, change.FolderPath)
		} else {
			_, e = tx.Exec(`INSERT INTO grants(identity_id,folder_path,role) VALUES(?,?,?) ON CONFLICT(identity_id,folder_path) DO UPDATE SET role=excluded.role`, change.IdentityID, change.FolderPath, change.Role)
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO security(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "acl:"+change.FolderID, change.ACLSequence); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(`UPDATE security SET value=MAX(value,?) WHERE key='generation'`, change.SecurityGeneration); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) nextSecurityGeneration() int {
	var generation int
	s.db.QueryRow(`SELECT value FROM security WHERE key='generation'`).Scan(&generation)
	return generation + 1
}
