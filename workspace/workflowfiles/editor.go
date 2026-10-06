package workflowfiles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

type FileError struct {
	Status  int
	Message string
	Code    string
}

func (e *FileError) Error() string               { return e.Message }
func fileError(status int, message string) error { return &FileError{Status: status, Message: message} }

// EditIdentity is supplied by the authenticated transport, never file arguments.
type EditIdentity struct {
	UserID       string `json:"user_id,omitempty"`
	Username     string `json:"username,omitempty"`
	ConnectionID string `json:"connection_id,omitempty"`
	Source       string `json:"source,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
}
type WriteRequest struct {
	Identity         EditIdentity `json:"identity"`
	Root             string       `json:"root"`
	Path             string       `json:"path"`
	Content          string       `json:"content"`
	ExpectedRevision string       `json:"expected_revision"`
	RequestID        string       `json:"request_id"`
	Actor            string       `json:"actor"`
	Guard            *FolderGuard `json:"guard,omitempty"`
}
type WriteReceipt struct {
	Identity         EditIdentity `json:"identity"`
	RequestID        string       `json:"request_id"`
	Path             string       `json:"path"`
	PreviousRevision string       `json:"previous_revision"`
	Revision         string       `json:"revision"`
	Applied          bool         `json:"applied"`
}
type editRecord struct {
	Fingerprint string       `json:"fingerprint"`
	Actor       string       `json:"actor"`
	Root        string       `json:"root"`
	Before      string       `json:"before"`
	CreatedAt   time.Time    `json:"created_at"`
	Status      string       `json:"status"`
	Receipt     WriteReceipt `json:"receipt"`
}

// Editor confines files to an already-open root. State is private and must be
// outside that root. SQLite supplies a cross-process lock; durable JSON records
// are written before mutation, allowing interrupted requests to be reconciled.
type Editor struct {
	rootIdentity string
	root         *os.Root
	state        *os.Root
	db           *sql.DB
}

func OpenEditor(rootDir, stateDir string) (*Editor, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	rootDir, err = filepath.EvalSymlinks(rootDir)
	if err != nil {
		return nil, err
	}
	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return nil, err
	}
	// Resolve trusted OS aliases before checking ancestry.
	if err = os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(stateDir)
	if err != nil || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("editor state must not be a symbolic link")
	}
	stateDir, err = filepath.EvalSymlinks(stateDir)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(rootDir, stateDir)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return nil, fmt.Errorf("editor state must be outside exposed files")
	}
	if err = os.Chmod(stateDir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	state, err := os.OpenRoot(stateDir)
	if err != nil {
		root.Close()
		return nil, err
	}
	lockDir, err := DefaultStateDir(rootDir)
	if err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	lockRelative, err := filepath.Rel(rootDir, lockDir)
	if err != nil || lockRelative == "." || lockRelative != ".." && !strings.HasPrefix(lockRelative, ".."+string(os.PathSeparator)) {
		root.Close()
		state.Close()
		return nil, fmt.Errorf("editor lock state must be outside exposed files")
	}
	if err = os.MkdirAll(lockDir, 0700); err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	info, err := os.Lstat(lockDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		root.Close()
		state.Close()
		return nil, fmt.Errorf("unsafe editor lock directory")
	}
	lockDir, err = filepath.EvalSymlinks(lockDir)
	if err == nil {
		lockRelative, err = filepath.Rel(rootDir, lockDir)
	}
	if err != nil || lockRelative == "." || lockRelative != ".." && !strings.HasPrefix(lockRelative, ".."+string(os.PathSeparator)) {
		root.Close()
		state.Close()
		return nil, fmt.Errorf("editor lock state must be outside exposed files")
	}
	if err = os.Chmod(lockDir, 0700); err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	lockRoot, err := os.OpenRoot(lockDir)
	if err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	defer lockRoot.Close()
	for _, name := range []string{"lock.sqlite", "lock.sqlite-wal", "lock.sqlite-shm"} {
		if info, e := lockRoot.Lstat(name); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			root.Close()
			state.Close()
			return nil, fmt.Errorf("unsafe editor state")
		}
	}
	f, err := lockRoot.OpenFile("lock.sqlite", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", sqliteopen.DSN(filepath.Join(lockDir, "lock.sqlite")))
	if err != nil {
		root.Close()
		state.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS file_lock (id INTEGER PRIMARY KEY)`); err != nil {
		db.Close()
		root.Close()
		state.Close()
		return nil, err
	}
	return &Editor{rootIdentity: Revision([]byte(rootDir)), root: root, state: state, db: db}, nil
}

// OpenReadOnlyEditor needs no writable state or parent directory.
func OpenReadOnlyEditor(rootDir string) (*Editor, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	return &Editor{root: root}, nil
}
func (e *Editor) Close() error {
	if e.root != nil {
		e.root.Close()
	}
	if e.state != nil {
		e.state.Close()
	}
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// OpenDirectory rejects symlinks component by component and anchors each
// directory before proceeding, including links into private sibling folders.
func OpenDirectory(root *os.Root, p string, create bool) (*os.Root, error) {
	clean, err := CleanRelative(p)
	if err != nil {
		return nil, fileError(400, "invalid path")
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "." {
			continue
		}
		info, err := current.Lstat(part)
		if os.IsNotExist(err) && create {
			if err = current.Mkdir(part, 0755); err != nil && !os.IsExist(err) {
				current.Close()
				return nil, err
			}
			info, err = current.Lstat(part)
		}
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fileError(403, "symbolic links and non-directories are blocked")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		observed, err := next.Stat(".")
		current.Close()
		if err != nil || !os.SameFile(info, observed) {
			next.Close()
			return nil, fileError(403, "directory changed during access")
		}
		current = next
	}
	return current, nil
}
func readRegular(root *os.Root, name string) ([]byte, os.FileMode, error) {
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil, 0644, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fileError(403, "only regular files are accessible")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	observed, err := f.Stat()
	if err != nil || !os.SameFile(info, observed) {
		return nil, 0, fileError(403, "file changed during access")
	}
	if info.Size() > MaxFileBytes {
		return nil, 0, fileError(413, "file exceeds 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if len(data) > MaxFileBytes {
		return nil, 0, fileError(413, "file exceeds 2 MiB")
	}
	if data == nil {
		data = []byte{}
	}
	return data, info.Mode().Perm(), err
}
func revision(data []byte) string {
	if data == nil {
		return MissingRevision
	}
	return Revision(data)
}
func atomicRootWrite(root *os.Root, name string, data []byte, mode os.FileMode) error {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return err
	}
	temp := ".aw-write-" + hex.EncodeToString(entropy)
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(temp, name); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	} // Windows has no portable directory fsync.
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (e *Editor) Write(ctx context.Context, r WriteRequest) (WriteReceipt, error) {
	if e.db == nil {
		return WriteReceipt{}, fileError(403, "editor is read-only")
	}
	p, err := CleanRelative(r.Path)
	if err != nil || p == "." {
		return WriteReceipt{}, fileError(400, "invalid file path")
	}
	scope, err := CleanRelative(r.Root)
	if err != nil {
		return WriteReceipt{}, fileError(400, "invalid root")
	}
	if ProtectedWrite(p) || !r.Guard.Allows(p, true) {
		return WriteReceipt{}, fileError(403, "file is protected or outside write grants")
	}
	if err = r.Guard.Validate(); err != nil {
		return WriteReceipt{}, fileError(400, err.Error())
	}
	if r.RequestID == "" || len(r.RequestID) > 128 || r.Actor == "" || r.ExpectedRevision == "" {
		return WriteReceipt{}, fileError(400, "request_id, actor and expected_revision are required")
	}
	if len(r.Content) > MaxFileBytes {
		return WriteReceipt{}, fileError(413, "content exceeds 2 MiB")
	}
	if !utf8.ValidString(r.Content) || strings.ContainsRune(r.Content, 0) {
		return WriteReceipt{}, fileError(400, "only UTF-8 text is writable")
	}
	r.Path = p
	r.Root = scope
	r.Guard = nil
	identity := r.Identity
	r.Identity = EditIdentity{} // Display-name changes do not turn identical retries into new mutations.
	fingerprintData, _ := json.Marshal(r)
	fingerprint := Revision(append([]byte(e.rootIdentity), fingerprintData...))
	key := Revision([]byte(r.Actor+"\x00"+scope+"\x00"+r.RequestID)) + ".json"
	conn, err := e.db.Conn(ctx)
	if err != nil {
		return WriteReceipt{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return WriteReceipt{}, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var record editRecord
	if data, _, readErr := readRegular(e.state, key); readErr != nil {
		return WriteReceipt{}, readErr
	} else if data != nil {
		if json.Unmarshal(data, &record) != nil {
			return WriteReceipt{}, fileError(409, "write receipt is unavailable")
		}
		if record.Fingerprint != fingerprint {
			return WriteReceipt{}, &FileError{Status: 409, Code: "request_id_conflict", Message: "request_id was already used with different arguments"}
		}
		if record.Status == "completed" {
			return record.Receipt, nil
		}
	}
	scoped, err := OpenDirectory(e.root, scope, false)
	if err != nil {
		return WriteReceipt{}, err
	}
	defer scoped.Close()
	parent, err := OpenDirectory(scoped, path.Dir(p), true)
	if err != nil {
		return WriteReceipt{}, err
	}
	defer parent.Close()
	before, mode, err := readRegular(parent, path.Base(p))
	if err != nil {
		return WriteReceipt{}, err
	}
	current := revision(before)
	target := Revision([]byte(r.Content))
	if record.Status == "prepared" && current == target {
		record.Status = "completed"
		record.Receipt.Applied = true
		data, _ := json.Marshal(record)
		err = atomicRootWrite(e.state, key, data, 0600)
		return record.Receipt, err
	}
	if record.Status == "prepared" && current != r.ExpectedRevision {
		return WriteReceipt{}, &FileError{Status: 409, Code: "write_outcome_unknown", Message: "prepared write outcome is unknown; inspect current content and the retained receipt"}
	}
	if current != r.ExpectedRevision {
		return WriteReceipt{}, fileError(409, "revision conflict; read the current file before retrying")
	}
	if before != nil && (!utf8.Valid(before) || strings.ContainsRune(string(before), 0)) {
		return WriteReceipt{}, fileError(400, "only UTF-8 text is writable")
	}
	if record.Status == "" {
		record = editRecord{Fingerprint: fingerprint, Actor: r.Actor, Root: scope, Before: string(before[:min(len(before), 128<<10)]), CreatedAt: time.Now().UTC(), Status: "prepared", Receipt: WriteReceipt{Identity: identity, RequestID: r.RequestID, Path: p, PreviousRevision: current, Revision: target}}
		data, _ := json.Marshal(record)
		if err = atomicRootWrite(e.state, key, data, 0600); err != nil {
			return WriteReceipt{}, err
		}
	}
	if err = ctx.Err(); err != nil {
		return WriteReceipt{}, err
	}
	// Detect replacement or relocation of the authorized scope itself.
	checkScope, err := OpenDirectory(e.root, scope, false)
	if err != nil {
		return WriteReceipt{}, err
	}
	scopedInfo, _ := scoped.Stat(".")
	checkInfo, _ := checkScope.Stat(".")
	checkScope.Close()
	if scopedInfo == nil || checkInfo == nil || !os.SameFile(scopedInfo, checkInfo) {
		return WriteReceipt{}, fileError(409, "workspace root changed")
	}
	// Re-open through the original root to detect a moved/replaced parent.
	check, err := OpenDirectory(scoped, path.Dir(p), false)
	if err != nil {
		return WriteReceipt{}, err
	}
	a, _ := parent.Stat(".")
	b, _ := check.Stat(".")
	check.Close()
	if a == nil || b == nil || !os.SameFile(a, b) {
		return WriteReceipt{}, fileError(409, "parent directory changed")
	}
	now, _, err := readRegular(parent, path.Base(p))
	if err != nil {
		return WriteReceipt{}, err
	}
	if revision(now) != current {
		return WriteReceipt{}, fileError(409, "revision conflict")
	}
	if err = atomicRootWrite(parent, path.Base(p), []byte(r.Content), mode); err != nil {
		return WriteReceipt{}, &FileError{Status: 409, Code: "write_outcome_unknown", Message: "write may have applied; retry the same request_id to reconcile"}
	}
	record.Status = "completed"
	record.Receipt.Applied = true
	data, _ := json.Marshal(record)
	if err = atomicRootWrite(e.state, key, data, 0600); err != nil {
		return WriteReceipt{}, &FileError{Status: 409, Code: "write_outcome_unknown", Message: "write may have applied; retry the same request_id to reconcile"}
	}
	return record.Receipt, nil
}

// Read returns a revision suitable for Write, with identical path and grant checks.
func (e *Editor) Read(scope, p string, g *FolderGuard) (File, error) {
	p, err := CleanRelative(p)
	if err != nil || p == "." {
		return File{}, fileError(400, "invalid file path")
	}
	if Private(p) || !g.Allows(p, false) {
		return File{}, fileError(403, "file is private or outside read grants")
	}
	root, err := OpenDirectory(e.root, scope, false)
	if err != nil {
		return File{}, err
	}
	defer root.Close()
	parent, err := OpenDirectory(root, path.Dir(p), false)
	if os.IsNotExist(err) {
		return File{Path: p, Revision: MissingRevision}, nil
	}
	if err != nil {
		return File{}, err
	}
	defer parent.Close()
	data, _, err := readRegular(parent, path.Base(p))
	if err != nil {
		return File{}, err
	}
	if data != nil && (!utf8.Valid(data) || strings.ContainsRune(string(data), 0)) {
		return File{}, fileError(400, "only UTF-8 text is supported")
	}
	return File{Path: p, Exists: data != nil, Content: string(data), Encoding: "utf-8", Revision: revision(data), Size: int64(len(data))}, nil
}

// StatusCode avoids exposing local filesystem details through public endpoints.
func StatusCode(err error) int {
	var typed *FileError
	if errors.As(err, &typed) {
		return typed.Status
	}
	if os.IsNotExist(err) {
		return 404
	}
	return 503
}

// DefaultStateDir is shared by the workspace HTTP handlers and locally mounted
// Builder writer, keeping their revision check and replacement under one lock.
func DefaultStateDir(rootDir string) (string, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", err
	}
	rootDir, err = filepath.EvalSymlinks(rootDir)
	if err != nil {
		return "", err
	}
	// Explicit shared state supports containers with a read-only mount parent.
	if configured := os.Getenv("WORKSPACE_FILE_STATE_DIR"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("WORKSPACE_FILE_STATE_DIR must be absolute")
		}
		return filepath.Clean(configured), nil
	}
	if configured := os.Getenv("AGENTWORKS_STATE_ROOT"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", fmt.Errorf("AGENTWORKS_STATE_ROOT must be absolute")
		}
		return filepath.Join(configured, "file-edits", Revision([]byte(rootDir))), nil
	}
	return filepath.Join(filepath.Dir(rootDir), "."+filepath.Base(rootDir)+"-file-edits"), nil
}
func LockWorkspace(ctx context.Context, rootDir string) (func(), error) {
	state, err := DefaultStateDir(rootDir)
	if err != nil {
		return nil, err
	}
	editor, err := OpenEditor(rootDir, state)
	if err != nil {
		return nil, err
	}
	conn, err := editor.db.Conn(ctx)
	if err != nil {
		editor.Close()
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		editor.Close()
		return nil, err
	}
	return func() { conn.ExecContext(context.Background(), "ROLLBACK"); conn.Close(); editor.Close() }, nil
}

// ErrorDetails preserves uncertain outcomes without disclosing filesystem paths.
func ErrorDetails(err error) (string, string) {
	var typed *FileError
	message := "file operation failed"
	code := "workspace_error"
	if errors.As(err, &typed) {
		message = typed.Message
		if typed.Code != "" {
			return typed.Code, message
		}
	}
	switch StatusCode(err) {
	case 400:
		code = "invalid_arguments"
	case 403:
		code = "protected_path"
	case 404:
		code = "not_found"
	case 409:
		code = "revision_conflict"
	case 413:
		code = "too_large"
	}
	return code, message
}
