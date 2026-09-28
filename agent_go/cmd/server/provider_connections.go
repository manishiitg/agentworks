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
)

const providerConnectionsPath = "config/provider-connections.json"

var providerConnectionsMu sync.Mutex

// ProviderConnection is safe public metadata. Credentials are stored only in
// the encrypted registry and resolved for an authorized execution principal.
type ProviderConnection struct {
	ID                      string    `json:"id"`
	Provider                string    `json:"provider"`
	DisplayName             string    `json:"display_name"`
	OwnerUserID             string    `json:"owner_user_id,omitempty"`
	Scope                   string    `json:"scope"`
	AuthMethod              string    `json:"auth_method"`
	UnderlyingProvider      string    `json:"underlying_provider,omitempty"`
	PersonalAccountsAllowed *bool     `json:"personal_accounts_allowed,omitempty"`
	UpdatedAt               time.Time `json:"updated_at"`
	// Sharing is who besides the owner may use a user account. Only the
	// owner and admins see it.
	Sharing *ProviderConnectionSharing `json:"sharing,omitempty"`
}
type storedProviderConnection struct {
	ProviderConnection
	Credential string `json:"credential"`
}

func loadProviderConnections(ctx context.Context) ([]storedProviderConnection, error) {
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
	return writeFileToWorkspace(ctx, providerConnectionsPath, base64.StdEncoding.EncodeToString(ciphertext))
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
func providerConnectionSetupEnvironment(keys *llm.ProviderAPIKeys) []string {
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
