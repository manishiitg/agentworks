package mcpoauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the sqlite-backed OAuth state: clients, pending requests, codes,
// token families, and CLI device approvals. One Store per OpenStore call;
// hosts open, use, and close per request.
type Store struct {
	db  *sql.DB
	cfg Config
}

// OpenStore creates the sqlite file (0600, no symlinks) and schema.
func OpenStore(path string, cfg Config) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	for _, p := range []string{filepath.Dir(path), path} {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("OAuth state cannot be a symlink: %s", p)
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
	// WAL + busy_timeout in the DSN so concurrent connections agree; a
	// one-time PRAGMA would not apply to later pooled connections.
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS clients (id TEXT PRIMARY KEY, name TEXT NOT NULL, redirect_uris TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS requests (hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL, state TEXT NOT NULL, scopes TEXT NOT NULL, challenge TEXT NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS codes (hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, resource TEXT NOT NULL, scopes TEXT NOT NULL, challenge TEXT NOT NULL, user_id TEXT NOT NULL, username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tokens (hash TEXT PRIMARY KEY, kind TEXT NOT NULL, family_id TEXT NOT NULL, client_id TEXT NOT NULL, resource TEXT NOT NULL, scopes TEXT NOT NULL, user_id TEXT NOT NULL, username TEXT NOT NULL, email TEXT NOT NULL, provider TEXT NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER, revoked_at INTEGER)`,
		`CREATE INDEX IF NOT EXISTS tokens_family ON tokens(family_id)`,
		`CREATE TABLE IF NOT EXISTS cli_devices (hash TEXT PRIMARY KEY, verification_hash TEXT UNIQUE NOT NULL, scopes TEXT NOT NULL, status TEXT NOT NULL, expires_at INTEGER NOT NULL, polled_at INTEGER, user_id TEXT NOT NULL DEFAULT '', username TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '')`,
	}
	if cfg.CLIClientID != "" {
		name := cfg.CLIClientName
		if name == "" {
			name = "CLI"
		}
		ddl = append(ddl, `INSERT OR IGNORE INTO clients (id,name,redirect_uris,created_at) VALUES ('`+cfg.CLIClientID+`','`+name+`','[]',0)`)
	}
	for _, stmt := range ddl {
		if _, err = db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{db: db, cfg: cfg}, nil
}

// Close releases the store.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) maxClients() int {
	if s.cfg.MaxClients > 0 {
		return s.cfg.MaxClients
	}
	return 10000
}

func (s *Store) isCLI(clientID string) bool {
	return s.cfg.CLIClientID != "" && clientID == s.cfg.CLIClientID
}

// RegisterClient adds a dynamically registered OAuth client.
func (s *Store) RegisterClient(ctx context.Context, name string, redirects []string) (Client, error) {
	id, err := randomToken("mcp_client_")
	if err != nil {
		return Client{}, err
	}
	data, _ := json.Marshal(redirects)
	result, err := s.db.ExecContext(ctx, `INSERT INTO clients (id,name,redirect_uris,created_at) SELECT ?,?,?,? WHERE (SELECT COUNT(*) FROM clients)< ?`, id, name, string(data), time.Now().Unix(), s.maxClients())
	if err != nil {
		return Client{}, err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return Client{}, errors.New("OAuth client registration limit reached")
	}
	return Client{ID: id, Name: name, RedirectURIs: redirects}, nil
}

// Client returns one registered client.
func (s *Store) Client(ctx context.Context, id string) (Client, error) {
	var c Client
	var redirects string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,redirect_uris FROM clients WHERE id=?`, id).Scan(&c.ID, &c.Name, &redirects)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(redirects), &c.RedirectURIs)
	return c, err
}

// SaveRequest stores a pending authorization request, returning its opaque id.
func (s *Store) SaveRequest(ctx context.Context, req AuthRequest) (string, error) {
	raw, err := randomToken("mcp_req_")
	if err != nil {
		return "", err
	}
	scopes, _ := json.Marshal(req.Scopes)
	_, err = s.db.ExecContext(ctx, `INSERT INTO requests (hash,client_id,redirect_uri,resource,state,scopes,challenge,expires_at) VALUES (?,?,?,?,?,?,?,?)`, hashToken(raw), req.ClientID, req.RedirectURI, req.Resource, req.State, string(scopes), req.Challenge, req.ExpiresUnix)
	return raw, err
}

// Request returns one unexpired pending authorization request.
func (s *Store) Request(ctx context.Context, raw string) (AuthRequest, error) {
	var req AuthRequest
	var scopes string
	err := s.db.QueryRowContext(ctx, `SELECT client_id,redirect_uri,resource,state,scopes,challenge,expires_at FROM requests WHERE hash=? AND expires_at>?`, hashToken(raw), time.Now().Unix()).Scan(&req.ClientID, &req.RedirectURI, &req.Resource, &req.State, &scopes, &req.Challenge, &req.ExpiresUnix)
	if err != nil {
		return req, err
	}
	err = json.Unmarshal([]byte(scopes), &req.Scopes)
	return req, err
}

// Decide consumes a pending request. On approve it issues an authorization
// code bound to the user; on deny it just consumes the request.
func (s *Store) Decide(ctx context.Context, raw string, user User, approve bool) (AuthRequest, string, error) {
	req, err := s.Request(ctx, raw)
	if err != nil {
		return req, "", err
	}
	code := ""
	if approve {
		code, err = randomToken("mcp_code_")
		if err != nil {
			return req, "", err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return req, "", err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM requests WHERE hash=? AND expires_at>?`, hashToken(raw), time.Now().Unix())
	if err != nil {
		return req, "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return req, "", sql.ErrNoRows
	}
	if approve {
		scopes, _ := json.Marshal(req.Scopes)
		_, err = tx.ExecContext(ctx, `INSERT INTO codes (hash,client_id,redirect_uri,resource,scopes,challenge,user_id,username,email,provider,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, hashToken(code), req.ClientID, req.RedirectURI, req.Resource, string(scopes), req.Challenge, user.ID, user.Username, user.Email, user.Provider, time.Now().Add(5*time.Minute).Unix())
		if err != nil {
			return req, "", err
		}
	}
	return req, code, tx.Commit()
}

// ExchangeCode swaps a single-use authorization code (PKCE-verified) for an
// access/refresh pair in a new family.
func (s *Store) ExchangeCode(ctx context.Context, raw, clientID, redirectURI, resource, verifier string) (Grant, string, string, error) {
	var grant Grant
	var challenge, scopes, savedRedirect string
	var expiry int64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT client_id,redirect_uri,resource,scopes,challenge,user_id,username,email,provider,expires_at FROM codes WHERE hash=?`, hashToken(raw)).Scan(&grant.ClientID, &savedRedirect, &grant.Resource, &scopes, &challenge, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &expiry)
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
	result, err := tx.ExecContext(ctx, `DELETE FROM codes WHERE hash=?`, hashToken(raw))
	if err != nil {
		return grant, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return grant, "", "", sql.ErrNoRows
	}
	grant.FamilyID, err = randomToken("")
	if err != nil {
		return grant, "", "", err
	}
	access, refresh, err := s.issuePair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}

func (s *Store) issuePair(ctx context.Context, tx *sql.Tx, grant *Grant) (string, string, error) {
	accessPrefix, refreshPrefix := s.cfg.AccessPrefix, s.cfg.RefreshPrefix
	if s.isCLI(grant.ClientID) {
		accessPrefix, refreshPrefix = s.cfg.CLIAccessPrefix, s.cfg.CLIRefreshPrefix
	}
	access, err := randomToken(accessPrefix)
	if err != nil {
		return "", "", err
	}
	refresh, err := randomToken(refreshPrefix)
	if err != nil {
		return "", "", err
	}
	scopes, _ := json.Marshal(grant.Scopes)
	now := time.Now()
	grant.Expires = now.Add(time.Hour).Unix()
	for _, token := range []struct {
		raw, kind string
		expiry    time.Time
	}{{access, "access", time.Unix(grant.Expires, 0)}, {refresh, "refresh", now.Add(30 * 24 * time.Hour)}} {
		_, err = tx.ExecContext(ctx, `INSERT INTO tokens (hash,kind,family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, hashToken(token.raw), token.kind, grant.FamilyID, grant.ClientID, grant.Resource, string(scopes), grant.UserID, grant.Username, grant.Email, grant.Provider, token.expiry.Unix())
		if err != nil {
			return "", "", err
		}
	}
	return access, refresh, nil
}

const grantColumns = `family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at`

func scanGrant(row interface{ Scan(...any) error }) (Grant, error) {
	var grant Grant
	var scopes string
	err := row.Scan(&grant.FamilyID, &grant.ClientID, &grant.Resource, &scopes, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &grant.Expires)
	if err != nil {
		return grant, err
	}
	err = json.Unmarshal([]byte(scopes), &grant.Scopes)
	return grant, err
}

func (s *Store) accessPrefixes() []string {
	out := []string{s.cfg.AccessPrefix}
	if s.cfg.CLIClientID != "" {
		out = append(out, s.cfg.CLIAccessPrefix)
	}
	return out
}

func (s *Store) refreshPrefixes() []string {
	out := []string{s.cfg.RefreshPrefix}
	if s.cfg.CLIClientID != "" {
		out = append(out, s.cfg.CLIRefreshPrefix)
	}
	return out
}

func hasAnyPrefix(raw string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(raw, p) {
			return true
		}
	}
	return false
}

// Authenticate resolves a live access token to its grant. Refresh tokens
// are never valid here.
func (s *Store) Authenticate(ctx context.Context, raw string) (Grant, error) {
	if !hasAnyPrefix(raw, s.accessPrefixes()) || hasAnyPrefix(raw, s.refreshPrefixes()) {
		return Grant{}, sql.ErrNoRows
	}
	return scanGrant(s.db.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM tokens WHERE hash=? AND kind='access' AND revoked_at IS NULL AND expires_at>?`, hashToken(raw), time.Now().Unix()))
}

// ActiveFamily returns the newest live access grant in a family.
func (s *Store) ActiveFamily(ctx context.Context, family string) (Grant, error) {
	return scanGrant(s.db.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM tokens WHERE family_id=? AND kind='access' AND revoked_at IS NULL AND expires_at>? ORDER BY expires_at DESC LIMIT 1`, family, time.Now().Unix()))
}

// Refresh rotates a refresh token, revoking the whole family on reuse.
func (s *Store) Refresh(ctx context.Context, raw, clientID, resource string) (Grant, string, string, error) {
	var grant Grant
	if !hasAnyPrefix(raw, s.refreshPrefixes()) {
		return grant, "", "", sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	var scopes string
	var expiry int64
	var used, revoked sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT family_id,client_id,resource,scopes,user_id,username,email,provider,expires_at,used_at,revoked_at FROM tokens WHERE hash=? AND kind='refresh'`, hashToken(raw)).Scan(&grant.FamilyID, &grant.ClientID, &grant.Resource, &scopes, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider, &expiry, &used, &revoked)
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
		return grant, "", "", ErrReuse
	}
	cli := s.isCLI(grant.ClientID)
	if revoked.Valid || expiry <= time.Now().Unix() || grant.ClientID != clientID || grant.Resource != resource || cli != (s.cfg.CLIClientID != "" && strings.HasPrefix(raw, s.cfg.CLIRefreshPrefix)) {
		return grant, "", "", errors.New("invalid refresh token")
	}
	if err = json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return grant, "", "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE tokens SET used_at=? WHERE hash=? AND used_at IS NULL AND revoked_at IS NULL`, time.Now().Unix(), hashToken(raw))
	if err != nil {
		return grant, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return grant, "", "", sql.ErrNoRows
	}
	access, refresh, err := s.issuePair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}

// Connections lists a user's revocable grant families (never tokens).
func (s *Store) Connections(ctx context.Context, userID string) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.family_id,c.name,t.scopes,MAX(t.expires_at) FROM tokens t JOIN clients c ON c.id=t.client_id WHERE t.user_id=? AND t.kind='refresh' AND t.revoked_at IS NULL AND t.expires_at>? GROUP BY t.family_id,c.name,t.scopes ORDER BY MAX(t.expires_at) DESC`, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := []Connection{}
	for rows.Next() {
		var c Connection
		var scopes string
		var expiry int64
		if err := rows.Scan(&c.ID, &c.ClientName, &scopes, &expiry); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &c.Scopes); err != nil {
			return nil, err
		}
		c.ExpiresAt = time.Unix(expiry, 0)
		connections = append(connections, c)
	}
	return connections, rows.Err()
}

// RevokeFamily revokes one of the user's grant families.
func (s *Store) RevokeFamily(ctx context.Context, family, userID string) error {
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

// RevokeByRefresh revokes the family holding a CLI refresh token.
func (s *Store) RevokeByRefresh(ctx context.Context, raw string) error {
	if s.cfg.CLIClientID == "" || !strings.HasPrefix(raw, s.cfg.CLIRefreshPrefix) {
		return sql.ErrNoRows
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at=COALESCE(revoked_at,?) WHERE family_id=(SELECT family_id FROM tokens WHERE hash=? AND kind='refresh' AND client_id=?) AND revoked_at IS NULL`, time.Now().Unix(), hashToken(raw), s.cfg.CLIClientID)
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

// CreateCLIDevice starts a device-flow approval. Requires CLIClientID.
func (s *Store) CreateCLIDevice(ctx context.Context) (deviceCode, verificationCode string, err error) {
	deviceCode, err = randomToken("cli_device_")
	if err != nil {
		return
	}
	verificationCode, err = randomToken("cli_verify_")
	if err != nil {
		return
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM cli_devices WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return
	}
	scopes, _ := json.Marshal(s.cfg.CLIDefaultScopes)
	result, err := s.db.ExecContext(ctx, `INSERT INTO cli_devices(hash,verification_hash,scopes,status,expires_at) SELECT ?,?,?,'pending',? WHERE (SELECT COUNT(*) FROM cli_devices)<10000`, hashToken(deviceCode), hashToken(verificationCode), string(scopes), time.Now().Add(10*time.Minute).Unix())
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = errors.New("too many pending CLI sign-ins")
		}
	}
	return
}

// CLIDeviceRequest returns the scopes a pending device approval would grant.
func (s *Store) CLIDeviceRequest(ctx context.Context, verificationCode string) ([]string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT scopes FROM cli_devices WHERE verification_hash=? AND status='pending' AND expires_at>?`, hashToken(verificationCode), time.Now().Unix()).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var scopes []string
	err = json.Unmarshal([]byte(raw), &scopes)
	return scopes, err
}

// DecideCLIDevice approves or denies a pending device approval.
func (s *Store) DecideCLIDevice(ctx context.Context, verificationCode string, user User, approve bool) error {
	status := "denied"
	if approve {
		status = "approved"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE cli_devices SET status=?,user_id=?,username=?,email=?,provider=? WHERE verification_hash=? AND status='pending' AND expires_at>?`, status, user.ID, user.Username, user.Email, user.Provider, hashToken(verificationCode), time.Now().Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// PollCLIDevice exchanges an approved device code for a token pair, or
// reports pending/denied/slow-down.
func (s *Store) PollCLIDevice(ctx context.Context, raw, resource string) (Grant, string, string, error) {
	var grant Grant
	var status, scopes string
	var expiry int64
	var polled sql.NullInt64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT scopes,status,expires_at,polled_at,user_id,username,email,provider FROM cli_devices WHERE hash=?`, hashToken(raw)).Scan(&scopes, &status, &expiry, &polled, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider)
	if err != nil {
		return grant, "", "", err
	}
	if expiry <= time.Now().Unix() {
		return grant, "", "", sql.ErrNoRows
	}
	if status == "denied" {
		return grant, "", "", ErrDenied
	}
	if status == "pending" {
		now := time.Now().Unix()
		if polled.Valid && now-polled.Int64 < 2 {
			return grant, "", "", ErrSlowDown
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cli_devices SET polled_at=? WHERE hash=?`, now, hashToken(raw)); err != nil {
			return grant, "", "", err
		}
		if err := tx.Commit(); err != nil {
			return grant, "", "", err
		}
		return grant, "", "", ErrPending
	}
	if status != "approved" || grant.UserID == "" {
		return grant, "", "", sql.ErrNoRows
	}
	if err := json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return grant, "", "", err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM cli_devices WHERE hash=? AND status='approved'`, hashToken(raw))
	if err != nil {
		return grant, "", "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return grant, "", "", sql.ErrNoRows
	}
	grant.FamilyID, err = randomToken("")
	if err != nil {
		return grant, "", "", err
	}
	grant.ClientID = s.cfg.CLIClientID
	grant.Resource = resource
	access, refresh, err := s.issuePair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}
