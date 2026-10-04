package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Named OAuth client registry for Gmail connections.
//
// Historically every Gmail connection resolved its OAuth app credentials from
// one shared file (~/.config/gws/client_secret.json) or one pair of env vars
// (see gmailOAuthConfig's fallback in gmail_oauth.go). Because an OAuth
// refresh token is bound to the client_id that issued it, replacing that
// single file to onboard a new mailbox silently invalidated every other
// connection's token on the box at once. This registry gives each OAuth app
// registration its own required name and its own file, so a second upload
// can never clobber the first without an explicit, deliberate replace.

// GmailOAuthClient is one named Google Cloud OAuth app registration.
type GmailOAuthClient struct {
	// Name is the required, operator-chosen identifier. Never guessed or
	// defaulted — the whole point is that the operator decides whether a new
	// upload is a new client or an explicit replacement of an existing one.
	Name string `json:"name"`
	// ClientID is informational only, shown in the UI so an operator can tell
	// clients apart without re-opening the file. Never the secret.
	ClientID  string    `json:"client_id,omitempty"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

var gmailOAuthClientNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidateGmailOAuthClientName reports whether name is an acceptable,
// slug-safe client name (it becomes a directory name on disk).
func ValidateGmailOAuthClientName(name string) error {
	if !gmailOAuthClientNamePattern.MatchString(name) {
		return fmt.Errorf("client name must be lowercase letters, numbers, and hyphens (not leading/trailing), e.g. %q", "primary")
	}
	return nil
}

// gmailOAuthClientsBaseDir is where named client registrations live, one
// subdirectory per name — host-level, never the workspace.
//
// A client_secret.json is a real credential, shared across every account
// authorized through it — the exact same reasoning gmailOAuthTokenDir
// (gmail_oauth.go) already applies to refresh tokens: workspace content is
// readable by anything that can read the workspace (an agent running a
// workflow, a workspace backup), so a secret placed there is a standing
// exposure. Follows the identical env-override / XDG_CONFIG_HOME / ~/.config
// resolution as gmailOAuthTokenDir, so both land in the same place on a host
// whose ~/.config is not writable by the service user (RTS: root-owned).
func gmailOAuthClientsBaseDir() string {
	if v := strings.TrimSpace(os.Getenv("GMAIL_OAUTH_CLIENTS_DIR")); v != "" {
		return v
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, "agentworks", "gmail-oauth-clients")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "agentworks-gmail-oauth-clients")
	}
	return filepath.Join(home, ".config", "agentworks", "gmail-oauth-clients")
}

func gmailOAuthClientDir(name string) string {
	return filepath.Join(gmailOAuthClientsBaseDir(), name)
}

func gmailOAuthClientSecretPath(name string) string {
	return filepath.Join(gmailOAuthClientDir(name), "client_secret.json")
}

func gmailOAuthClientMetaPath(name string) string {
	return filepath.Join(gmailOAuthClientDir(name), "meta.json")
}

// parseGmailClientSecretJSON extracts the client id/secret from a Desktop-app
// (or web) OAuth client JSON — the exact shape both gws and gog accept.
func parseGmailClientSecretJSON(raw []byte) (clientID, clientSecret string, err error) {
	var parsed map[string]struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("parse client_secret.json: %w", err)
	}
	for _, key := range []string{"installed", "web"} {
		if entry, ok := parsed[key]; ok {
			if strings.TrimSpace(entry.ClientID) == "" || strings.TrimSpace(entry.ClientSecret) == "" {
				return "", "", fmt.Errorf("client_secret.json's %q entry is missing client_id or client_secret", key)
			}
			return entry.ClientID, entry.ClientSecret, nil
		}
	}
	return "", "", fmt.Errorf("client_secret.json has neither an \"installed\" nor a \"web\" client")
}

func loadGmailOAuthClientMeta(name string) (GmailOAuthClient, bool) {
	raw, err := os.ReadFile(gmailOAuthClientMetaPath(name))
	if err != nil {
		return GmailOAuthClient{}, false
	}
	var client GmailOAuthClient
	if err := json.Unmarshal(raw, &client); err != nil {
		return GmailOAuthClient{}, false
	}
	return client, true
}

func saveGmailOAuthClientMeta(client GmailOAuthClient) error {
	blob, err := json.MarshalIndent(client, "", "  ")
	if err != nil {
		return fmt.Errorf("gmail oauth client: marshal metadata: %w", err)
	}
	return os.WriteFile(gmailOAuthClientMetaPath(client.Name), blob, 0o600)
}

// CreateOAuthClient registers a new named OAuth client from an uploaded
// Web-app client_secret.json. Refuses to overwrite an existing name
// unless replace is true — this is the actual fix for the shared-file
// overwrite bug: a second upload needs a new name, or an explicit,
// deliberate replace with full knowledge that every connection currently
// using that name will need to reconnect.
func CreateOAuthClient(ctx context.Context, name string, secretJSON []byte, replace bool) (GmailOAuthClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return GmailOAuthClient{}, fmt.Errorf("gmail oauth client: name is required")
	}
	if err := ValidateGmailOAuthClientName(name); err != nil {
		return GmailOAuthClient{}, err
	}
	clientID, _, err := parseGmailClientSecretJSON(secretJSON)
	if err != nil {
		return GmailOAuthClient{}, err
	}

	existing, hadExisting := loadGmailOAuthClientMeta(name)
	if (hadExisting || OAuthClientExists(name)) && !replace {
		return GmailOAuthClient{}, fmt.Errorf(
			"an OAuth client named %q already exists — choose a new name, or explicitly replace it (this invalidates every connection currently using it)",
			name,
		)
	}

	dir := gmailOAuthClientDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return GmailOAuthClient{}, fmt.Errorf("gmail oauth client: create directory: %w", err)
	}
	if err := storeGogClient(ctx, gogHomeDir(), name, secretJSON); err != nil {
		return GmailOAuthClient{}, err
	}

	now := time.Now().UTC()
	var projectMetadata map[string]struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(secretJSON, &projectMetadata)
	projectID := projectMetadata["web"].ProjectID
	if projectID == "" {
		projectID = projectMetadata["installed"].ProjectID
	}
	client := GmailOAuthClient{
		ProjectID: projectID,
		Name:      name,
		ClientID:  clientID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if hadExisting {
		client.CreatedAt = existing.CreatedAt
	}
	if err := saveGmailOAuthClientMeta(client); err != nil {
		return GmailOAuthClient{}, err
	}
	_ = ctx // reserved for a future audit-log hook; unused today
	return client, nil
}

// ListOAuthClients returns every registered named client, sorted by name.
func ListOAuthClients() ([]GmailOAuthClient, error) {
	base := gmailOAuthClientsBaseDir()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("gmail oauth clients: list %s: %w", base, err)
	}
	out := make([]GmailOAuthClient, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if client, ok := loadGmailOAuthClientMeta(e.Name()); ok {
			out = append(out, client)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// OAuthClientExists reports whether a named client is registered, without
// reading its secret — used to validate a connection's client_name at
// create/update time.
func OAuthClientExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if path, err := gogClientCredentialsPath(name); err == nil {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	} else {
		return false
	}
	_, err := os.Stat(gmailOAuthClientSecretPath(name))
	return err == nil
}

// GetOAuthClientSecret returns the client id/secret for a named client, for
// building an oauth2.Config. Errors if the name is unknown.
func GetOAuthClientSecret(name string) (clientID, clientSecret string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("gmail oauth client: name is required")
	}
	path, err := gogClientCredentialsPath(name)
	if err != nil {
		return "", "", err
	}
	if raw, err := os.ReadFile(path); err == nil {
		var stored struct {
			ID     string `json:"client_id"`
			Secret string `json:"client_secret"`
		}
		if err := json.Unmarshal(raw, &stored); err != nil {
			return "", "", err
		}
		if stored.ID == "" || stored.Secret == "" {
			return "", "", fmt.Errorf("gog client %q has incomplete credentials", name)
		}
		return stored.ID, stored.Secret, nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	// Read-only compatibility for clients not yet migrated.
	raw, err := os.ReadFile(gmailOAuthClientSecretPath(name))
	if err != nil {
		return "", "", fmt.Errorf("gmail oauth client %q not found: %w", name, err)
	}
	return parseGmailClientSecretJSON(raw)
}

// DeleteOAuthClient removes a named client's stored credentials. Does not
// touch any connection that references it — a deleted client leaves those
// connections needing reconnect against a different (or recreated) client,
// same as any other broken credential.
func DeleteOAuthClient(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("gmail oauth client: name is required")
	}
	dir := gmailOAuthClientDir(name)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("gmail oauth client %q not found", name)
		}
		return err
	}
	path, err := gogClientCredentialsPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	return os.RemoveAll(dir)
}

// ImportLegacyOAuthClient reads the box's existing shared client credentials
// (the pre-registry ~/.config/gws/client_secret.json, or
// GOOGLE_WORKSPACE_CLI_CLIENT_ID/_SECRET / GMAIL_CLIENT_SECRET_FILE) and
// registers them as a named client, then backfills ClientName on every
// existing connection that does not already have one set.
//
// A one-time, operator-triggered migration: naming the client is the
// operator's decision, so this never runs automatically on server start.
func (g *GmailService) ImportLegacyOAuthClient(ctx context.Context, name string) (GmailOAuthClient, int, error) {
	clientID, clientSecret, err := readGmailClientSecretFile()
	if err != nil {
		return GmailOAuthClient{}, 0, fmt.Errorf("no legacy client credentials found to import: %w", err)
	}
	autoNamed := strings.TrimSpace(name) == ""
	if autoNamed {
		// A reconnect must never enter OAuth with a legacy client and then fail
		// at the callback because gog requires a named client. Derive a stable,
		// non-secret name from the OAuth client id so every connection using the
		// same legacy registration converges on one registry entry.
		sum := sha256.Sum256([]byte(clientID))
		name = fmt.Sprintf("legacy-%x", sum[:6])
	}
	secretJSON, err := json.Marshal(map[string]any{
		"installed": map[string]string{
			"client_id":     clientID,
			"client_secret": clientSecret,
		},
	})
	if err != nil {
		return GmailOAuthClient{}, 0, fmt.Errorf("gmail oauth client: encode legacy credentials: %w", err)
	}
	var client GmailOAuthClient
	if autoNamed && OAuthClientExists(name) {
		existing, ok := loadGmailOAuthClientMeta(name)
		if !ok || existing.ClientID != clientID {
			return GmailOAuthClient{}, 0, fmt.Errorf("derived legacy OAuth client %q conflicts with an existing registration", name)
		}
		client = existing
	} else {
		client, err = CreateOAuthClient(ctx, name, secretJSON, false)
		if err != nil {
			return GmailOAuthClient{}, 0, err
		}
	}

	cfg := g.GetConfig()
	backfilled := 0
	for i := range cfg.Connections {
		if strings.TrimSpace(cfg.Connections[i].ClientName) == "" {
			cfg.Connections[i].ClientName = client.Name
			cfg.Connections[i].UpdatedAt = time.Now().UTC()
			backfilled++
		}
	}
	if backfilled > 0 {
		if err := g.SaveConfig(ctx, cfg); err != nil {
			return client, 0, fmt.Errorf("registered client %q but failed to backfill connections: %w", client.Name, err)
		}
	}
	return client, backfilled, nil
}

// EnsureConnectionOAuthClient migrates a pre-registry Gmail connection onto
// the named gog client store before OAuth begins. New connections are already
// named and pass through unchanged.
func (g *GmailService) EnsureConnectionOAuthClient(ctx context.Context, connectionID string) (GmailConnection, error) {
	conn, ok := g.GetConnection(strings.TrimSpace(connectionID))
	if !ok {
		return GmailConnection{}, fmt.Errorf("gmail connection %q not found", connectionID)
	}
	if strings.TrimSpace(conn.ClientName) != "" {
		return conn, nil
	}
	if _, _, err := g.ImportLegacyOAuthClient(ctx, ""); err != nil {
		return GmailConnection{}, fmt.Errorf("migrate legacy Gmail OAuth client before reconnect: %w", err)
	}
	conn, ok = g.GetConnection(strings.TrimSpace(connectionID))
	if !ok || strings.TrimSpace(conn.ClientName) == "" {
		return GmailConnection{}, fmt.Errorf("legacy Gmail connection was not assigned an OAuth client")
	}
	return conn, nil
}
