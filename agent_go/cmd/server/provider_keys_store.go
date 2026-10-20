package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
)

const providerKeysFilePath = "config/provider-api-keys.json"
const maskedProviderKeyPrefix = "********"

// StoredProviderKeys holds workspace provider credentials in a single encrypted
// file in the workspace config folder. The top-level
// upstream keys (OpenAI, Anthropic, Vertex, ...) are Pi CLI sub-provider keys.
// Bedrock, Azure and MiniMaxCodingPlan are legacy and only kept so existing
// files still decode.
type StoredProviderKeys struct {
	OpenRouter        string               `json:"openrouter,omitempty"`
	OpenAI            string               `json:"openai,omitempty"`
	Anthropic         string               `json:"anthropic,omitempty"`
	ZAI               string               `json:"zai,omitempty"`
	Kimi              string               `json:"kimi,omitempty"`
	Vertex            string               `json:"vertex,omitempty"`
	CodexCLI          string               `json:"codex_cli,omitempty"`
	CursorCLI         string               `json:"cursor_cli,omitempty"`
	PiCLI             string               `json:"pi_cli,omitempty"`
	MiniMax           string               `json:"minimax,omitempty"`
	MiniMaxCodingPlan string               `json:"minimax_coding_plan,omitempty"`
	PiProviderKeys    map[string]string    `json:"pi_provider_keys,omitempty"`
	Bedrock           *StoredBedrockConfig `json:"bedrock,omitempty"`
	Azure             *StoredAzureConfig   `json:"azure,omitempty"`
}

// StoredBedrockConfig holds Bedrock region config.
type StoredBedrockConfig struct {
	Region string `json:"region"`
}

// StoredAzureConfig holds Azure OpenAI connection config.
type StoredAzureConfig struct {
	Endpoint   string `json:"endpoint"`
	APIKey     string `json:"api_key"`
	APIVersion string `json:"api_version,omitempty"`
	Region     string `json:"region,omitempty"`
}

// SaveProviderKeys encrypts and stores provider API keys to the workspace.
func SaveProviderKeys(ctx context.Context, keys *StoredProviderKeys) error {
	if keys == nil {
		return nil
	}
	if !hasStoredProviderKeys(keys) {
		if err := deleteWorkspaceFile(ctx, providerKeysFilePath); err != nil {
			return fmt.Errorf("failed to delete empty provider keys file: %w", err)
		}
		return nil
	}

	plaintext, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("failed to marshal keys: %w", err)
	}

	encrypted, err := encryptProviderKeys(plaintext)
	if err != nil {
		return fmt.Errorf("failed to encrypt keys: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(encrypted)
	if err := writeFileToWorkspace(ctx, providerKeysFilePath, encoded); err != nil {
		return fmt.Errorf("failed to write encrypted keys: %w", err)
	}

	return nil
}

func hasStoredProviderKeys(keys *StoredProviderKeys) bool {
	if keys == nil {
		return false
	}

	stringValues := []string{
		keys.OpenRouter,
		keys.OpenAI,
		keys.Anthropic,
		keys.ZAI,
		keys.Kimi,
		keys.Vertex,
		keys.CodexCLI,
		keys.CursorCLI,
		keys.PiCLI,
		keys.MiniMax,
		keys.MiniMaxCodingPlan,
	}
	for _, value := range stringValues {
		if isMeaningfulProviderKey(value) {
			return true
		}
	}
	for _, value := range keys.PiProviderKeys {
		if isMeaningfulProviderKey(value) {
			return true
		}
	}
	if keys.Bedrock != nil && strings.TrimSpace(keys.Bedrock.Region) != "" {
		return true
	}
	if keys.Azure != nil && strings.TrimSpace(keys.Azure.Endpoint) != "" && isMeaningfulProviderKey(keys.Azure.APIKey) {
		return true
	}
	return false
}

func isMeaningfulProviderKey(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !isMaskedProviderKey(value)
}

func isMaskedProviderKey(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), maskedProviderKeyPrefix)
}

// LoadProviderKeys reads and decrypts provider API keys from the workspace.
// Returns nil, nil if the file doesn't exist.
func LoadProviderKeys(ctx context.Context) (*StoredProviderKeys, error) {
	content, exists, err := readFileFromWorkspace(ctx, providerKeysFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read keys file: %w", err)
	}
	if !exists {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	plaintext, err := decryptProviderKeys(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt keys: %w", err)
	}

	var keys StoredProviderKeys
	if err := json.Unmarshal(plaintext, &keys); err != nil {
		return nil, fmt.Errorf("failed to unmarshal keys: %w", err)
	}

	return &keys, nil
}

// LoadProviderKeysAsLLMKeys loads workspace-encrypted keys and converts to *llm.ProviderAPIKeys.
// Returns an empty (non-nil) struct if keys don't exist or decryption fails.
func LoadProviderKeysAsLLMKeys(ctx context.Context) *llm.ProviderAPIKeys {
	keys, err := LoadProviderKeys(ctx)
	if err != nil {
		log.Printf("[PROVIDER_KEYS] Failed to load workspace provider keys: %v", err)
		return &llm.ProviderAPIKeys{}
	}
	if keys == nil {
		log.Printf("[PROVIDER_KEYS] No workspace provider keys file found")
		return &llm.ProviderAPIKeys{}
	}
	result := &llm.ProviderAPIKeys{}
	if keys.OpenRouter != "" {
		result.OpenRouter = &keys.OpenRouter
	}
	if keys.OpenAI != "" {
		result.OpenAI = &keys.OpenAI
	}
	if keys.Anthropic != "" {
		result.Anthropic = &keys.Anthropic
	}
	if keys.ZAI != "" {
		result.ZAI = &keys.ZAI
	}
	if keys.Kimi != "" {
		result.Kimi = &keys.Kimi
	}
	if keys.Vertex != "" {
		result.Vertex = &keys.Vertex
	}
	if keys.CodexCLI != "" {
		result.CodexCLI = &keys.CodexCLI
	}
	if keys.CursorCLI != "" {
		result.CursorCLI = &keys.CursorCLI
	}
	if keys.PiCLI != "" {
		result.PiCLI = &keys.PiCLI
	}
	if keys.MiniMax != "" {
		result.MiniMax = &keys.MiniMax
	}
	if len(keys.PiProviderKeys) > 0 {
		result.PiProviderKeys = map[string]string{}
		for provider, key := range keys.PiProviderKeys {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider == "" || strings.TrimSpace(key) == "" {
				continue
			}
			result.PiProviderKeys[provider] = strings.TrimSpace(key)
		}
	}
	// Log which providers have keys loaded
	var loaded []string
	if result.OpenRouter != nil {
		loaded = append(loaded, "openrouter")
	}
	if result.CodexCLI != nil {
		loaded = append(loaded, "codex-cli")
	}
	if result.CursorCLI != nil {
		loaded = append(loaded, "cursor-cli")
	}
	if result.PiCLI != nil {
		loaded = append(loaded, "pi-cli")
	}
	if result.OpenAI != nil {
		loaded = append(loaded, "openai")
	}
	if result.Anthropic != nil {
		loaded = append(loaded, "anthropic")
	}
	if result.ZAI != nil {
		loaded = append(loaded, "z-ai")
	}
	if result.Kimi != nil {
		loaded = append(loaded, "kimi")
	}
	if result.Vertex != nil {
		loaded = append(loaded, "vertex")
	}
	if result.MiniMax != nil {
		loaded = append(loaded, "minimax")
	}
	if len(result.PiProviderKeys) > 0 {
		for provider := range result.PiProviderKeys {
			loaded = append(loaded, "pi:"+provider)
		}
	}
	log.Printf("[PROVIDER_KEYS] Loaded workspace keys for providers: %v", loaded)

	return result
}

// MergedProviderAPIKeys returns a single *llm.ProviderAPIKeys that merges
// env-var keys (.env) and workspace-encrypted keys. Base layer is .env,
// workspace keys override when present. This is the single source of truth
// for "give me all available API keys".
func MergedProviderAPIKeys(ctx context.Context) *llm.ProviderAPIKeys {
	envKeys := buildProviderAPIKeysFromEnv()
	wsKeys := LoadProviderKeysAsLLMKeys(ctx)

	if envKeys == nil && wsKeys == nil {
		return &llm.ProviderAPIKeys{}
	}
	if envKeys == nil {
		return wsKeys
	}
	if wsKeys == nil {
		return envKeys
	}

	// Start from env, workspace overrides when set
	pick := func(env, ws *string) *string {
		if ws != nil {
			return ws
		}
		return env
	}
	result := &llm.ProviderAPIKeys{
		OpenRouter:           pick(envKeys.OpenRouter, wsKeys.OpenRouter),
		OpenAI:               pick(envKeys.OpenAI, wsKeys.OpenAI),
		Anthropic:            pick(envKeys.Anthropic, wsKeys.Anthropic),
		ClaudeCodeOAuthToken: pick(envKeys.ClaudeCodeOAuthToken, wsKeys.ClaudeCodeOAuthToken),
		ZAI:                  pick(envKeys.ZAI, wsKeys.ZAI),
		Kimi:                 pick(envKeys.Kimi, wsKeys.Kimi),
		Vertex:               pick(envKeys.Vertex, wsKeys.Vertex),
		CodexCLI:             pick(envKeys.CodexCLI, wsKeys.CodexCLI),
		CursorCLI:            pick(envKeys.CursorCLI, wsKeys.CursorCLI),
		PiCLI:                pick(envKeys.PiCLI, wsKeys.PiCLI),
		MiniMax:              pick(envKeys.MiniMax, wsKeys.MiniMax),
	}
	result.PiProviderKeys = mergePiProviderKeyMaps(envKeys.PiProviderKeys, wsKeys.PiProviderKeys)

	return result
}

// encryptProviderKeys encrypts data using AES-256-GCM with the derived secrets key.
// Uses "provider-keys" as AAD (global, not per-user).
func encryptProviderKeys(plaintext []byte) ([]byte, error) {
	return encryptProviderKeysWithSecret(plaintext, GetAuthSecret())
}

func encryptProviderKeysWithSecret(plaintext []byte, authSecret []byte) ([]byte, error) {
	key := deriveSecretsKeyFromSecret(authSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	aad := []byte("provider-keys")
	return aesGCM.Seal(nonce, nonce, plaintext, aad), nil
}

// decryptProviderKeys decrypts data using AES-256-GCM with the derived secrets key.
func decryptProviderKeys(data []byte) ([]byte, error) {
	return decryptProviderKeysWithSecret(data, GetAuthSecret())
}

func decryptProviderKeysWithSecret(data []byte, authSecret []byte) ([]byte, error) {
	key := deriveSecretsKeyFromSecret(authSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	aad := []byte("provider-keys")
	return aesGCM.Open(nil, nonce, ciphertext, aad)
}

// handleSaveProviderKeys saves provider API keys (encrypted) to the workspace.
// PUT /api/provider-keys
func (api *StreamingAPI) handleSaveProviderKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var incoming StoredProviderKeys
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	// Bedrock, Azure and MiniMax coding-plan were direct-API agent providers;
	// they are no longer accepted. Top-level upstream keys stay: Pi uses them.
	incoming.Bedrock = nil
	incoming.Azure = nil
	incoming.MiniMaxCodingPlan = ""

	// Merge: load existing keys first, then overlay non-empty incoming fields.
	// This prevents the frontend from wiping keys it didn't send.
	merged := mergeStoredProviderKeys(r.Context(), &incoming)

	if err := SaveProviderKeys(r.Context(), merged); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save keys: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// mergeStoredProviderKeys loads existing keys and overlays incoming non-empty values.
// Empty strings in incoming are treated as "not sent" and won't erase existing keys.
// To explicitly delete a key, send the sentinel value "__DELETE__".
func mergeStoredProviderKeys(ctx context.Context, incoming *StoredProviderKeys) *StoredProviderKeys {
	existing, err := LoadProviderKeys(ctx)
	if err != nil || existing == nil {
		return discardMaskedProviderKeys(incoming) // nothing to merge with
	}

	return mergeStoredProviderKeyValues(existing, incoming)
}

// mergeStoredProviderKeyValues overlays non-empty incoming values onto existing
// provider keys while preserving fields that were not sent by the client.
func mergeStoredProviderKeyValues(existing, incoming *StoredProviderKeys) *StoredProviderKeys {
	if existing == nil {
		return incoming
	}
	if incoming == nil {
		return existing
	}

	pick := func(existingVal, incomingVal string) string {
		if incomingVal == "__DELETE__" {
			return ""
		}
		if isMeaningfulProviderKey(incomingVal) {
			return strings.TrimSpace(incomingVal)
		}
		return existingVal
	}

	merged := &StoredProviderKeys{
		OpenRouter: pick(existing.OpenRouter, incoming.OpenRouter),
		OpenAI:     pick(existing.OpenAI, incoming.OpenAI),
		Anthropic:  pick(existing.Anthropic, incoming.Anthropic),
		ZAI:        pick(existing.ZAI, incoming.ZAI),
		Kimi:       pick(existing.Kimi, incoming.Kimi),
		Vertex:     pick(existing.Vertex, incoming.Vertex),
		CodexCLI:   pick(existing.CodexCLI, incoming.CodexCLI),
		CursorCLI:  pick(existing.CursorCLI, incoming.CursorCLI),
		PiCLI:      pick(existing.PiCLI, incoming.PiCLI),
		MiniMax:    pick(existing.MiniMax, incoming.MiniMax),
	}
	merged.PiProviderKeys = mergeStoredPiProviderKeys(existing.PiProviderKeys, incoming.PiProviderKeys)
	// Bedrock / Azure: incoming wins if present, else keep existing
	if incoming.Bedrock != nil {
		merged.Bedrock = incoming.Bedrock
	} else {
		merged.Bedrock = existing.Bedrock
	}
	merged.Azure = mergeStoredAzureConfig(existing.Azure, incoming.Azure)

	return merged
}

func mergeStoredAzureConfig(existing, incoming *StoredAzureConfig) *StoredAzureConfig {
	if incoming == nil {
		return existing
	}
	if existing == nil {
		incomingCopy := *incoming
		if isMaskedProviderKey(incomingCopy.APIKey) {
			incomingCopy.APIKey = ""
		}
		if strings.TrimSpace(incomingCopy.Endpoint) == "" && strings.TrimSpace(incomingCopy.APIKey) == "" {
			return nil
		}
		return &incomingCopy
	}

	merged := *existing
	if endpoint := strings.TrimSpace(incoming.Endpoint); endpoint != "" {
		merged.Endpoint = endpoint
	}
	if incoming.APIKey == "__DELETE__" {
		merged.APIKey = ""
	} else if isMeaningfulProviderKey(incoming.APIKey) {
		merged.APIKey = strings.TrimSpace(incoming.APIKey)
	}
	if apiVersion := strings.TrimSpace(incoming.APIVersion); apiVersion != "" {
		merged.APIVersion = apiVersion
	}
	if region := strings.TrimSpace(incoming.Region); region != "" {
		merged.Region = region
	}
	if strings.TrimSpace(merged.Endpoint) == "" && strings.TrimSpace(merged.APIKey) == "" {
		return nil
	}
	return &merged
}

func mergeStoredPiProviderKeys(existing, incoming map[string]string) map[string]string {
	merged := map[string]string{}
	for provider, key := range existing {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider != "" && key != "" {
			merged[provider] = key
		}
	}
	for provider, key := range incoming {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider == "" {
			continue
		}
		if key == "__DELETE__" {
			delete(merged, provider)
			continue
		}
		if isMeaningfulProviderKey(key) {
			merged[provider] = key
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func discardMaskedProviderKeys(keys *StoredProviderKeys) *StoredProviderKeys {
	if keys == nil {
		return nil
	}
	sanitized := *keys
	clearMasked := func(value string) string {
		if isMaskedProviderKey(value) {
			return ""
		}
		return value
	}
	sanitized.OpenRouter = clearMasked(sanitized.OpenRouter)
	sanitized.OpenAI = clearMasked(sanitized.OpenAI)
	sanitized.Anthropic = clearMasked(sanitized.Anthropic)
	sanitized.ZAI = clearMasked(sanitized.ZAI)
	sanitized.Kimi = clearMasked(sanitized.Kimi)
	sanitized.Vertex = clearMasked(sanitized.Vertex)
	sanitized.CodexCLI = clearMasked(sanitized.CodexCLI)
	sanitized.CursorCLI = clearMasked(sanitized.CursorCLI)
	sanitized.PiCLI = clearMasked(sanitized.PiCLI)
	sanitized.MiniMax = clearMasked(sanitized.MiniMax)
	sanitized.MiniMaxCodingPlan = clearMasked(sanitized.MiniMaxCodingPlan)
	if keys.PiProviderKeys != nil {
		sanitized.PiProviderKeys = map[string]string{}
		for provider, key := range keys.PiProviderKeys {
			if !isMaskedProviderKey(key) {
				sanitized.PiProviderKeys[provider] = key
			}
		}
		if len(sanitized.PiProviderKeys) == 0 {
			sanitized.PiProviderKeys = nil
		}
	}
	if keys.Azure != nil {
		azure := *keys.Azure
		azure.APIKey = clearMasked(azure.APIKey)
		sanitized.Azure = &azure
	}
	return &sanitized
}

func maskStoredProviderKeys(keys *StoredProviderKeys) *StoredProviderKeys {
	if keys == nil {
		return &StoredProviderKeys{}
	}
	masked := *keys
	masked.OpenRouter = maskProviderKey(masked.OpenRouter)
	masked.OpenAI = maskProviderKey(masked.OpenAI)
	masked.Anthropic = maskProviderKey(masked.Anthropic)
	masked.ZAI = maskProviderKey(masked.ZAI)
	masked.Kimi = maskProviderKey(masked.Kimi)
	masked.Vertex = maskProviderKey(masked.Vertex)
	masked.CodexCLI = maskProviderKey(masked.CodexCLI)
	masked.CursorCLI = maskProviderKey(masked.CursorCLI)
	masked.PiCLI = maskProviderKey(masked.PiCLI)
	masked.MiniMax = maskProviderKey(masked.MiniMax)
	masked.MiniMaxCodingPlan = maskProviderKey(masked.MiniMaxCodingPlan)
	if keys.PiProviderKeys != nil {
		masked.PiProviderKeys = map[string]string{}
		for provider, key := range keys.PiProviderKeys {
			if maskedKey := maskProviderKey(key); maskedKey != "" {
				masked.PiProviderKeys[provider] = maskedKey
			}
		}
		if len(masked.PiProviderKeys) == 0 {
			masked.PiProviderKeys = nil
		}
	}
	if keys.Azure != nil {
		azure := *keys.Azure
		azure.APIKey = maskProviderKey(azure.APIKey)
		masked.Azure = &azure
	}
	return &masked
}

func maskProviderKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return maskedProviderKeyPrefix
	}
	return maskedProviderKeyPrefix + value[len(value)-4:]
}

func mergePiProviderKeyMaps(envKeys, workspaceKeys map[string]string) map[string]string {
	merged := map[string]string{}
	for provider, key := range envKeys {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider != "" && key != "" {
			merged[provider] = key
		}
	}
	for provider, key := range workspaceKeys {
		provider = strings.ToLower(strings.TrimSpace(provider))
		key = strings.TrimSpace(key)
		if provider != "" && key != "" {
			merged[provider] = key
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func setStoredPiProviderAPIKey(keys *StoredProviderKeys, piProvider, modelID, apiKey string) (string, bool) {
	if keys == nil {
		return "", false
	}
	value := strings.TrimSpace(apiKey)
	if value == "" {
		return "", false
	}

	provider := normalizeStoredPiProviderKey(piProvider)
	if provider == "" {
		provider = normalizeStoredPiProviderKey(piProviderFromModelID(modelID))
	}
	if provider == "" {
		provider = "google"
	}

	if keys.PiProviderKeys == nil {
		keys.PiProviderKeys = map[string]string{}
	}
	keys.PiProviderKeys[provider] = value
	setStoredPiProviderTopLevelAPIKey(keys, provider, value)
	return provider, true
}

func normalizeStoredPiProviderKey(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "", "pi-cli", "auto":
		return ""
	case "google-vertex":
		return "google"
	default:
		return provider
	}
}

func setStoredPiProviderTopLevelAPIKey(keys *StoredProviderKeys, provider, apiKey string) {
	if keys == nil {
		return
	}
	value := strings.TrimSpace(apiKey)
	switch normalizeStoredPiProviderKey(provider) {
	case "google":
		keys.PiCLI = value
	case "openai":
		keys.OpenAI = value
	case "anthropic":
		keys.Anthropic = value
	case "openrouter":
		keys.OpenRouter = value
	case "zai":
		keys.ZAI = value
	case "kimi-coding", "moonshotai", "moonshotai-cn":
		keys.Kimi = value
	case "minimax":
		keys.MiniMax = value
	}
}

func selectStoredPiAPIKeyForModel(keys *StoredProviderKeys, modelID string) string {
	if keys == nil {
		return ""
	}
	return selectPiAPIKeyForModel(&llm.ProviderAPIKeys{
		OpenRouter:     stringFieldPtr(keys.OpenRouter),
		OpenAI:         stringFieldPtr(keys.OpenAI),
		Anthropic:      stringFieldPtr(keys.Anthropic),
		Vertex:         stringFieldPtr(keys.Vertex),
		PiCLI:          stringFieldPtr(keys.PiCLI),
		MiniMax:        stringFieldPtr(keys.MiniMax),
		ZAI:            stringFieldPtr(keys.ZAI),
		Kimi:           stringFieldPtr(keys.Kimi),
		PiProviderKeys: keys.PiProviderKeys,
	}, modelID)
}

func stringFieldPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func selectPiAPIKeyForModel(keys *llm.ProviderAPIKeys, modelID string) string {
	if keys == nil {
		return ""
	}
	provider := piProviderFromModelID(modelID)
	if key := piProviderKeyFromMap(keys.PiProviderKeys, provider); key != "" {
		return key
	}
	switch provider {
	case "google", "google-vertex":
		for _, value := range []*string{keys.PiCLI, keys.Vertex} {
			if key := strings.TrimSpace(stringPtrValue(value)); key != "" {
				return key
			}
		}
	case "openai":
		return stringPtrValue(keys.OpenAI)
	case "anthropic":
		return stringPtrValue(keys.Anthropic)
	case "openrouter":
		return stringPtrValue(keys.OpenRouter)
	case "zai":
		return stringPtrValue(keys.ZAI)
	case "kimi-coding", "moonshotai", "moonshotai-cn":
		return stringPtrValue(keys.Kimi)
	case "minimax":
		return stringPtrValue(keys.MiniMax)
	}
	return ""
}

func piProviderFromModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || strings.EqualFold(modelID, "pi-cli") || strings.EqualFold(modelID, "auto") {
		return "google"
	}
	if slash := strings.Index(modelID, "/"); slash > 0 {
		return strings.ToLower(strings.TrimSpace(modelID[:slash]))
	}
	return "google"
}

func piProviderKeyFromMap(keys map[string]string, provider string) string {
	if keys == nil {
		return ""
	}
	for _, candidate := range piProviderKeyAliases(provider) {
		if key := strings.TrimSpace(keys[candidate]); key != "" {
			return key
		}
	}
	return ""
}

func piProviderKeyAliases(provider string) []string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "google-vertex":
		return []string{"google-vertex", "google"}
	case "moonshotai", "moonshotai-cn":
		return []string{provider, "kimi-coding"}
	default:
		return []string{provider}
	}
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// handleLoadProviderKeys loads and returns decrypted provider API keys.
// GET /api/provider-keys
func (api *StreamingAPI) handleLoadProviderKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	keys, err := LoadProviderKeys(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load keys: %v", err), http.StatusInternalServerError)
		return
	}

	if keys == nil {
		keys = &StoredProviderKeys{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskStoredProviderKeys(keys))
}
