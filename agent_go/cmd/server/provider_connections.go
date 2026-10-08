package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

const (
	providerConnectionsPath       = "config/provider-connections.json"
	providerConnectionHistoryPath = "config/provider-connection-history.json"
)

var providerConnectionsMu sync.Mutex

// ProviderConnection is safe public metadata. Credentials are stored only in
// the encrypted registry and resolved for an authorized execution principal.
type ProviderConnection struct {
	ID                 string `json:"id"`
	Provider           string `json:"provider"`
	DisplayName        string `json:"display_name"`
	OwnerUserID        string `json:"owner_user_id,omitempty"`
	Scope              string `json:"scope"`
	AuthMethod         string `json:"auth_method"`
	UnderlyingProvider string `json:"underlying_provider,omitempty"`
	// BaseURL is the endpoint of a Pi account on a person's own
	// OpenAI-compatible service (UnderlyingProvider "openai-compatible").
	BaseURL                 string    `json:"base_url,omitempty"`
	PersonalAccountsAllowed *bool     `json:"personal_accounts_allowed,omitempty"`
	UpdatedAt               time.Time `json:"updated_at"`
	// AllowedModels limits the models that may run on this account; empty
	// means every model. The server account's list lives in
	// providerAccountSettings.AllowedModels and is shown here in its view.
	AllowedModels []string `json:"allowed_models,omitempty"`
	// Sharing is who besides the owner may use a user account. Only the
	// owner and admins see it.
	Sharing *ProviderConnectionSharing `json:"sharing,omitempty"`
}
type storedProviderConnection struct {
	ProviderConnection
	Credential string `json:"credential"`
	Removed    bool   `json:"-"`
}

// Keep only the metadata needed to attribute historical costs after an
// account's credential and runtime files have been deleted.
type providerConnectionHistory struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	DisplayName string `json:"display_name"`
	OwnerUserID string `json:"owner_user_id"`
}

func loadProviderConnectionHistory(ctx context.Context) ([]providerConnectionHistory, error) {
	raw, exists, err := readFileFromWorkspace(ctx, providerConnectionHistoryPath)
	if err != nil || !exists {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid provider connection history storage")
	}
	plain, err := decryptProviderKeys(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt provider connection history")
	}
	var records []providerConnectionHistory
	if err := json.Unmarshal(plain, &records); err != nil {
		return nil, fmt.Errorf("invalid provider connection history records")
	}
	return records, nil
}

func saveProviderConnectionHistory(ctx context.Context, records []providerConnectionHistory) error {
	plain, err := json.Marshal(records)
	if err != nil {
		return err
	}
	ciphertext, err := encryptProviderKeys(plain)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, providerConnectionHistoryPath, base64.StdEncoding.EncodeToString(ciphertext))
}

// The registry is cached in memory: this server is its only writer, so it is
// read from the workspace once (per workspace API) and every save through
// saveProviderConnections updates the cache. A turn therefore never waits on
// the workspace to admit its account.
var providerConnectionsCache struct {
	sync.Mutex
	url     string
	loaded  bool
	records []storedProviderConnection
}

func copyProviderConnections(records []storedProviderConnection) []storedProviderConnection {
	return append([]storedProviderConnection(nil), records...)
}

func loadProviderConnections(ctx context.Context) ([]storedProviderConnection, error) {
	url := getWorkspaceAPIURL()
	providerConnectionsCache.Lock()
	if providerConnectionsCache.loaded && providerConnectionsCache.url == url {
		records := copyProviderConnections(providerConnectionsCache.records)
		providerConnectionsCache.Unlock()
		return records, nil
	}
	providerConnectionsCache.Unlock()
	records, err := readProviderConnections(ctx)
	if err != nil {
		return nil, err
	}
	providerConnectionsCache.Lock()
	providerConnectionsCache.url, providerConnectionsCache.loaded, providerConnectionsCache.records = url, true, copyProviderConnections(records)
	providerConnectionsCache.Unlock()
	return records, nil
}

func readProviderConnections(ctx context.Context) ([]storedProviderConnection, error) {
	raw, exists, err := readFileFromWorkspace(ctx, providerConnectionsPath)
	if err != nil || !exists {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid provider connection storage")
	}
	plain, err := decryptProviderKeys(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt provider connections")
	}
	var records []storedProviderConnection
	if err := json.Unmarshal(plain, &records); err != nil {
		return nil, fmt.Errorf("invalid provider connection records")
	}
	return records, nil
}

func saveProviderConnections(ctx context.Context, records []storedProviderConnection) error {
	plain, err := json.Marshal(records)
	if err != nil {
		return err
	}
	ciphertext, err := encryptProviderKeys(plain)
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, providerConnectionsPath, base64.StdEncoding.EncodeToString(ciphertext)); err != nil {
		providerConnectionsCache.Lock()
		providerConnectionsCache.loaded = false
		providerConnectionsCache.Unlock()
		return err
	}
	providerConnectionsCache.Lock()
	providerConnectionsCache.url, providerConnectionsCache.loaded, providerConnectionsCache.records = getWorkspaceAPIURL(), true, copyProviderConnections(records)
	providerConnectionsCache.Unlock()
	return nil
}

func connectionCredentialKeys(record storedProviderConnection) (*llm.ProviderAPIKeys, error) {
	if record.AuthMethod == "cli_login" {
		// The CLI's own login, kept in the account's private HOME (and
		// CLAUDE_CONFIG_DIR / XDG dirs, see connectionAPIKeys). Empty keys
		// mean "use the stored login", never the server's.
		if !providerSupportsPrivateCLILogin(record.Provider) {
			return nil, fmt.Errorf("isolated browser login is unavailable for this provider; use a token or API key")
		}
		empty := ""
		return &llm.ProviderAPIKeys{ClaudeCodeOAuthToken: &empty, CodexCLI: &empty, CursorCLI: &empty, MuseCLI: &empty}, nil
	}
	if strings.TrimSpace(record.Credential) == "" {
		return nil, fmt.Errorf("provider connection needs authentication")
	}
	keys := &llm.ProviderAPIKeys{}
	switch record.Provider {
	case "claude-code":
		keys.ClaudeCodeOAuthToken = &record.Credential
	case "codex-cli":
		keys.CodexCLI = &record.Credential
	case "cursor-cli":
		keys.CursorCLI = &record.Credential
	case "muse-cli":
		keys.MuseCLI = &record.Credential
	case "pi-cli":
		if record.UnderlyingProvider == "" {
			return nil, fmt.Errorf("Pi connection requires an underlying provider")
		}
		keys.PiProviderKeys = map[string]string{record.UnderlyingProvider: record.Credential}
		if record.UnderlyingProvider == byokCustomProvider {
			// The account's own endpoint: Pi learns it from a staged
			// models.json that names the key only by its variable.
			models := make([]string, 0, len(record.AllowedModels))
			for _, model := range record.AllowedModels {
				models = append(models, strings.TrimPrefix(model, byokCustomProvider+"/"))
			}
			custom := &picli.PiCustomProvider{Name: byokCustomProvider, BaseURL: record.BaseURL, Models: models}
			if err := custom.Validate(); err != nil {
				return nil, fmt.Errorf("the OpenAI-compatible account needs its base URL and at least one model")
			}
			keys.PiCustomProvider = custom
		}
	default:
		return nil, fmt.Errorf("unsupported provider connection")
	}
	return keys, nil
}

// connectionAPIKeys admits account id for scope (see admitProviderAccount)
// and returns the credentials a run on it uses. It runs on every turn: a
// denied account errors and never falls back to another account.
func (api *StreamingAPI) connectionAPIKeys(ctx context.Context, scope providerAccountScope, provider, id string) (*llm.ProviderAPIKeys, error) {
	record, err := api.admitProviderAccount(ctx, scope, provider, id)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return MergedProviderAPIKeys(ctx), nil
	}
	return providerConnectionRuntimeKeys(*record)
}

// providerConnectionHome is a user account's own HOME. Its CLI logins live
// here, never in the service HOME.
func providerConnectionHome(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "agentworks", "provider-connections", id, "home"), nil
}

func providerConnectionRuntimeKeys(record storedProviderConnection) (*llm.ProviderAPIKeys, error) {
	keys, err := connectionCredentialKeys(record)
	if err != nil {
		return nil, err
	}
	accountHome, err := providerConnectionHome(record.ID)
	if err != nil {
		return nil, err
	}
	if err := checkPrivateProviderCredentialLinks(accountHome); err != nil {
		return nil, err
	}
	keys.RuntimeEnvironment = map[string]string{"HOME": accountHome, "XDG_CONFIG_HOME": filepath.Join(accountHome, ".config"), "XDG_DATA_HOME": filepath.Join(accountHome, ".local", "share"), "XDG_STATE_HOME": filepath.Join(accountHome, ".local", "state"), "CODEX_HOME": filepath.Join(accountHome, ".codex"), "CLAUDE_CONFIG_DIR": filepath.Join(accountHome, ".claude")}
	for _, dir := range keys.RuntimeEnvironment {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("cannot prepare provider connection storage")
		}
	}
	if record.Provider == "codex-cli" {
		configPath := filepath.Join(keys.RuntimeEnvironment["CODEX_HOME"], "config.toml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if err := os.WriteFile(configPath, []byte("cli_auth_credentials_store = \"file\"\n"), 0600); err != nil {
				return nil, fmt.Errorf("cannot configure Codex credential storage")
			}
		}
	}
	return keys, nil
}

// withConnectionResolver attaches the per-turn account resolver for scope.
// A model that names no account resolves to the server account through the
// same admission (llmguard.WithServerAccountAdmission) and keeps the keys
// the caller layered for this run.
func (api *StreamingAPI) withConnectionResolver(keys *llm.ProviderAPIKeys, scope providerAccountScope) *llm.ProviderAPIKeys {
	keys = keys.Clone()
	if keys == nil {
		keys = &llm.ProviderAPIKeys{}
	}
	keys.ResolveConnection = func(ctx context.Context, provider llm.Provider, id string) (*llm.ProviderAPIKeys, error) {
		if strings.HasPrefix(id, llmguard.ServerDefaultConnectionPrefix) {
			if _, err := api.admitProviderAccount(ctx, scope, string(provider), id); err != nil {
				return nil, err
			}
			return keys.Clone(), nil
		}
		return api.connectionAPIKeys(ctx, scope, string(provider), id)
	}
	return keys
}

// The HTTP handlers for provider accounts live in provider_account_routes.go.

// Remove ambient CLI credentials before applying this connection's identity.
func providerConnectionSetupEnvironment(label string, keys *llm.ProviderAPIKeys) []string {
	before := os.Environ()
	blocked := map[string]bool{"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "CLAUDE_CODE_OAUTH_TOKEN": true, "OPENAI_API_KEY": true, "CODEX_API_KEY": true, "CURSOR_API_KEY": true, "META_API_KEY": true, "PI_CODING_AGENT_DIR": true}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			env = append(env, entry)
		}
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithProviderAccountEnvironment(keys.RuntimeEnvironment)(opts)
	env = llmtypes.MergeCodingAgentSecretEnvironment(env, opts)
	for name, value := range map[string]*string{"CLAUDE_CODE_OAUTH_TOKEN": keys.ClaudeCodeOAuthToken, "CODEX_API_KEY": keys.CodexCLI, "CURSOR_API_KEY": keys.CursorCLI, "META_API_KEY": keys.MuseCLI} {
		if value != nil && *value != "" {
			env = append(env, name+"="+*value)
		}
	}
	logChildEnv(label, before, env)
	return env
}

// providerSupportsPrivateCLILogin lists the CLIs whose login lives under
// the HOME / XDG / CLAUDE_CONFIG_DIR a private account gets, so signing in
// for the account never touches the server's own login.
func providerSupportsPrivateCLILogin(provider string) bool {
	switch provider {
	case "claude-code", "codex-cli", "cursor-cli", "muse-cli":
		return true
	}
	return false
}

func canonicalProviderConnectionID(provider, id string) string {
	if id == "" {
		return "global:" + provider
	}
	return id
}

// Deployments can permit private accounts while keeping shared provider/model
// administration locked. Without this opt-in, existing lock behavior remains.
func personalProviderConnectionsLocked(provider string) bool {
	allow := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_PERSONAL_PROVIDER_CONNECTIONS")))
	return isProviderLocked(provider) && allow != "true" && allow != "1"
}

// ownDefaultProviderAccountID is the account a Crew or Code chat uses when it names none: the
// person's own account for this provider (the one they added themselves), newest first, skipping
// one known not to be signed in. Empty means the server account, as before. An account chosen
// explicitly in the project, the server account included ("global:<provider>"), always wins
// over this default; see resolveAgentProfileForQuery.
func ownDefaultProviderAccountID(ctx context.Context, userID, provider string) string {
	userID, provider = strings.TrimSpace(userID), strings.TrimSpace(provider)
	if userID == "" || provider == "" || personalProviderConnectionsLocked(provider) {
		return ""
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return ""
	}
	best := ""
	var bestAt time.Time
	for _, record := range records {
		if record.Removed || record.Scope != "user" || record.OwnerUserID != userID || record.Provider != provider {
			continue
		}
		if configured, ok := cachedProviderAccountConfigured(record.ID); ok && configured != nil && !*configured {
			continue
		}
		if best == "" || record.UpdatedAt.After(bestAt) {
			best, bestAt = record.ID, record.UpdatedAt
		}
	}
	return best
}

// Old setup confinement linked personal credential files to the server home.
// Never admit such a link, even if the registry marks the account private.
var privateProviderCredentialPaths = []string{
	".claude/.credentials.json", ".codex/auth.json", ".config/cursor/auth.json",
	".cursor/cli-config.json", ".config/muse/auth.json",
}

func checkPrivateProviderCredentialLinks(home string) error {
	for _, rel := range privateProviderCredentialPaths {
		path := filepath.Join(home, rel)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect private provider login")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("private provider login is linked to another account; reconnect this account")
		}
	}
	return nil
}

// Reauthentication starts with an independent file. Only remove the link,
// never copy or modify the account it previously pointed at.
func detachPrivateProviderCredentialLinks(home string) error {
	for _, rel := range privateProviderCredentialPaths {
		path := filepath.Join(home, rel)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect private provider login")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("cannot detach private provider login")
			}
		}
	}
	return nil
}
