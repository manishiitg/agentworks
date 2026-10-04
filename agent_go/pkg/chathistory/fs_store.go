package chathistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// FilesystemStore is the filesystem-backed implementation of Store. It owns
// small JSON files under workspace-docs:
//
//	config/bot-connectors.json          — operator-level bot connector configs
//	_users/<userID>/secrets.json                         — per-user encrypted secret blobs
//	_users/<userID>/workflow_secrets/<workflow-hash>.json - per-workflow encrypted secret blobs
//	_users/<userID>/workflow_provider_credentials/<workflow-hash>.json - private provider auth
//
// Both are loaded into memory on first access and mutated under narrow
// mutexes. The store is built for single-instance deployments.
type FilesystemStore struct {
	rootDir string

	// Bot connector configs (keyed by platform id — "slack", "discord", ...)
	botCfgMu         sync.RWMutex
	botCfgs          map[string]*BotConnectorConfig
	botCfgFile       string
	botCfgLegacyFile string

	// Secrets mutexes, created on-demand.
	secretsMu                     sync.Map // userID -> *sync.Mutex
	workflowSecretsMu             sync.Map // userID + workflow path -> *sync.Mutex
	workflowProviderCredentialsMu sync.Map // userID + workflow path -> *sync.Mutex
}

// sanitizeUserID returns a safe folder segment for a user ID. Empty or
// invalid IDs collapse to "default".

func sanitizeUserID(userID string) string {
	return workspaceref.SanitizeUserID(strings.TrimSpace(userID))
}

// NewFilesystemStore constructs a FilesystemStore rooted at the given
// workspace-docs path and loads the bot connector config file eagerly so
// subsequent reads are in-memory.
func NewFilesystemStore(workspaceDocsAbs string) (*FilesystemStore, error) {
	if workspaceDocsAbs == "" {
		return nil, fmt.Errorf("chathistory: workspace-docs root is empty")
	}
	abs, err := filepath.Abs(workspaceDocsAbs)
	if err != nil {
		return nil, fmt.Errorf("chathistory: resolve workspace-docs path: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "_system"), 0755); err != nil {
		return nil, fmt.Errorf("chathistory: create _system dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "config"), 0755); err != nil {
		return nil, fmt.Errorf("chathistory: create config dir: %w", err)
	}

	s := &FilesystemStore{
		rootDir:          abs,
		botCfgs:          make(map[string]*BotConnectorConfig),
		botCfgFile:       filepath.Join(abs, "config", "bot-connectors.json"),
		botCfgLegacyFile: filepath.Join(abs, "_system", "bot_connectors.json"),
	}
	if err := s.loadBotConnectorConfigs(); err != nil {
		log.Printf("[CHATHISTORY] Warning: could not load bot connector configs: %v (starting empty)", err)
	}
	log.Printf("[CHATHISTORY] Filesystem store ready (root=%s, bot_cfgs=%d)", abs, len(s.botCfgs))
	return s, nil
}

// Close is a no-op for the filesystem store.
func (s *FilesystemStore) Close() error { return nil }

// --- Bot connector configs ----------------------------------------------

func (s *FilesystemStore) loadBotConnectorConfigs() error {
	s.botCfgMu.Lock()
	defer s.botCfgMu.Unlock()
	s.botCfgs = make(map[string]*BotConnectorConfig)

	data, err := os.ReadFile(s.botCfgFile)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = os.ReadFile(s.botCfgLegacyFile)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
		} else {
			return err
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
	if err := s.saveBotConnectorConfigsLocked(); err != nil {
		return fmt.Errorf("chathistory: migrate bot connector configs to %s: %w", s.botCfgFile, err)
	}
	return nil
}

func (s *FilesystemStore) saveBotConnectorConfigsLocked() error {
	return fsutil.WriteJSONAtomic(s.botCfgFile, s.botCfgs, 0644)
}

func (s *FilesystemStore) UpsertBotConnectorConfig(ctx context.Context, req *CreateBotConnectorConfigRequest) (*BotConnectorConfig, error) {
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

	if err := s.saveBotConnectorConfigsLocked(); err != nil {
		return nil, fmt.Errorf("chathistory: save bot connector configs: %w", err)
	}
	out := *cfg
	return &out, nil
}

func (s *FilesystemStore) GetBotConnectorConfig(ctx context.Context, id string) (*BotConnectorConfig, error) {
	s.botCfgMu.RLock()
	defer s.botCfgMu.RUnlock()
	cfg, ok := s.botCfgs[id]
	if !ok || cfg == nil {
		return nil, fmt.Errorf("%w: %s", ErrBotConnectorConfigNotFound, id)
	}
	out := *cfg
	return &out, nil
}

func (s *FilesystemStore) ListBotConnectorConfigs(ctx context.Context) ([]BotConnectorConfig, error) {
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

// --- User secrets -------------------------------------------------------

type userSecretRecord struct {
	EncryptedValue string    `json:"encrypted_value"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type workflowSecretsDocument struct {
	WorkflowPath string                       `json:"workflow_path"`
	Secrets      map[string]*userSecretRecord `json:"secrets"`
}

type workflowProviderCredentialsDocument struct {
	WorkflowPath string                       `json:"workflow_path"`
	Credentials  map[string]*userSecretRecord `json:"credentials"`
}

func normalizeWorkflowSecretPath(workflowPath string) (string, error) {
	workflowPath = strings.TrimSpace(filepath.ToSlash(workflowPath))
	workflowPath = strings.Trim(workflowPath, "/")
	if workflowPath == "" {
		return "", fmt.Errorf("chathistory: workflow path is required")
	}
	if strings.Contains(workflowPath, "\x00") {
		return "", fmt.Errorf("chathistory: workflow path contains invalid character")
	}
	cleaned := filepath.ToSlash(filepath.Clean(workflowPath))
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("chathistory: invalid workflow path %q", workflowPath)
	}
	return cleaned, nil
}

func workflowSecretPathHash(workflowPath string) string {
	sum := sha256.Sum256([]byte(workflowPath))
	return hex.EncodeToString(sum[:])
}

func (s *FilesystemStore) userSecretsFile(userID string) string {
	return filepath.Join(s.rootDir, workspaceref.UsersDir, sanitizeUserID(userID), "secrets.json")
}

func (s *FilesystemStore) workflowSecretsFile(userID, workflowPath string) (string, string, error) {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(s.rootDir, workspaceref.UsersDir, sanitizeUserID(userID), "workflow_secrets", workflowSecretPathHash(normalized)+".json"), normalized, nil
}

func (s *FilesystemStore) workflowProviderCredentialsFile(userID, workflowPath string) (string, string, error) {
	normalized, err := normalizeWorkflowSecretPath(workflowPath)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(s.rootDir, workspaceref.UsersDir, sanitizeUserID(userID), "workflow_provider_credentials", workflowSecretPathHash(normalized)+".json"), normalized, nil
}

// secretsLock returns a per-user mutex, created on demand.
func (s *FilesystemStore) secretsLock(userID string) *sync.Mutex {
	key := sanitizeUserID(userID)
	if m, ok := s.secretsMu.Load(key); ok {
		return m.(*sync.Mutex)
	}
	m := &sync.Mutex{}
	actual, _ := s.secretsMu.LoadOrStore(key, m)
	return actual.(*sync.Mutex)
}

func (s *FilesystemStore) workflowSecretsLock(userID, workflowPath string) *sync.Mutex {
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

func (s *FilesystemStore) workflowProviderCredentialsLock(userID, workflowPath string) *sync.Mutex {
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

func (s *FilesystemStore) loadUserSecretsLocked(userID string) (map[string]*userSecretRecord, error) {
	path := s.userSecretsFile(userID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*userSecretRecord), nil
		}
		return nil, err
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

func (s *FilesystemStore) saveUserSecretsLocked(userID string, secrets map[string]*userSecretRecord) error {
	return fsutil.WriteJSONAtomic(s.userSecretsFile(userID), secrets, 0600)
}

func (s *FilesystemStore) UpsertUserSecret(ctx context.Context, userID, name, encryptedValue string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("chathistory: secret name is required")
	}
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(userID)
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
	return s.saveUserSecretsLocked(userID, secrets)
}

func (s *FilesystemStore) DeleteUserSecret(ctx context.Context, userID, name string) error {
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(userID)
	if err != nil {
		return err
	}
	if _, ok := secrets[name]; !ok {
		return nil
	}
	delete(secrets, name)
	return s.saveUserSecretsLocked(userID, secrets)
}

func (s *FilesystemStore) ListUserSecrets(ctx context.Context, userID string) ([]UserSecret, error) {
	mux := s.secretsLock(userID)
	mux.Lock()
	defer mux.Unlock()

	secrets, err := s.loadUserSecretsLocked(userID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]UserSecret, 0, len(names))
	for _, name := range names {
		rec := secrets[name]
		out = append(out, UserSecret{
			UserID:         sanitizeUserID(userID),
			Name:           name,
			EncryptedValue: rec.EncryptedValue,
			CreatedAt:      rec.CreatedAt,
			UpdatedAt:      rec.UpdatedAt,
		})
	}
	return out, nil
}

func (s *FilesystemStore) loadWorkflowSecretsLocked(userID, workflowPath string) (string, map[string]*userSecretRecord, error) {
	path, normalized, err := s.workflowSecretsFile(userID, workflowPath)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalized, make(map[string]*userSecretRecord), nil
		}
		return "", nil, err
	}
	if len(data) == 0 {
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

func (s *FilesystemStore) saveWorkflowSecretsLocked(userID, workflowPath string, secrets map[string]*userSecretRecord) error {
	path, normalized, err := s.workflowSecretsFile(userID, workflowPath)
	if err != nil {
		return err
	}
	doc := workflowSecretsDocument{
		WorkflowPath: normalized,
		Secrets:      secrets,
	}
	return fsutil.WriteJSONAtomic(path, doc, 0600)
}

func (s *FilesystemStore) UpsertWorkflowSecret(ctx context.Context, userID, workflowPath, name, encryptedValue string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("chathistory: secret name is required")
	}
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(userID, workflowPath)
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
	return s.saveWorkflowSecretsLocked(userID, normalized, secrets)
}

func (s *FilesystemStore) DeleteWorkflowSecret(ctx context.Context, userID, workflowPath, name string) error {
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(userID, workflowPath)
	if err != nil {
		return err
	}
	if _, ok := secrets[name]; !ok {
		return nil
	}
	delete(secrets, name)
	return s.saveWorkflowSecretsLocked(userID, normalized, secrets)
}

func (s *FilesystemStore) ListWorkflowSecrets(ctx context.Context, userID, workflowPath string) ([]UserSecret, error) {
	mux := s.workflowSecretsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, secrets, err := s.loadWorkflowSecretsLocked(userID, workflowPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for n := range secrets {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]UserSecret, 0, len(names))
	for _, name := range names {
		rec := secrets[name]
		out = append(out, UserSecret{
			UserID:         sanitizeUserID(userID),
			Name:           name,
			WorkflowPath:   normalized,
			EncryptedValue: rec.EncryptedValue,
			CreatedAt:      rec.CreatedAt,
			UpdatedAt:      rec.UpdatedAt,
		})
	}
	return out, nil
}

func (s *FilesystemStore) loadWorkflowProviderCredentialsLocked(userID, workflowPath string) (string, map[string]*userSecretRecord, error) {
	path, normalized, err := s.workflowProviderCredentialsFile(userID, workflowPath)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalized, make(map[string]*userSecretRecord), nil
		}
		return "", nil, err
	}
	if len(data) == 0 {
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

func (s *FilesystemStore) saveWorkflowProviderCredentialsLocked(userID, workflowPath string, credentials map[string]*userSecretRecord) error {
	path, normalized, err := s.workflowProviderCredentialsFile(userID, workflowPath)
	if err != nil {
		return err
	}
	doc := workflowProviderCredentialsDocument{WorkflowPath: normalized, Credentials: credentials}
	return fsutil.WriteJSONAtomic(path, doc, 0600)
}

func (s *FilesystemStore) UpsertWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider, encryptedValue string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(userID, workflowPath)
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
	return s.saveWorkflowProviderCredentialsLocked(userID, normalized, credentials)
}

func (s *FilesystemStore) GetWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) (*WorkflowProviderCredential, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil, fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(userID, workflowPath)
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

func (s *FilesystemStore) DeleteWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return fmt.Errorf("chathistory: provider is required")
	}
	mux := s.workflowProviderCredentialsLock(userID, workflowPath)
	mux.Lock()
	defer mux.Unlock()

	normalized, credentials, err := s.loadWorkflowProviderCredentialsLocked(userID, workflowPath)
	if err != nil {
		return err
	}
	if _, ok := credentials[provider]; !ok {
		return nil
	}
	delete(credentials, provider)
	return s.saveWorkflowProviderCredentialsLocked(userID, normalized, credentials)
}

// Compile-time check that FilesystemStore satisfies Store.
var _ Store = (*FilesystemStore)(nil)
