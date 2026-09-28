package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gog is the credential authority for new connections. The legacy stores are
// retained for un-migrated connections; account settings and client metadata stay here.
func gogClientCredentialsPath(name string) (string, error) {
	if err := ValidateGmailOAuthClientName(name); err != nil {
		return "", err
	}
	return filepath.Join(gogHomeDir(), "data", "credentials-"+name+".json"), nil
}

var storeGogClient = writeGogClient

func writeGogClient(ctx context.Context, home, name string, secretJSON []byte) error {
	if err := ValidateGmailOAuthClientName(name); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "agentworks-gog-client-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "client.json")
	if err := os.WriteFile(path, secretJSON, 0600); err != nil {
		return err
	}
	args := gogBaseArgs(home, nil)
	args = append(args, "auth", "credentials", "set", path, "--client", name, "--insecure", "--no-input", "--force")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Never echo a credential command's output; some CLI failures include input.
	if err := exec.CommandContext(ctx, "gog", args...).Run(); err != nil {
		return fmt.Errorf("store OAuth client in gog: %w", err)
	}
	return nil
}

type gogStoredAccount struct {
	Email  string   `json:"email"`
	Client string   `json:"client"`
	Scopes []string `json:"scopes"`
	Valid  bool     `json:"valid"`
}

func checkedGogAccount(ctx context.Context, binary, home, email, client string) (gogStoredAccount, error) {
	if binary == "" {
		binary = "gog"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := gogBaseArgs(home, nil)
	args = append(args, "--client", client, "auth", "list", "--check", "--timeout=15s", "--json", "--no-input")
	out, err := exec.CommandContext(ctx, binary, args...).Output()
	if err != nil {
		return gogStoredAccount{}, fmt.Errorf("verify gog account: %w", err)
	}
	var result struct {
		Accounts []gogStoredAccount `json:"accounts"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return gogStoredAccount{}, fmt.Errorf("decode gog account status: %w", err)
	}
	for _, account := range result.Accounts {
		if strings.EqualFold(account.Email, email) && account.Client == client {
			if !account.Valid {
				return gogStoredAccount{}, fmt.Errorf("gog authorization for %s needs reconnecting", email)
			}
			return account, nil
		}
	}
	return gogStoredAccount{}, fmt.Errorf("gog has no authorization for %s with client %s", email, client)
}

// CompleteGogConnection records a successfully imported connection. There is
// no refresh token in this registry; all future use goes through gog.
// grantedScopes are the consent-time tokeninfo scopes from CompleteGmailOAuth;
// they are authoritative for what this consent granted, since gog keeps no
// scope metadata for imported accounts.
func (g *GmailService) CompleteGogConnection(ctx context.Context, id, email string, grantedScopes []string) error {
	cfg := g.GetConfig()
	for i := range cfg.Connections {
		conn := &cfg.Connections[i]
		if conn.ID != id {
			continue
		}
		account, err := checkedGogAccount(ctx, cfg.GogPath, gogHomeForConnection(*conn), email, conn.ClientName)
		if err != nil {
			return err
		}
		conn.Email, conn.AuthBackend = account.Email, "gog"
		conn.Scopes = resolveCompletionScopes(grantedScopes, account.Scopes, conn.Scopes)
		conn.Status = GmailConnectionConnected
		conn.Enabled = true
		conn.UpdatedAt = time.Now().UTC()
		if err := g.SaveConfig(ctx, cfg); err != nil {
			return err
		}
		deleteGmailOAuthToken(id)
		g.InvalidateConnectionAuthCache(id)
		return nil
	}
	return fmt.Errorf("Google connection %q no longer exists", id)
}

// resolveCompletionScopes selects the scopes recorded on a connection at
// OAuth completion. Consent-time tokeninfo scopes win — they describe exactly
// what this consent granted; gog's stored metadata is the fallback for
// accounts that carry it. When both are empty the previously stored scopes
// are kept: gog reports no scope metadata for imported accounts, and empty
// means "unknown", never "revoked". Mirrors syncConnectionScopes' rule of
// never persisting an empty observation over a known one.
func resolveCompletionScopes(granted, account, stored []string) []string {
	switch {
	case len(granted) > 0:
		return append([]string(nil), granted...)
	case len(account) > 0:
		return append([]string(nil), account...)
	default:
		return stored
	}
}

// VerifyGmailConfigGog checks the existing gog store without importing or
// changing credentials. Used for pre-deployment verification.
func VerifyGmailConfigGog(ctx context.Context, cfg *GmailConfig) error {
	if cfg == nil || len(cfg.Connections) == 0 {
		return fmt.Errorf("no named Google connections to verify; configure the connection registry first")
	}
	for _, conn := range cfg.Connections {
		if conn.Email == "" || conn.ClientName == "" {
			return fmt.Errorf("connection %s is missing its email or client", conn.ID)
		}
		if _, err := checkedGogAccount(ctx, cfg.GogPath, gogHomeForConnection(conn), conn.Email, conn.ClientName); err != nil {
			return err
		}
	}
	return nil
}

// MigrateGmailConfigToGog verifies every connection before switching the
// registry. It never deletes legacy credentials; callers first persist the
// returned config, then may retire the old files. A failed migration leaves
// the original config usable, even if an earlier token was imported into gog.
func MigrateGmailConfigToGog(ctx context.Context, cfg *GmailConfig) (*GmailConfig, error) {
	if cfg == nil || len(cfg.Connections) == 0 {
		return nil, fmt.Errorf("no named Google connections to migrate; configure the connection registry first")
	}
	copy := *cfg
	copy.Connections = append([]GmailConnection(nil), cfg.Connections...)
	for i := range copy.Connections {
		conn := &copy.Connections[i]
		if conn.Email == "" || conn.ClientName == "" {
			return nil, fmt.Errorf("connection %s needs an email and named OAuth client before migration", conn.ID)
		}
		account, err := checkedGogAccount(ctx, copy.GogPath, gogHomeForConnection(*conn), conn.Email, conn.ClientName)
		if err != nil {
			token, ok := loadGmailOAuthToken(conn.ID)
			if !ok || token.RefreshToken == "" {
				return nil, err
			}
			if err := ImportRefreshTokenIntoGog(ctx, gogHomeForConnection(*conn), conn.Email, conn.ClientName, token.RefreshToken); err != nil {
				return nil, err
			}
			account, err = checkedGogAccount(ctx, copy.GogPath, gogHomeForConnection(*conn), conn.Email, conn.ClientName)
			if err != nil {
				return nil, err
			}
		}
		conn.AuthBackend = "gog"
		// gog commonly reports an imported account as valid with no scope
		// metadata. Empty means unknown, not revoked; preserve the last known
		// scopes during migration just as OAuth completion does.
		conn.Scopes = resolveCompletionScopes(nil, account.Scopes, conn.Scopes)
		conn.Status = GmailConnectionConnected
		conn.UpdatedAt = time.Now().UTC()
	}
	copy.UseGogBackend = true
	copy.Token, copy.CredentialsFile, copy.ConfigHome = "", "", ""
	return &copy, nil
}

// RetireLegacyGmailCredentials must only be called after the migrated config
// is durably saved. Remove only proven duplicates, never an unrelated client.
func RetireLegacyGmailCredentials(cfg *GmailConfig) error {
	for _, conn := range cfg.Connections {
		if conn.AuthBackend != "gog" {
			continue
		}
		path, err := gogClientCredentialsPath(conn.ClientName)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var stored struct {
			ID     string `json:"client_id"`
			Secret string `json:"client_secret"`
		}
		if err := json.Unmarshal(raw, &stored); err != nil {
			return err
		}
		legacyClientInUse := false
		for _, other := range cfg.Connections {
			if other.ClientName == conn.ClientName && other.AuthBackend != "gog" {
				legacyClientInUse = true
			}
		}
		if legacyClientInUse {
			deleteGmailOAuthToken(conn.ID)
			continue
		}
		legacyPath := gmailOAuthClientSecretPath(conn.ClientName)
		if legacy, err := os.ReadFile(legacyPath); err == nil {
			id, secret, err := parseGmailClientSecretJSON(legacy)
			if err != nil || id != stored.ID || !bytes.Equal([]byte(secret), []byte(stored.Secret)) {
				return fmt.Errorf("legacy client %s differs from gog; retained", conn.ClientName)
			}
			if err := os.Remove(legacyPath); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(gmailOAuthTokenPath(conn.ID)); err != nil && !os.IsNotExist(err) {
			return err
		}
		deleteGmailOAuthToken(conn.ID) // also clears in-memory legacy refresh state
	}
	return nil
}

// Keep existing gws connections usable while a deployment adds gog accounts
// (and vice versa). One missing CLI must not disable the entire mixed channel.
func gmailConfiguredBinaryAvailable(cfg *GmailConfig, gwsPath, gogPath string) bool {
	paths := []string{}
	for _, conn := range cfg.Connections {
		if !conn.Enabled {
			continue
		}
		if cfg.UseGogBackend || conn.AuthBackend == "gog" {
			paths = append(paths, gogPath)
		} else {
			paths = append(paths, gwsPath)
		}
	}
	if len(paths) == 0 {
		if cfg.UseGogBackend {
			paths = append(paths, gogPath)
		} else {
			paths = append(paths, gwsPath)
		}
	}
	for _, path := range paths {
		if _, err := exec.LookPath(path); err == nil {
			return true
		}
	}
	return false
}
