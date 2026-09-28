package chathistory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	workspaceAPIBotConfigFile       = "config/bot-connectors.json"
	workspaceAPIBotConfigLegacyFile = "_system/bot_connectors.json"
)

type workspaceAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

// WorkspaceAPIStore persists operator state through the workspace API instead
// of writing directly to the host filesystem. This keeps workspace ownership
// inside the workspace service even when agent_go runs outside Docker.
type WorkspaceAPIStore struct {
	baseURL string
	client  *http.Client

	botCfgMu         sync.RWMutex
	botCfgs          map[string]*BotConnectorConfig
	botCfgFile       string
	botCfgLegacyFile string

	secretsMu                     sync.Map // userID -> *sync.Mutex
	workflowSecretsMu             sync.Map // userID + workflow path -> *sync.Mutex
	workflowProviderCredentialsMu sync.Map // userID + workflow path -> *sync.Mutex
}

// NewWorkspaceAPIStore constructs a Store backed by the workspace API.
func NewWorkspaceAPIStore(workspaceAPIURL string) (*WorkspaceAPIStore, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(workspaceAPIURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("chathistory: workspace API URL is empty")
	}

	s := &WorkspaceAPIStore{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: wsauth.Transport(&http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			}),
		},
		botCfgs:          make(map[string]*BotConnectorConfig),
		botCfgFile:       workspaceAPIBotConfigFile,
		botCfgLegacyFile: workspaceAPIBotConfigLegacyFile,
	}

	if err := s.loadBotConnectorConfigs(context.Background()); err != nil {
		return nil, err
	}

	log.Printf("[CHATHISTORY] Workspace API store ready (base_url=%s, bot_cfgs=%d)", s.baseURL, len(s.botCfgs))
	return s, nil
}

func (s *WorkspaceAPIStore) Close() error { return nil }

func (s *WorkspaceAPIStore) workspacePathURL(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return s.baseURL + "/api/documents/" + strings.Join(segments, "/")
}

func (s *WorkspaceAPIStore) readFile(ctx context.Context, path string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.workspacePathURL(path), nil)
	if err != nil {
		return nil, false, fmt.Errorf("chathistory: create read request for %s: %w", path, err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("chathistory: read %s via workspace API: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("chathistory: read response body for %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("chathistory: workspace API returned status %d for %s: %s", resp.StatusCode, path, string(body))
	}

	var apiResp workspaceAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, false, fmt.Errorf("chathistory: parse workspace API response for %s: %w", path, err)
	}

	if strings.Contains(apiResp.Message, "File does not exist") || strings.Contains(apiResp.Error, "File not found") {
		return nil, false, nil
	}
	if !apiResp.Success {
		return nil, false, fmt.Errorf("chathistory: workspace API error for %s: %s", path, apiResp.Error)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, false, fmt.Errorf("chathistory: parse workspace API data for %s: %w", path, err)
	}

	rawContent, ok := data["content"]
	if !ok {
		return nil, false, fmt.Errorf("chathistory: workspace API response for %s did not include content", path)
	}

	var content string
	if err := json.Unmarshal(rawContent, &content); err != nil {
		return nil, false, fmt.Errorf("chathistory: parse content for %s: %w", path, err)
	}
	return []byte(content), true, nil
}

func (s *WorkspaceAPIStore) writeFile(ctx context.Context, path string, content []byte) error {
	requestBody, err := json.Marshal(map[string]string{"content": string(content)})
	if err != nil {
		return fmt.Errorf("chathistory: marshal content for %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.workspacePathURL(path), bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("chathistory: create write request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("chathistory: write %s via workspace API: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chathistory: workspace API returned status %d for %s: %s", resp.StatusCode, path, string(body))
	}
	return nil
}

func (s *WorkspaceAPIStore) loadBotConnectorConfigs(ctx context.Context) error {
	s.botCfgMu.Lock()
	defer s.botCfgMu.Unlock()

	s.botCfgs = make(map[string]*BotConnectorConfig)

	data, exists, err := s.readFile(ctx, s.botCfgFile)
	if err != nil {
		return err
	}
	if !exists {
		data, exists, err = s.readFile(ctx, s.botCfgLegacyFile)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}

	if len(data) == 0 {
		return nil
	}

	var raw map[string]*BotConnectorConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("chathistory: parse %s: %w", s.botCfgFile, err)
	}
	for id, cfg := range raw {
		if cfg == nil {
			continue
		}
		s.botCfgs[id] = cfg
	}

	if err := s.saveBotConnectorConfigsLocked(ctx); err != nil {
		return fmt.Errorf("chathistory: migrate bot connector configs to %s: %w", s.botCfgFile, err)
	}
	return nil
}

func (s *WorkspaceAPIStore) saveBotConnectorConfigsLocked(ctx context.Context) error {
	data, err := json.MarshalIndent(s.botCfgs, "", "  ")
	if err != nil {
		return fmt.Errorf("chathistory: marshal bot connector configs: %w", err)
	}
	return s.writeFile(ctx, s.botCfgFile, data)
}

func (s *WorkspaceAPIStore) UpsertBotConnectorConfig(ctx context.Context, req *CreateBotConnectorConfigRequest) (*BotConnectorConfig, error) {
	if req == nil || strings.TrimSpace(req.ID) == "" {
		return nil, fmt.Errorf("chathistory: bot connector config requires non-empty id")
	}
	configJSON := req.ConfigJSON
	if configJSON == "" {
		configJSON = "{}"
	}
	allowedChannels := req.AllowedChannels
	if allowedChannels == "" {
		allowedChannels = "[]"
	}

	s.botCfgMu.Lock()
	defer s.botCfgMu.Unlock()

	now := time.Now().UTC()
	cfg, ok := s.botCfgs[req.ID]
	if ok && cfg != nil {
		cfg.Enabled = req.Enabled
		cfg.BotMode = req.BotMode
		cfg.ConfigJSON = configJSON
		cfg.DefaultPresetID = req.DefaultPresetID
		cfg.AutoConfirm = req.AutoConfirm
		cfg.AllowedChannels = allowedChannels
		cfg.UpdatedAt = now
	} else {
		cfg = &BotConnectorConfig{
			ID:              req.ID,
			Enabled:         req.Enabled,
			BotMode:         req.BotMode,
			ConfigJSON:      configJSON,
			DefaultPresetID: req.DefaultPresetID,
			AutoConfirm:     req.AutoConfirm,
			AllowedChannels: allowedChannels,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		s.botCfgs[req.ID] = cfg
	}

	if err := s.saveBotConnectorConfigsLocked(ctx); err != nil {
		return nil, fmt.Errorf("chathistory: save bot connector configs: %w", err)
	}
	out := *cfg
	return &out, nil
}

func (s *WorkspaceAPIStore) GetBotConnectorConfig(ctx context.Context, id string) (*BotConnectorConfig, error) {
	s.botCfgMu.RLock()
	defer s.botCfgMu.RUnlock()

	cfg, ok := s.botCfgs[id]
	if !ok || cfg == nil {
		return nil, fmt.Errorf("%w: %s", ErrBotConnectorConfigNotFound, id)
	}
	out := *cfg
	return &out, nil
}

func (s *WorkspaceAPIStore) ListBotConnectorConfigs(ctx context.Context) ([]BotConnectorConfig, error) {
	s.botCfgMu.RLock()
	defer s.botCfgMu.RUnlock()

	out := make([]BotConnectorConfig, 0, len(s.botCfgs))
	ids := make([]string, 0, len(s.botCfgs))
	for id := range s.botCfgs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cfg := s.botCfgs[id]
		if cfg == nil {
			continue
		}
		out = append(out, *cfg)
	}
	return out, nil
}

func (s *WorkspaceAPIStore) userSecretsFile(userID string) string {
	return "_users/" + sanitizeUserID(userID) + "/secrets.json"
}

func (s *WorkspaceAPIStore) workflowSecretsFile(userID, workflowPath string) (string, string, error) {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return "", "", err
	}
	return "_users/" + sanitizeUserID(userID) + "/workflow_secrets/" + workflowSecretPathHash(normalized) + ".json", normalized, nil
}

func (s *WorkspaceAPIStore) workflowProviderCredentialsFile(userID, workflowPath string) (string, string, error) {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return "", "", err
	}
	return "_users/" + sanitizeUserID(userID) + "/workflow_provider_credentials/" + workflowSecretPathHash(normalized) + ".json", normalized, nil
}

func (s *WorkspaceAPIStore) secretsLock(userID string) *sync.Mutex {
	key := sanitizeUserID(userID)
	if m, ok := s.secretsMu.Load(key); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := s.secretsMu.LoadOrStore(key, m)
	return actual.(*sync.Mutex)
}

func (s *WorkspaceAPIStore) workflowSecretsLock(userID, workflowPath string) *sync.Mutex {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		normalized = strings.TrimSpace(workflowPath)
	}
	key := sanitizeUserID(userID) + ":" + workflowSecretPathHash(normalized)
	if m, ok := s.workflowSecretsMu.Load(key); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := s.workflowSecretsMu.LoadOrStore(key, m)
	return actual.(*sync.Mutex)
}

func (s *WorkspaceAPIStore) workflowProviderCredentialsLock(userID, workflowPath string) *sync.Mutex {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		normalized = strings.TrimSpace(workflowPath)
	}
	key := sanitizeUserID(userID) + ":" + workflowSecretPathHash(normalized)
	if m, ok := s.workflowProviderCredentialsMu.Load(key); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := s.workflowProviderCredentialsMu.LoadOrStore(key, m)
	return actual.(*sync.Mutex)
}

func (s *WorkspaceAPIStore) loadUserSecretsLocked(ctx context.Context, userID string) (map[string]*userSecretRecord, error) {
	path := s.userSecretsFile(userID)
	data, exists, err := s.readFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return make(map[string]*userSecretRecord), nil
	}

	out := make(map[string]*userSecretRecord)
	if len(data) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("chathistory: parse %s: %w", path, err)
	}
	return out, nil
}

func (s *WorkspaceAPIStore) saveUserSecretsLocked(ctx context.Context, userID string, secrets map[string]*userSecretRecord) error {
	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("chathistory: marshal %s: %w", s.userSecretsFile(userID), err)
	}
	return s.writeFile(ctx, s.userSecretsFile(userID), data)
}

func (s *WorkspaceAPIStore) UpsertUserSecret(ctx context.Context, userID, name, encryptedValue string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("chathistory: secret name is required")
	}
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(ctx, userID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if existing, ok := secrets[name]; ok {
		existing.EncryptedValue = encryptedValue
		existing.UpdatedAt = now
	} else {
		secrets[name] = &userSecretRecord{
			EncryptedValue: encryptedValue,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return s.saveUserSecretsLocked(ctx, userID, secrets)
}

func (s *WorkspaceAPIStore) DeleteUserSecret(ctx context.Context, userID, name string) error {
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(ctx, userID)
	if err != nil {
		return err
	}
	if _, ok := secrets[name]; !ok {
		return nil
	}
	delete(secrets, name)
	return s.saveUserSecretsLocked(ctx, userID, secrets)
}

func (s *WorkspaceAPIStore) ListUserSecrets(ctx context.Context, userID string) ([]UserSecret, error) {
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]UserSecret, 0, len(names))
	sanitizedUserID := sanitizeUserID(userID)
	for _, name := range names {
		rec := secrets[name]
		out = append(out, UserSecret{
			UserID:         sanitizedUserID,
			Name:           name,
			EncryptedValue: rec.EncryptedValue,
			CreatedAt:      rec.CreatedAt,
			UpdatedAt:      rec.UpdatedAt,
		})
	}
	return out, nil
}

func (s *WorkspaceAPIStore) loadWorkflowSecretsLocked(ctx context.Context, userID, workflowPath string) (string, map[string]*userSecretRecord, error) {
	path, normalized, err := s.workflowSecretsFile(userID, workflowPath)
	if err != nil {
		return "", nil, err
	}
	data, exists, err := s.readFile(ctx, path)
	if err != nil {
		return "", nil, err
	}
	if !exists || len(data) == 0 {
		return normalized, make(map[string]*userSecretRecord), nil
	}

	var doc workflowSecretsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", nil, fmt.Errorf("chathistory: parse %s: %w", path, err)
	}
	if doc.Secrets == nil {
		doc.Secrets = make(map[string]*userSecretRecord)
	}
	return normalized, doc.Secrets, nil
}

func (s *WorkspaceAPIStore) saveWorkflowSecretsLocked(ctx context.Context, userID, workflowPath string, secrets map[string]*userSecretRecord) error {
	path, normalized, err := s.workflowSecretsFile(userID, workflowPath)
	if err != nil {
		return err
	}
	doc := workflowSecretsDocument{
		WorkflowPath: normalized,
		Secrets:      secrets,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("chathistory: marshal %s: %w", path, err)
	}
	return s.writeFile(ctx, path, data)
}

func (s *WorkspaceAPIStore) UpsertWorkflowSecret(ctx context.Context, userID, workflowPath, name, encryptedValue string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("chathistory: secret name is required")
	}
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(ctx, userID, workflowPath)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if existing, ok := secrets[name]; ok {
		existing.EncryptedValue = encryptedValue
		existing.UpdatedAt = now
	} else {
		secrets[name] = &userSecretRecord{
			EncryptedValue: encryptedValue,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return s.saveWorkflowSecretsLocked(ctx, userID, normalized, secrets)
}

func (s *WorkspaceAPIStore) DeleteWorkflowSecret(ctx context.Context, userID, workflowPath, name string) error {
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(ctx, userID, workflowPath)
	if err != nil {
		return err
	}
	if _, ok := secrets[name]; !ok {
		return nil
	}
	delete(secrets, name)
	return s.saveWorkflowSecretsLocked(ctx, userID, normalized, secrets)
}

func (s *WorkspaceAPIStore) ListWorkflowSecrets(ctx context.Context, userID, workflowPath string) ([]UserSecret, error) {
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(ctx, userID, workflowPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]UserSecret, 0, len(names))
	sanitizedUserID := sanitizeUserID(userID)
	for _, name := range names {
		rec := secrets[name]
		out = append(out, UserSecret{
			UserID:         sanitizedUserID,
			Name:           name,
			WorkflowPath:   normalized,
			EncryptedValue: rec.EncryptedValue,
			CreatedAt:      rec.CreatedAt,
			UpdatedAt:      rec.UpdatedAt,
		})
	}
	return out, nil
}

func (s *WorkspaceAPIStore) loadWorkflowProviderCredentialsLocked(ctx context.Context, userID, workflowPath string) (string, map[string]*userSecretRecord, error) {
	path, normalized, err := s.workflowProviderCredentialsFile(userID, workflowPath)
	if err != nil {
		return "", nil, err
	}
	data, exists, err := s.readFile(ctx, path)
	if err != nil {
		return "", nil, err
	}
	if !exists || len(data) == 0 {
		return normalized, make(map[string]*userSecretRecord), nil
	}
	var doc workflowProviderCredentialsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", nil, fmt.Errorf("chathistory: parse %s: %w", path, err)
	}
	if doc.Credentials == nil {
		doc.Credentials = make(map[string]*userSecretRecord)
	}
	return normalized, doc.Credentials, nil
}

func (s *WorkspaceAPIStore) saveWorkflowProviderCredentialsLocked(ctx context.Context, userID, workflowPath string, credentials map[string]*userSecretRecord) error {
	path, normalized, err := s.workflowProviderCredentialsFile(userID, workflowPath)
	if err != nil {
		return err
	}
	doc := workflowProviderCredentialsDocument{WorkflowPath: normalized, Credentials: credentials}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("chathistory: marshal %s: %w", path, err)
	}
	return s.writeFile(ctx, path, data)
}

func (s *WorkspaceAPIStore) UpsertWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider, encryptedValue string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(ctx, userID, workflowPath)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if existing, ok := credentials[provider]; ok {
		existing.EncryptedValue = encryptedValue
		existing.UpdatedAt = now
	} else {
		credentials[provider] = &userSecretRecord{EncryptedValue: encryptedValue, CreatedAt: now, UpdatedAt: now}
	}
	return s.saveWorkflowProviderCredentialsLocked(ctx, userID, normalized, credentials)
}

func (s *WorkspaceAPIStore) GetWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) (*WorkflowProviderCredential, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil, fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(ctx, userID, workflowPath)
	if err != nil {
		return nil, err
	}
	rec, ok := credentials[provider]
	if !ok {
		return nil, nil
	}
	return &WorkflowProviderCredential{
		UserID:         sanitizeUserID(userID),
		WorkflowPath:   normalized,
		Provider:       provider,
		EncryptedValue: rec.EncryptedValue,
		CreatedAt:      rec.CreatedAt,
		UpdatedAt:      rec.UpdatedAt,
	}, nil
}

func (s *WorkspaceAPIStore) DeleteWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(ctx, userID, workflowPath)
	if err != nil {
		return err
	}
	if _, ok := credentials[provider]; !ok {
		return nil
	}
	delete(credentials, provider)
	return s.saveWorkflowProviderCredentialsLocked(ctx, userID, normalized, credentials)
}

var _ Store = (*WorkspaceAPIStore)(nil)
