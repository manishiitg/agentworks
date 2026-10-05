package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var errCLIOAuthPending = errors.New("authorization_pending")
var errCLIOAuthDenied = errors.New("access_denied")
var errCLIOAuthSlowDown = errors.New("slow_down")

func (s *mcpOAuthStore) CreateCLIDevice(ctx context.Context) (deviceCode, verificationCode string, err error) {
	deviceCode, err = mcpOAuthRandom("cli_device_")
	if err != nil {
		return
	}
	verificationCode, err = mcpOAuthRandom("cli_verify_")
	if err != nil {
		return
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM cli_devices WHERE expires_at<=?`, time.Now().Unix()); err != nil {
		return
	}
	scopes, _ := json.Marshal(mcpOAuthDefaultScopes)
	result, err := s.db.ExecContext(ctx, `INSERT INTO cli_devices(hash,verification_hash,scopes,status,expires_at) SELECT ?,?,?,'pending',? WHERE (SELECT COUNT(*) FROM cli_devices)<10000`, mcpOAuthHash(deviceCode), mcpOAuthHash(verificationCode), string(scopes), time.Now().Add(10*time.Minute).Unix())
	if err == nil {
		var n int64
		n, err = result.RowsAffected()
		if err == nil && n != 1 {
			err = errors.New("too many pending CLI sign-ins")
		}
	}
	return
}

func (s *mcpOAuthStore) CLIDeviceRequest(ctx context.Context, verificationCode string) ([]string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT scopes FROM cli_devices WHERE verification_hash=? AND status='pending' AND expires_at>?`, mcpOAuthHash(verificationCode), time.Now().Unix()).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var scopes []string
	err = json.Unmarshal([]byte(raw), &scopes)
	return scopes, err
}

func (s *mcpOAuthStore) DecideCLIDevice(ctx context.Context, verificationCode string, user *UserClaims, approve bool) error {
	status := "denied"
	if approve {
		status = "approved"
	}
	requested, err := s.CLIDeviceRequest(ctx, verificationCode)
	if err != nil {
		return err
	}
	scopes, err := json.Marshal(mcpOAuthScopesFor(user, requested))
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE cli_devices SET status=?,user_id=?,username=?,email=?,provider=?,scopes=? WHERE verification_hash=? AND status='pending' AND expires_at>?`, status, user.UserID, user.Username, user.Email, user.Provider, string(scopes), mcpOAuthHash(verificationCode), time.Now().Unix())
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

func (s *mcpOAuthStore) PollCLIDevice(ctx context.Context, raw, resource string) (mcpOAuthGrant, string, string, error) {
	var grant mcpOAuthGrant
	var status, scopes string
	var expiry int64
	var polled sql.NullInt64
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return grant, "", "", err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT scopes,status,expires_at,polled_at,user_id,username,email,provider FROM cli_devices WHERE hash=?`, mcpOAuthHash(raw)).Scan(&scopes, &status, &expiry, &polled, &grant.UserID, &grant.Username, &grant.Email, &grant.Provider)
	if err != nil {
		return grant, "", "", err
	}
	if expiry <= time.Now().Unix() {
		return grant, "", "", sql.ErrNoRows
	}
	if status == "denied" {
		return grant, "", "", errCLIOAuthDenied
	}
	if status == "pending" {
		now := time.Now().Unix()
		if polled.Valid && now-polled.Int64 < 2 {
			return grant, "", "", errCLIOAuthSlowDown
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cli_devices SET polled_at=? WHERE hash=?`, now, mcpOAuthHash(raw)); err != nil {
			return grant, "", "", err
		}
		if err := tx.Commit(); err != nil {
			return grant, "", "", err
		}
		return grant, "", "", errCLIOAuthPending
	}
	if status != "approved" || grant.UserID == "" {
		return grant, "", "", sql.ErrNoRows
	}
	if err := json.Unmarshal([]byte(scopes), &grant.Scopes); err != nil {
		return grant, "", "", err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM cli_devices WHERE hash=? AND status='approved'`, mcpOAuthHash(raw))
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
	grant.ClientID = cliOAuthClientID
	grant.Resource = resource
	access, refresh, err := issueMCPOAuthPair(ctx, tx, &grant)
	if err != nil {
		return grant, "", "", err
	}
	return grant, access, refresh, tx.Commit()
}

func (s *mcpOAuthStore) RevokeByRefresh(ctx context.Context, raw string) error {
	if !strings.HasPrefix(raw, cliOAuthRefreshPrefix) {
		return sql.ErrNoRows
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at=COALESCE(revoked_at,?) WHERE family_id=(SELECT family_id FROM tokens WHERE hash=? AND kind='refresh' AND client_id=?) AND revoked_at IS NULL`, time.Now().Unix(), mcpOAuthHash(raw), cliOAuthClientID)
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
