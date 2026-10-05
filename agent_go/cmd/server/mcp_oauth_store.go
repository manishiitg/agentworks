package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	_ "modernc.org/sqlite"
)

const mcpOAuthAccessPrefix = "aw_mcp_"
const mcpOAuthRefreshPrefix = "aw_mcp_refresh_"
const cliOAuthAccessPrefix = "aw_cli_"
const cliOAuthRefreshPrefix = "aw_cli_refresh_"
const cliOAuthClientID = "agentworks-cli"

type mcpOAuthStore struct{ db *sql.DB }

type mcpOAuthClient struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type mcpOAuthRequest struct {
	ClientID    string
	RedirectURI string
	Resource    string
	State       string
	Scopes      []string
	Challenge   string
	ExpiresAt   time.Time
}

type mcpOAuthGrant struct {
	FamilyID    string
	ClientID    string
	Resource    string
	Scopes      []string
	WorkflowIDs []string
	UserID      string
	Username    string
	Email       string
	Provider    string
	Expires     time.Time
}

func mcpOAuthSecret() (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	// OAuth clients and refresh tokens are server-owned state, never workflow
	// files. Keep the same isolation rule as personal access tokens.
	docs, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(docs, root)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("MCP OAuth state must be outside workspace documents")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if actualDocs, e := filepath.EvalSymlinks(docs); e == nil {
		docs = actualDocs
	}
	rel, err = filepath.Rel(docs, resolved)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("MCP OAuth state must be outside workspace documents")
	}
	authority := sha256.Sum256(GetAuthSecret())
	return filepath.Join(resolved, "auth", hex.EncodeToString(authority[:16])+".mcp-oauth.sqlite"), nil
}

func openMCPOAuthStore() (*mcpOAuthStore, error) {
	path, err := mcpOAuthSecret()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{filepath.Dir(path), path} {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("MCP OAuth state cannot be a symlink: %s", p)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS clients (id TEXT PRIMARY KEY, name TEXT NOT NULL, redirect_uris TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS requests (hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL, state TEXT NOT NULL, scopes TEXT NOT NULL, challenge TEXT NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS codes (hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL, scopes TEXT NOT NULL, challenge TEXT NOT NULL, user_id TEXT NOT NULL, username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tokens (hash TEXT PRIMARY KEY, kind TEXT NOT NULL, family_id TEXT NOT NULL, client_id TEXT NOT NULL, resource TEXT NOT NULL, scopes TEXT NOT NULL, user_id TEXT NOT NULL, username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER, revoked_at INTEGER)`,
		`CREATE INDEX IF NOT EXISTS tokens_family ON tokens(family_id)`,
		`CREATE TABLE IF NOT EXISTS cli_devices (hash TEXT PRIMARY KEY, verification_hash TEXT UNIQUE NOT NULL, scopes TEXT NOT NULL, status TEXT NOT NULL, expires_at INTEGER NOT NULL, polled_at INTEGER, user_id TEXT NOT NULL DEFAULT '', username TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '')`,
		`INSERT OR IGNORE INTO clients (id,name,redirect_uris,created_at) VALUES ('agentworks-cli','AgentWorks CLI','[]',0)`,
	} {
		if _, err = db.Exec(ddl); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	// Missing bounds preserve historical read/run access; Builder was never issued.
	for _, table := range []string{"codes", "tokens"} {
		if _, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN workflow_ids TEXT NOT NULL DEFAULT '[]'"); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			_ = db.Close()
			return nil, err
		}
	}
	return &mcpOAuthStore{db: db}, nil
}

func (s *mcpOAuthStore) Close() error { return s.db.Close() }

func mcpOAuthRandom(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func mcpOAuthHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *mcpOAuthStore) RegisterClient(ctx context.Context, name string, redirects []string) (mcpOAuthClient, error) {
	id, err := mcpOAuthRandom("mcp_client_")
	if err != nil {
		return mcpOAuthClient{}, err
	}
	data, _ := json.Marshal(redirects)
	result, err := s.db.ExecContext(ctx, `INSERT INTO clients (id,name,redirect_uris,created_at) SELECT ?,?,?,? WHERE (SELECT COUNT(*) FROM clients)<10000`, id, name, string(data), time.Now().Unix())
	if err != nil {
		return mcpOAuthClient{}, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return mcpOAuthClient{}, errors.New("MCP OAuth client registration limit reached")
	}
	return mcpOAuthClient{ID: id, Name: name, RedirectURIs: redirects}, nil
}

func (s *mcpOAuthStore) Client(ctx context.Context, id string) (mcpOAuthClient, error) {
	var c mcpOAuthClient
	var redirects string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,redirect_uris FROM clients WHERE id=?`, id).Scan(&c.ID, &c.Name, &redirects)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(redirects), &c.RedirectURIs)
	return c, err
}

func (s *mcpOAuthStore) SaveRequest(ctx context.Context, req mcpOAuthRequest) (string, error) {
	raw, err := mcpOAuthRandom("mcp_req_")
	if err != nil {
		return "", err
	}
	scopes, _ := json.Marshal(req.Scopes)
	_, err = s.db.ExecContext(ctx, `INSERT INTO requests (hash,client_id,redirect_uri,resource,state,scopes,challenge,expires_at) VALUES (?,?,?,?,?,?,?,?)`, mcpOAuthHash(raw), req.ClientID, req.RedirectURI, req.Resource, req.State, string(scopes), req.Challenge, req.ExpiresAt.Unix())
	return raw, err
}

func (s *mcpOAuthStore) Request(ctx context.Context, raw string) (mcpOAuthRequest, error) {
	var req mcpOAuthRequest
	var scopes string
	var expiry int64
	err := s.db.QueryRowContext(ctx, `SELECT client_id,redirect_uri,resource,state,scopes,challenge,expires_at FROM requests WHERE hash=? AND expires_at>?`, mcpOAuthHash(raw), time.Now().Unix()).Scan(&req.ClientID, &req.RedirectURI, &req.Resource, &req.State, &scopes, &req.Challenge, &expiry)
	if err != nil {
		return req, err
	}
	req.ExpiresAt = time.Unix(expiry, 0)
	err = json.Unmarshal([]byte(scopes), &req.Scopes)
	return req, err
}

func (s *mcpOAuthStore) Decide(ctx context.Context, raw string, user *UserClaims, approve bool, selected ...[]string) (mcpOAuthRequest, string, error) {
	req, err := s.Request(ctx, raw)
	if err != nil {
		return req, "", err
	}
	var workflowIDs []string
	if len(selected) > 0 {
		workflowIDs = slices.Clone(selected[0])
	}
	if approve {
		// Validate what will really be granted: the scopes this server and account allow, not everything requested.
		granted := mcpOAuthScopesFor(user, req.Scopes)
		if !slices.Contains(granted, "builder:chat") {
			workflowIDs = nil
		}
		if err := validateMCPOAuthBuilderSelection(ctx, user, granted, workflowIDs); err != nil {
			return req, "", &mcpOAuthRefusal{err}
		}
	}
	code := ""
	if approve {
		code, err = mcpOAuthRandom("mcp_code_")
		if err != nil {
			return req, "", err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return req, "", err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM requests WHERE hash=? AND expires_at>?`, mcpOAuthHash(raw), time.Now().Unix())
	if err != nil {
		return req, "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return req, "", sql.ErrNoRows
	}
	if approve {
		scopes, _ := json.Marshal(mcpOAuthScopesFor(user, req.Scopes))
		bounds, _ := json.Marshal(workflowIDs)
		_, err = tx.ExecContext(ctx, `INSERT INTO codes (hash,client_id,redirect_uri,resource,scopes,challenge,user_id,username,email,provider,expires_at,workflow_ids) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, mcpOAuthHash(code), req.ClientID, req.RedirectURI, req.Resource, string(scopes), req.Challenge, user.UserID, user.Username, user.Email, user.Provider, time.Now().Add(5*time.Minute).Unix(), string(bounds))
		if err != nil {
			return req, "", err
		}
	}
	return req, code, tx.Commit()
}

func (s *mcpOAuthStore) ExchangeCode(ctx context.Context, raw, clientID, redirectURI, resource, verifier string) (mcpOAuthGrant, string, string, error) {
	var grant mcpOAuthGrant
	var challenge, scopes, savedRedirect, bounds string
	var expiry int64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT client_id,redirect_uri,resource,scopes,challenge,user_id,username,email,provider,expires_at,workflow_ids FROM codes WHERE hash=?`, mcpOAuthHash(raw)).Scan(&grant.ClientID, &savedRedirect, &grant.Resource, &scopes, &challenge, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &expiry, &bounds)
	if err != nil {
		return grant, "", "", err
	}
	if expiry <= time.Now().Unix() || grant.ClientID != clientID || savedRedirect != redirectURI || grant.Resource != resource || len(verifier) < 43 || len(verifier) > 128 {
		return grant, "", "", errors.New("invalid authorization code")
	}
	for _, c := range verifier {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return grant, "", "", errors.New("invalid code verifier")
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(want)) != 1 {
		return grant, "", "", errors.New("invalid code verifier")
	}
	if err = json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return grant, "", "", err
	}
	if err = json.Unmarshal([]byte(bounds), &grant.WorkflowIDs); err != nil {
		return grant, "", "", err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM codes WHERE hash=?`, mcpOAuthHash(raw))
	if err != nil {
		return grant, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return grant, "", "", sql.ErrNoRows
	}
	grant.FamilyID, err = mcpOAuthRandom("")
	if err != nil {
		return grant, "", "", err
	}
	access, refresh, err := issueMCPOAuthPair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}

func issueMCPOAuthPair(ctx context.Context, tx *sql.Tx, grant *mcpOAuthGrant) (string, string, error) {
	accessPrefix, refreshPrefix := mcpOAuthAccessPrefix, mcpOAuthRefreshPrefix
	if grant.ClientID == cliOAuthClientID {
		accessPrefix, refreshPrefix = cliOAuthAccessPrefix, cliOAuthRefreshPrefix
	}
	access, err := mcpOAuthRandom(accessPrefix)
	if err != nil {
		return "", "", err
	}
	refresh, err := mcpOAuthRandom(refreshPrefix)
	if err != nil {
		return "", "", err
	}
	scopes, _ := json.Marshal(grant.Scopes)
	bounds, _ := json.Marshal(grant.WorkflowIDs)
	now := time.Now()
	grant.Expires = now.Add(time.Hour)
	for _, token := range []struct {
		raw, kind string
		expiry    time.Time
	}{{access, "access", grant.Expires}, {refresh, "refresh", now.Add(30 * 24 * time.Hour)}} {
		_, err = tx.ExecContext(ctx, `INSERT INTO tokens (hash,kind,family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,workflow_ids) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, mcpOAuthHash(token.raw), token.kind, grant.FamilyID, grant.ClientID, grant.Resource, string(scopes), grant.UserID, grant.Username, grant.Email, grant.Provider, token.expiry.Unix(), string(bounds))
		if err != nil {
			return "", "", err
		}
	}
	return access, refresh, nil
}

func scanMCPOAuthGrant(row interface{ Scan(...any) error }) (mcpOAuthGrant, error) {
	var grant mcpOAuthGrant
	var scopes, bounds string
	var expiry int64
	err := row.Scan(&grant.FamilyID, &grant.ClientID, &grant.Resource, &scopes, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &expiry, &bounds)
	if err != nil {
		return grant, err
	}
	grant.Expires = time.Unix(expiry, 0)
	if err = json.Unmarshal([]byte(scopes), &grant.Scopes); err == nil {
		err = json.Unmarshal([]byte(bounds), &grant.WorkflowIDs)
	}
	return grant, err
}

const mcpOAuthGrantColumns = `family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,workflow_ids`

func (s *mcpOAuthStore) Authenticate(ctx context.Context, raw string) (mcpOAuthGrant, error) {
	if !(strings.HasPrefix(raw, mcpOAuthAccessPrefix) || strings.HasPrefix(raw, cliOAuthAccessPrefix)) || strings.HasPrefix(raw, mcpOAuthRefreshPrefix) || strings.HasPrefix(raw, cliOAuthRefreshPrefix) {
		return mcpOAuthGrant{}, sql.ErrNoRows
	}
	return scanMCPOAuthGrant(s.db.QueryRowContext(ctx, `SELECT `+mcpOAuthGrantColumns+` FROM tokens WHERE hash=? AND kind='access' AND revoked_at IS NULL AND expires_at>?`, mcpOAuthHash(raw), time.Now().Unix()))
}

func (s *mcpOAuthStore) ActiveFamily(ctx context.Context, family string) (mcpOAuthGrant, error) {
	return scanMCPOAuthGrant(s.db.QueryRowContext(ctx, `SELECT `+mcpOAuthGrantColumns+` FROM tokens WHERE family_id=? AND kind='access' AND revoked_at IS NULL AND expires_at>? ORDER BY expires_at DESC LIMIT 1`, family, time.Now().Unix()))
}

func (s *mcpOAuthStore) Refresh(ctx context.Context, raw, clientID, resource string) (mcpOAuthGrant, string, string, error) {
	var grant mcpOAuthGrant
	if !(strings.HasPrefix(raw, mcpOAuthRefreshPrefix) || strings.HasPrefix(raw, cliOAuthRefreshPrefix)) {
		return grant, "", "", sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	var scopes, bounds string
	var expiry int64
	var used, revoked sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,used_at,revoked_at,workflow_ids FROM tokens WHERE hash=? AND kind='refresh'`, mcpOAuthHash(raw)).Scan(&grant.FamilyID, &grant.ClientID, &grant.Resource, &scopes, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &expiry, &used, &revoked, &bounds)
	if err != nil {
		return grant, "", "", err
	}
	if used.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at=COALESCE(revoked_at,?) WHERE family_id=?`, time.Now().Unix(), grant.FamilyID); err != nil {
			return grant, "", "", err
		}
		if err := tx.Commit(); err != nil {
			return grant, "", "", err
		}
		return grant, "", "", errors.New("refresh token reuse detected")
	}
	if revoked.Valid || expiry <= time.Now().Unix() || grant.ClientID != clientID || grant.Resource != resource || (grant.ClientID == cliOAuthClientID) != strings.HasPrefix(raw, cliOAuthRefreshPrefix) {
		return grant, "", "", errors.New("invalid refresh token")
	}
	if err = json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return grant, "", "", err
	}
	if err = json.Unmarshal([]byte(bounds), &grant.WorkflowIDs); err != nil {
		return grant, "", "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tokens SET used_at=? WHERE hash=? AND used_at IS NULL AND revoked_at IS NULL`, time.Now().Unix(), mcpOAuthHash(raw))
	if err != nil {
		return grant, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return grant, "", "", sql.ErrNoRows
	}
	access, refresh, err := issueMCPOAuthPair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}

type mcpOAuthConnection struct {
	ID           string    `json:"id"`
	ClientName   string    `json:"client_name"`
	Scopes       []string  `json:"scopes"`
	WorkflowIDs  []string  `json:"workflow_ids"`
	AllWorkflows bool      `json:"all_workflows"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (s *mcpOAuthStore) Connections(ctx context.Context, userID string) ([]mcpOAuthConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.family_id,c.name,t.scopes,MAX(t.expires_at),t.workflow_ids FROM tokens t JOIN clients c ON c.id=t.client_id WHERE t.user_id=? AND t.kind='refresh' AND t.revoked_at IS NULL AND t.expires_at>? GROUP BY t.family_id,c.name,t.scopes,t.workflow_ids ORDER BY MAX(t.expires_at) DESC`, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []mcpOAuthConnection{}
	for rows.Next() {
		var c mcpOAuthConnection
		var scopes, bounds string
		var expiry int64
		if err := rows.Scan(&c.ID, &c.ClientName, &scopes, &expiry, &bounds); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &c.Scopes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(bounds), &c.WorkflowIDs); err != nil {
			return nil, err
		}
		c.AllWorkflows = len(c.WorkflowIDs) == 0 && !slices.Contains(c.Scopes, "builder:chat")
		c.ExpiresAt = time.Unix(expiry, 0)
		connections = append(connections, c)
	}
	return connections, rows.Err()
}

func (s *mcpOAuthStore) RevokeFamily(ctx context.Context, family, userID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at=COALESCE(revoked_at,?) WHERE family_id=? AND user_id=? AND revoked_at IS NULL`, time.Now().Unix(), family, userID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// mcpOAuthRefusal is a consent the server refuses for a reason the person can act on (the request itself is intact).
type mcpOAuthRefusal struct{ error }

func (r *mcpOAuthRefusal) Unwrap() error { return r.error }
