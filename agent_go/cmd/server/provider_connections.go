package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
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

func (api *StreamingAPI) connectionAPIKeys(ctx context.Context, userID, provider, id string) (*llm.ProviderAPIKeys, error) {
	enabled := false
	for _, candidate := range getSupportedProviders() {
		if candidate == provider {
			enabled = true
		}
	}
	if !enabled {
		return nil, fmt.Errorf("provider is not enabled")
	}
	if strings.HasPrefix(id, "global:") {
		if id != "global:"+provider {
			return nil, fmt.Errorf("provider connection does not match selected provider")
		}
		return MergedProviderAPIKeys(ctx), nil
	}
	if userID == "" {
		return nil, fmt.Errorf("provider connection requires an execution principal")
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record.ID != id {
			continue
		}
		if record.OwnerUserID != userID || record.Provider != provider {
			return nil, fmt.Errorf("provider connection is unavailable or unauthorized")
		}
		if personalProviderConnectionsLocked(provider) {
			return nil, fmt.Errorf("personal provider connections are locked by administrator")
		}
		keys, err := connectionCredentialKeys(record)
		if err != nil {
			return nil, err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		accountHome := filepath.Join(home, ".local", "state", "agentworks", "provider-connections", record.ID, "home")
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
	return nil, fmt.Errorf("provider connection is unavailable or unauthorized")
}

func (api *StreamingAPI) withConnectionResolver(keys *llm.ProviderAPIKeys, userID string) *llm.ProviderAPIKeys {
	keys = keys.Clone()
	if keys == nil {
		keys = &llm.ProviderAPIKeys{}
	}
	keys.ResolveConnection = func(ctx context.Context, provider llm.Provider, id string) (*llm.ProviderAPIKeys, error) {
		return api.connectionAPIKeys(ctx, userID, string(provider), id)
	}
	return keys
}

func (api *StreamingAPI) handleProviderConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID := GetUserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		http.Error(w, "cannot load provider connections", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		connections := []ProviderConnection{}
		for _, provider := range getSupportedProviders() {
			if _, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: provider, UnderlyingProvider: "placeholder"}, Credential: "placeholder"}); err != nil {
				continue
			}
			allowed := !personalProviderConnectionsLocked(provider)
			connections = append(connections, ProviderConnection{PersonalAccountsAllowed: &allowed, ID: "global:" + provider, Provider: provider, DisplayName: "Server account", Scope: "global", AuthMethod: "server"})
		}
		for _, record := range records {
			if record.OwnerUserID == userID {
				connections = append(connections, record.ProviderConnection)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connections": connections})
	case http.MethodPost:
		var request struct {
			Provider           string `json:"provider"`
			DisplayName        string `json:"display_name"`
			Credential         string `json:"credential"`
			UnderlyingProvider string `json:"underlying_provider"`
			AuthMethod         string `json:"auth_method"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&request); err != nil {
			http.Error(w, "invalid connection request", http.StatusBadRequest)
			return
		}
		request.DisplayName = strings.TrimSpace(request.DisplayName)
		if request.DisplayName == "" || len(request.DisplayName) > 120 {
			http.Error(w, "connection name is required (maximum 120 characters)", http.StatusBadRequest)
			return
		}
		if personalProviderConnectionsLocked(request.Provider) {
			http.Error(w, "personal provider connections are locked by administrator", http.StatusForbidden)
			return
		}
		supported := false
		for _, provider := range getSupportedProviders() {
			if provider == request.Provider {
				supported = true
			}
		}
		if !supported {
			http.Error(w, "provider is not enabled", http.StatusBadRequest)
			return
		}
		record := storedProviderConnection{ProviderConnection: ProviderConnection{ID: uuid.NewString(), Provider: request.Provider, DisplayName: request.DisplayName, OwnerUserID: userID, Scope: "user", AuthMethod: "api_key", UnderlyingProvider: strings.TrimSpace(request.UnderlyingProvider), UpdatedAt: time.Now().UTC()}, Credential: strings.TrimSpace(request.Credential)}
		if request.AuthMethod == "cli_login" {
			record.AuthMethod = "cli_login"
		} else if request.Provider == "claude-code" {
			record.AuthMethod = "oauth_token"
		}
		if _, err := connectionCredentialKeys(record); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := saveProviderConnections(r.Context(), append(records, record)); err != nil {
			http.Error(w, "cannot save provider connection", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(record.ProviderConnection)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (api *StreamingAPI) handleProviderConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	userID := GetUserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "authentication required", 401)
		return
	}
	id := mux.Vars(r)["connectionID"]
	providerConnectionsMu.Lock()
	defer providerConnectionsMu.Unlock()
	records, err := loadProviderConnections(r.Context())
	if err != nil {
		http.Error(w, "cannot load connections", 500)
		return
	}
	for i := range records {
		record := &records[i]
		if record.ID != id || record.OwnerUserID != userID {
			continue
		}
		if r.Method != http.MethodDelete && personalProviderConnectionsLocked(record.Provider) {
			http.Error(w, "personal connections are locked", 403)
			return
		}
		if r.Method == http.MethodDelete {
			records = append(records[:i], records[i+1:]...)
		} else {
			var request struct {
				DisplayName string  `json:"display_name"`
				Credential  *string `json:"credential"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&request) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			name := strings.TrimSpace(request.DisplayName)
			if name == "" || len(name) > 120 {
				http.Error(w, "account name is required", 400)
				return
			}
			record.DisplayName = name
			if request.Credential != nil {
				record.Credential = strings.TrimSpace(*request.Credential)
				if _, err := connectionCredentialKeys(*record); err != nil {
					http.Error(w, "invalid credential", 400)
					return
				}
			}
			record.UpdatedAt = time.Now().UTC()
		}
		if saveProviderConnections(r.Context(), records) != nil {
			http.Error(w, "cannot save connections", 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "connection unavailable", 404)
}

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
