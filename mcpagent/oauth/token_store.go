package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// TokenSealer encrypts token files at rest. A host registers one with
// SetTokenSealer; it applies only to the paths it claims (Handles), so every
// other token file keeps its plaintext format.
type TokenSealer interface {
	Handles(path string) bool
	Seal(path string, plaintext []byte) ([]byte, error)
	Open(path string, sealed []byte) ([]byte, error)
}

var (
	tokenSealerMu sync.RWMutex
	tokenSealer   TokenSealer
)

// SetTokenSealer installs the process-wide token sealer (nil removes it).
func SetTokenSealer(sealer TokenSealer) {
	tokenSealerMu.Lock()
	defer tokenSealerMu.Unlock()
	tokenSealer = sealer
}

func sealerFor(path string) TokenSealer {
	tokenSealerMu.RLock()
	defer tokenSealerMu.RUnlock()
	if tokenSealer != nil && tokenSealer.Handles(path) {
		return tokenSealer
	}
	return nil
}

// ReadTokenFile reads a token file, opening it when a sealer claims the path.
// Callers outside this package that inspect a token file use this, never
// os.ReadFile, so a sealed file is never parsed as JSON.
func ReadTokenFile(path string) ([]byte, error) {
	path = ExpandTokenPath(path)
	data, err := os.ReadFile(path) //nolint:gosec // G304: token paths come from server configuration
	if err != nil {
		return nil, err
	}
	if sealer := sealerFor(path); sealer != nil {
		return sealer.Open(path, data)
	}
	return data, nil
}

// WriteTokenFile writes a credential file (a token, a client registration)
// owner-only, sealing it when a sealer claims the path.
func WriteTokenFile(path string, data []byte) error {
	path = ExpandTokenPath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if sealer := sealerFor(path); sealer != nil {
		sealed, err := sealer.Seal(path, data)
		if err != nil {
			return err
		}
		data = sealed
	}
	return os.WriteFile(path, data, 0o600)
}

// TokenStore manages persistent storage of OAuth tokens
type TokenStore struct {
	filePath string
	mu       sync.RWMutex
}

// NewTokenStore creates a new token store for the given file path
func NewTokenStore(filePath string) *TokenStore {
	filePath = ExpandTokenPath(filePath)

	return &TokenStore{
		filePath: filePath,
	}
}

// Save saves a token to the file with secure permissions (0600)
func (ts *TokenStore) Save(token *oauth2.Token) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(ts.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create token directory: %w", err)
	}

	// Marshal token to JSON
	data, err := json.MarshalIndent(token, "", "  ") //nolint:gosec // persisting the token to its own 0600 file is this store's purpose
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	if sealer := sealerFor(ts.filePath); sealer != nil {
		if data, err = sealer.Seal(ts.filePath, data); err != nil {
			return fmt.Errorf("failed to seal token: %w", err)
		}
	}

	// Write with secure permissions (owner read/write only)
	//nolint:gosec // G306: We intentionally set 0600 permissions for security
	if err := os.WriteFile(ts.filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}

	return nil
}

// Load loads a token from the file
func (ts *TokenStore) Load() (*oauth2.Token, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// Check if file exists
	if _, err := os.Stat(ts.filePath); os.IsNotExist(err) {
		return nil, ErrTokenFileNotFound
	}

	// Read file (opened when a sealer claims the path)
	data, err := ReadTokenFile(ts.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	// Unmarshal token
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidTokenFile, err)
	}

	return &token, nil
}

// Delete removes the token file
func (ts *TokenStore) Delete() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if err := os.Remove(ts.filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete token file: %w", err)
	}

	return nil
}

// IsValid checks if a stored token exists and is valid (not expired)
func (ts *TokenStore) IsValid() bool {
	token, err := ts.Load()
	if err != nil {
		return false
	}

	return token.Valid()
}

// ExpiresIn returns the duration until token expiry, or 0 if expired/invalid
// Returns a special large duration for tokens with no expiry (zero time)
func (ts *TokenStore) ExpiresIn() time.Duration {
	token, err := ts.Load()
	if err != nil {
		return 0
	}

	if !token.Valid() {
		return 0
	}

	// Check for zero expiry time (means token never expires)
	// Go's zero time is "0001-01-01 00:00:00 +0000 UTC"
	if token.Expiry.IsZero() {
		// Return a large duration indicating "never expires"
		// Using 100 years as a symbolic "forever" value
		return 100 * 365 * 24 * time.Hour
	}

	return time.Until(token.Expiry)
}

// GetFilePath returns the token file path
func (ts *TokenStore) GetFilePath() string {
	return ts.filePath
}

// ExpandTokenPath resolves the "~/" token-file convention. "~/.config/..."
// honours XDG_CONFIG_HOME so a host whose ~/.config is not writable by the
// service user (the rootless EC2 deployment: root-owned, the bootstrap wrote
// the systemd units there) can redirect per-user token storage to a writable
// directory -- the same knob cursor-agent and the agent server use. Every
// reader and writer of a token file must go through this, or the server
// writes a token the runtime never finds.
func ExpandTokenPath(filePath string) string {
	if strings.HasPrefix(filePath, "~/.config/") {
		if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
			return filepath.Join(xdg, strings.TrimPrefix(filePath, "~/.config/"))
		}
	}
	if strings.HasPrefix(filePath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, filePath[2:])
		}
	}
	return filePath
}

// ReadClientSecretFile returns the client_secret held in a credential file
// (OAuthConfig.ClientSecretFile), unsealing it when a sealer claims the path.
func ReadClientSecretFile(path string) (string, error) {
	data, err := ReadTokenFile(path)
	if err != nil {
		return "", err
	}
	var record struct {
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("unreadable client credential file")
	}
	return record.ClientSecret, nil
}
