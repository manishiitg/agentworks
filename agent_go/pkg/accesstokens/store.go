// Package accesstokens stores revocable, hashed personal access tokens.
package accesstokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

const Prefix = "aw_pat_"

var ErrInvalid = errors.New("access token is invalid, expired, or revoked")
var Scopes = []string{"workflows:read", "files:read", "runs:execute", "files:write", "plan:write", "builder:chat", "relays:write", "crews:read", "crews:run", "crews:write", "code:review", "vault:manage"}

// workflowScopes is the complete workflow permission set; FullBuilderAccess
// means all of these, independent of any Crew permissions.
var workflowScopes = []string{"workflows:read", "files:read", "runs:execute", "files:write", "plan:write", "builder:chat"}

type Token struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	UserID       string   `json:"-"`
	Username     string   `json:"-"`
	Email        string   `json:"-"`
	Provider     string   `json:"-"`
	Scopes       []string `json:"scopes"`
	WorkflowIDs  []string `json:"workflow_ids"`
	AllWorkflows bool     `json:"all_workflows"`
	// CrewIDs / AllCrews bound crews:read, crews:run and crews:write the same way
	// WorkflowIDs / AllWorkflows bound the workflow permissions.
	CrewIDs    []string   `json:"crew_ids"`
	AllCrews   bool       `json:"all_crews"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func (t Token) Allows(scope string) bool { return slices.Contains(t.Scopes, scope) }
func (t Token) AllowsWorkflow(id string) bool {
	return t.AllWorkflows || slices.Contains(t.WorkflowIDs, id)
}
func (t Token) AllowsCrew(id string) bool {
	return t.AllCrews || slices.Contains(t.CrewIDs, id)
}
func (t Token) FullBuilderAccess() bool {
	if !t.AllWorkflows {
		return false
	}
	for _, s := range workflowScopes {
		if !t.Allows(s) {
			return false
		}
	}
	return true
}

// BuilderAccess is explicit authoring consent. It follows the account's own permission (every workflow the person
// may edit, checked live on every call) unless a token names specific workflow IDs (an older grant, or a personal
// access token made for a few workflows). Direct file/plan write scopes are deliberately unnecessary and remain unissued.
func (t Token) BuilderAccess() bool {
	if t.AllWorkflows && len(t.WorkflowIDs) > 0 || !t.AllWorkflows && (len(t.WorkflowIDs) == 0 || len(t.WorkflowIDs) > 200) {
		return false
	}
	for _, scope := range []string{"builder:chat", "workflows:read", "files:read", "runs:execute"} {
		if !t.Allows(scope) {
			return false
		}
	}
	seen := map[string]bool{}
	for _, id := range t.WorkflowIDs {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

// RelayBuilderAccess delegates Relay authoring only. AllWorkflows permits
// creating new Relays; a selected-ID grant may edit only those Relays.
func (t Token) RelayBuilderAccess() bool {
	if !t.Allows("relays:write") || (!t.AllWorkflows && len(t.WorkflowIDs) == 0) {
		return false
	}
	for _, scope := range []string{"workflows:read", "files:read", "runs:execute"} {
		if !t.Allows(scope) {
			return false
		}
	}
	return true
}

func Validate(t Token, now time.Time) error {
	if strings.TrimSpace(t.Name) == "" || len(t.Name) > 80 || t.UserID == "" {
		return errors.New("a token name (1–80 characters) and user are required")
	}
	if !t.ExpiresAt.After(now) || t.ExpiresAt.After(now.Add(90*24*time.Hour)) {
		return errors.New("expiry must be within 90 days")
	}
	if len(t.Scopes) == 0 || len(t.Scopes) > len(Scopes) {
		return errors.New("select at least one permission")
	}
	seen := map[string]bool{}
	for _, s := range t.Scopes {
		if !slices.Contains(Scopes, s) || seen[s] {
			return errors.New("invalid or duplicate permission")
		}
		if s == "files:write" || s == "plan:write" {
			return errors.New("direct files:write and plan:write permissions are not issued; use scoped Builder chat")
		}
		seen[s] = true
	}
	hasWorkflowScope, hasCrewScope := false, false
	for _, s := range t.Scopes {
		switch {
		case strings.HasPrefix(s, "crews:"):
			hasCrewScope = true
		case s == "code:review" || s == "vault:manage":
			// Bounded by the account (admin or Code reviewer), not by IDs.
		default:
			hasWorkflowScope = true
		}
	}
	// A bound is required exactly for the kind of permission granted; a
	// Crew-only token names no workflows and vice versa.
	if hasWorkflowScope && (t.AllWorkflows && len(t.WorkflowIDs) > 0 || !t.AllWorkflows && len(t.WorkflowIDs) == 0) || len(t.WorkflowIDs) > 200 {
		return errors.New("choose all accessible workflows or specific workflow IDs")
	}
	if !hasWorkflowScope && (t.AllWorkflows || len(t.WorkflowIDs) > 0) {
		return errors.New("workflow bounds need a workflow permission")
	}
	if hasCrewScope && (t.AllCrews && len(t.CrewIDs) > 0 || !t.AllCrews && len(t.CrewIDs) == 0) || len(t.CrewIDs) > 200 {
		return errors.New("choose all accessible Crews or specific Crew IDs")
	}
	if !hasCrewScope && (t.AllCrews || len(t.CrewIDs) > 0) {
		return errors.New("Crew bounds need a Crew permission (crews:read, crews:run or crews:write)")
	}
	if t.Allows("builder:chat") && !t.BuilderAccess() {
		return errors.New("Builder chat requires workflows:read, files:read and runs:execute")
	}
	if t.Allows("relays:write") && !t.RelayBuilderAccess() {
		return errors.New("Relay authoring requires workflows:read, files:read and runs:execute")
	}
	return nil
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("access token database path must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// The configured state root is trusted server configuration. Reject links
	// for the private auth directory and database while allowing OS aliases
	// such as macOS /var -> /private/var in ancestors.
	for _, p := range []string{dir, path} {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("access token storage must not use symbolic links")
		}
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS access_tokens (
 id TEXT PRIMARY KEY, hash TEXT NOT NULL UNIQUE, user_id TEXT NOT NULL,
 username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL,
 name TEXT NOT NULL, scopes TEXT NOT NULL, workflow_ids TEXT NOT NULL, all_workflows INTEGER NOT NULL,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	// Crew bounds arrived after the table; add them to existing databases.
	for _, column := range []string{`crew_ids TEXT NOT NULL DEFAULT '[]'`, `all_crews INTEGER NOT NULL DEFAULT 0`} {
		if _, alterErr := db.Exec(`ALTER TABLE access_tokens ADD COLUMN ` + column); alterErr != nil && !strings.Contains(alterErr.Error(), "duplicate column") {
			db.Close()
			return nil, alterErr
		}
	}
	return &Store{db}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func hash(raw string) string  { b := sha256.Sum256([]byte(raw)); return hex.EncodeToString(b[:]) }
func (s *Store) Issue(ctx context.Context, t Token, now time.Time) (Token, string, error) {
	if err := Validate(t, now); err != nil {
		return Token{}, "", err
	}
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return Token{}, "", err
	}
	raw := Prefix + hex.EncodeToString(entropy)
	t.ID = hex.EncodeToString(entropy[:8])
	t.CreatedAt = now.UTC()
	t.Name = strings.TrimSpace(t.Name)
	// One token per user: issuing replaces any live token. History stays
	// listed; only unrevoked, unexpired rows lose access. The cap below
	// remains as a backstop for concurrent double issuance.
	if _, err := s.db.ExecContext(ctx, `UPDATE access_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE user_id=? AND revoked_at IS NULL AND expires_at>?`, now.Unix(), t.UserID, now.Unix()); err != nil {
		return Token{}, "", err
	}
	scopes, _ := json.Marshal(t.Scopes)
	ids, _ := json.Marshal(t.WorkflowIDs)
	if t.CrewIDs == nil {
		t.CrewIDs = []string{}
	}
	crewIDs, _ := json.Marshal(t.CrewIDs)
	// Cap issuance in the same statement, including concurrent requests.
	result, err := s.db.ExecContext(ctx, `INSERT INTO access_tokens (id,hash,user_id,username,email,provider,name,scopes,workflow_ids,all_workflows,crew_ids,all_crews,created_at,expires_at)
 SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,? WHERE (SELECT COUNT(*) FROM access_tokens WHERE user_id=? AND revoked_at IS NULL AND expires_at>?)<100`, t.ID, hash(raw), t.UserID, t.Username, t.Email, t.Provider, t.Name, string(scopes), string(ids), t.AllWorkflows, string(crewIDs), t.AllCrews, now.Unix(), t.ExpiresAt.Unix(), t.UserID, now.Unix())
	if err != nil {
		return Token{}, "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Token{}, "", errors.New("maximum 100 active tokens; revoke an existing token first")
	}
	return t, raw, nil
}

const columns = `id,user_id,username,email,provider,name,scopes,workflow_ids,all_workflows,crew_ids,all_crews,created_at,expires_at,last_used_at,revoked_at`

func scan(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var scopes, ids, crewIDs string
	var created, expires int64
	var used, revoked sql.NullInt64
	err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Email, &t.Provider, &t.Name, &scopes, &ids, &t.AllWorkflows, &crewIDs, &t.AllCrews, &created, &expires, &used, &revoked)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(ids), &t.WorkflowIDs); err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(crewIDs), &t.CrewIDs); err != nil {
		return t, err
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	t.ExpiresAt = time.Unix(expires, 0).UTC()
	if used.Valid {
		v := time.Unix(used.Int64, 0).UTC()
		t.LastUsedAt = &v
	}
	if revoked.Valid {
		v := time.Unix(revoked.Int64, 0).UTC()
		t.RevokedAt = &v
	}
	return t, nil
}
func (s *Store) Authenticate(ctx context.Context, raw string, now time.Time) (Token, error) {
	if !strings.HasPrefix(raw, Prefix) || len(raw) != len(Prefix)+64 {
		return Token{}, ErrInvalid
	}
	t, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM access_tokens WHERE hash=? AND revoked_at IS NULL AND expires_at>?`, hash(raw), now.Unix()))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrInvalid
	}
	if err != nil {
		return t, err
	}
	// A conditional update cannot resurrect a token revoked between read and touch.
	result, err := s.db.ExecContext(ctx, `UPDATE access_tokens SET last_used_at=? WHERE id=? AND revoked_at IS NULL AND expires_at>?`, now.Unix(), t.ID, now.Unix())
	if err != nil {
		return t, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return t, ErrInvalid
	}
	return t, nil
}
func (s *Store) Active(ctx context.Context, id string, now time.Time) (Token, error) {
	t, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM access_tokens WHERE id=? AND revoked_at IS NULL AND expires_at>?`, id, now.Unix()))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrInvalid
	}
	return t, err
}
func (s *Store) List(ctx context.Context, userID string) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM access_tokens WHERE user_id=? ORDER BY (revoked_at IS NULL AND expires_at>?) DESC, created_at DESC LIMIT 500`, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Token{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(ctx context.Context, id, userID string, now time.Time) error {
	r, err := s.db.ExecContext(ctx, `UPDATE access_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE id=? AND user_id=?`, now.Unix(), id, userID)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w", ErrInvalid)
	}
	return nil
}
