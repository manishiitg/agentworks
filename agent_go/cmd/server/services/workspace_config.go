package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func workspaceAPIURL() string {
	if url := os.Getenv("WORKSPACE_API_URL"); url != "" {
		return url
	}
	return "http://127.0.0.1:8081"
}

// PublishedLLM is the minimal workspace-backed published LLM record needed by
// workspace-aware services outside the main server package.
type PublishedLLM struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Provider       string                 `json:"provider"`
	ModelID        string                 `json:"model_id"`
	SearchRole     string                 `json:"search_role,omitempty"`
	SearchPriority *int                   `json:"search_priority,omitempty"`
	Options        map[string]interface{} `json:"options,omitempty"`
}

func isPublishedLLMProviderAllowed(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "bedrock", "openai", "vertex", "anthropic", "azure",
		"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli":
		return true
	default:
		return false
	}
}

type ImageGenerationModelConfig struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id"`
}

type ImageGenerationConfig struct {
	Primary *ImageGenerationModelConfig `json:"primary,omitempty"`
}

type ImageAnalysisConfig struct {
	Primary *ImageGenerationModelConfig `json:"primary,omitempty"`
}

// readWorkspaceFile reads a file from the workspace API using the given workspaceURL.
// Returns (content, true, nil) if found, ("", false, nil) if not found, or ("", false, err) on error.
func readWorkspaceFile(ctx context.Context, workspaceURL, filePath string) (string, bool, error) {
	pathSegments := strings.Split(filePath, "/")
	encodedSegments := make([]string, len(pathSegments))
	for i, seg := range pathSegments {
		encodedSegments[i] = strings.NewReplacer(" ", "%20", "#", "%23", "?", "%3F").Replace(seg)
	}
	encodedPath := strings.Join(encodedSegments, "/")

	apiURL := workspaceURL + "/api/documents/" + encodedPath
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("workspace API returned status %d", resp.StatusCode)
	}

	var apiResp struct {
		Success bool        `json:"success"`
		Error   string      `json:"error"`
		Message string      `json:"message"`
		Data    interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", false, fmt.Errorf("failed to parse API response: %w", err)
	}
	// A missing file is a normal "not configured yet" state, not a failure. The
	// workspace API reports it as HTTP 200 with success=true and an explanatory
	// message/error, so this check must run regardless of Success — when it was
	// nested under !Success it never fired, and callers got the misleading
	// "failed to extract content" error below instead of a clean absent signal.
	if strings.Contains(apiResp.Message, "File does not exist") || strings.Contains(apiResp.Error, "File not found") {
		return "", false, nil
	}
	if !apiResp.Success {
		return "", false, fmt.Errorf("workspace API error: %s", apiResp.Error)
	}

	var content string
	if dataMap, ok := apiResp.Data.(map[string]interface{}); ok {
		if c, ok := dataMap["content"].(string); ok {
			content = c
		}
	}
	if content == "" {
		log.Printf("[WORKSPACE_CONFIG] Failed to extract content for %s", filePath)
		return "", false, fmt.Errorf("failed to extract content from workspace response")
	}
	return content, true, nil
}

func writeWorkspaceFile(ctx context.Context, workspaceURL, filePath, content string) error {
	pathSegments := strings.Split(filePath, "/")
	encodedSegments := make([]string, len(pathSegments))
	for i, seg := range pathSegments {
		encodedSegments[i] = strings.NewReplacer(" ", "%20", "#", "%23", "?", "%3F").Replace(seg)
	}
	encodedPath := strings.Join(encodedSegments, "/")

	requestBody, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	apiURL := workspaceURL + "/api/documents/" + encodedPath
	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(string(requestBody)))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// LoadDelegationTierConfig reads delegation tier config (plain JSON) from the workspace.
// Returns (nil, false, nil) if the file doesn't exist.
func LoadDelegationTierConfig(ctx context.Context, workspaceURL string) (map[string]interface{}, bool, error) {
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "config/delegation-tier-config.json")
	if err != nil || !exists {
		return nil, exists, err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		return nil, false, fmt.Errorf("failed to parse tier config: %w", err)
	}
	return cfg, true, nil
}

// MultiAgentChatCapabilities is the typed view of the `capabilities` block in
// _users/<userID>/multiagent-config.json. JSON tags mirror the server-side
// WorkflowCapabilities so the same file round-trips; the services package keeps
// its own struct to avoid importing the server package.
type MultiAgentChatCapabilities struct {
	SelectedServers           []string                       `json:"selected_servers"`
	SelectedTools             []string                       `json:"selected_tools"`
	SelectedSkills            []string                       `json:"selected_skills"`
	SelectedSecrets           []string                       `json:"selected_secrets"`
	SelectedGlobalSecretNames *[]string                      `json:"selected_global_secret_names"`
	BrowserMode               string                         `json:"browser_mode"`
	CDPPorts                  []int                          `json:"cdp_ports,omitempty"`
	UseCodeExecutionMode      bool                           `json:"use_code_execution_mode"`
	LLMConfig                 *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
	Notifications             *MultiAgentNotificationConfig  `json:"notifications,omitempty"`
}

type MultiAgentNotificationConfig struct {
	SlackWebhookSecretName string `json:"slack_webhook_secret_name,omitempty"`
}

// LoadMultiAgentChatCapabilities reads the user's saved multi-agent chat
// capabilities from _users/<userID>/multiagent-config.json via the workspace
// API so bot sessions can start with the user's chosen setup. Returns
// (nil, false, nil) when no config is saved.
func LoadMultiAgentChatCapabilities(ctx context.Context, workspaceURL, userID string) (*MultiAgentChatCapabilities, bool, error) {
	if userID == "" {
		return nil, false, nil
	}
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "_users/"+userID+"/multiagent-config.json")
	if err != nil || !exists {
		return nil, exists, err
	}
	var parsed struct {
		Capabilities *MultiAgentChatCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, false, fmt.Errorf("failed to parse multiagent-config.json: %w", err)
	}
	if parsed.Capabilities == nil {
		return nil, false, nil
	}
	return parsed.Capabilities, true, nil
}

// LoadPublishedLLMs reads published LLM metadata (plain JSON) from the workspace.
// Returns (nil, false, nil) if the file doesn't exist.
func LoadPublishedLLMs(ctx context.Context, workspaceURL string) ([]PublishedLLM, bool, error) {
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "config/published-llms.json")
	if err != nil || !exists {
		return nil, exists, err
	}

	var stored []PublishedLLM
	if err := json.Unmarshal([]byte(content), &stored); err != nil {
		return nil, false, fmt.Errorf("failed to parse published llms: %w", err)
	}

	normalized := make([]PublishedLLM, 0, len(stored))
	for _, entry := range stored {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Name = strings.TrimSpace(entry.Name)
		entry.Provider = strings.TrimSpace(entry.Provider)
		entry.ModelID = strings.TrimSpace(entry.ModelID)
		entry.SearchRole = strings.ToLower(strings.TrimSpace(entry.SearchRole))
		if entry.Provider == "" || entry.ModelID == "" {
			continue
		}
		if !isPublishedLLMProviderAllowed(entry.Provider) {
			continue
		}
		if len(entry.Options) == 0 {
			entry.Options = nil
		}
		normalized = append(normalized, entry)
	}

	return normalized, true, nil
}

// LoadImageGenerationConfig reads image generation defaults (plain JSON) from the workspace.
// Returns (nil, false, nil) if the file doesn't exist.
func LoadImageGenerationConfig(ctx context.Context, workspaceURL string) (*ImageGenerationConfig, bool, error) {
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "config/image-generation-config.json")
	if err != nil || !exists {
		return nil, exists, err
	}

	var stored ImageGenerationConfig
	if err := json.Unmarshal([]byte(content), &stored); err != nil {
		return nil, false, fmt.Errorf("failed to parse image generation config: %w", err)
	}

	cfg := &ImageGenerationConfig{}
	if stored.Primary != nil {
		if primary := sanitizeImageGenerationModelConfig(stored.Primary); primary != nil {
			cfg.Primary = primary
		}
	}

	return cfg, true, nil
}

// LoadImageAnalysisConfig reads image analysis defaults (plain JSON) from the workspace.
// Returns (nil, false, nil) if the file doesn't exist.
func LoadImageAnalysisConfig(ctx context.Context, workspaceURL string) (*ImageAnalysisConfig, bool, error) {
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "config/image-analysis-config.json")
	if err != nil || !exists {
		return nil, exists, err
	}

	var stored ImageAnalysisConfig
	if err := json.Unmarshal([]byte(content), &stored); err != nil {
		return nil, false, fmt.Errorf("failed to parse image analysis config: %w", err)
	}

	cfg := &ImageAnalysisConfig{}
	if stored.Primary != nil {
		if primary := sanitizeImageGenerationModelConfig(stored.Primary); primary != nil {
			cfg.Primary = primary
		}
	}

	return cfg, true, nil
}

func sanitizeImageGenerationModelConfig(cfg *ImageGenerationModelConfig) *ImageGenerationModelConfig {
	if cfg == nil {
		return nil
	}

	provider := strings.TrimSpace(cfg.Provider)
	modelID := strings.TrimSpace(cfg.ModelID)
	if provider == "" || modelID == "" {
		return nil
	}

	return &ImageGenerationModelConfig{
		Provider: provider,
		ModelID:  modelID,
	}
}

// LoadProviderKeys reads and decrypts provider API keys from the workspace.
// Returns a map in the api_keys format used by llm_config.
// Returns (nil, false, nil) if the file doesn't exist.
func LoadProviderKeys(ctx context.Context, workspaceURL string) (map[string]interface{}, bool, error) {
	content, exists, err := readWorkspaceFile(ctx, workspaceURL, "config/provider-api-keys.json")
	if err != nil || !exists {
		return nil, exists, err
	}

	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return nil, false, fmt.Errorf("failed to decode base64: %w", err)
	}

	plaintext, err := decryptWithAAD(data, "provider-keys")
	if err != nil {
		return nil, false, fmt.Errorf("failed to decrypt provider keys: %w", err)
	}

	var stored struct {
		OpenAI         string            `json:"openai,omitempty"`
		Anthropic      string            `json:"anthropic,omitempty"`
		ZAI            string            `json:"zai,omitempty"`
		Kimi           string            `json:"kimi,omitempty"`
		Vertex         string            `json:"vertex,omitempty"`
		CodexCLI       string            `json:"codex_cli,omitempty"`
		PiCLI          string            `json:"pi_cli,omitempty"`
		MiniMax        string            `json:"minimax,omitempty"`
		PiProviderKeys map[string]string `json:"pi_provider_keys,omitempty"`
	}
	if err := json.Unmarshal(plaintext, &stored); err != nil {
		return nil, false, fmt.Errorf("failed to parse provider keys: %w", err)
	}

	m := map[string]interface{}{}
	if stored.OpenAI != "" {
		m["openai"] = stored.OpenAI
	}
	if stored.Anthropic != "" {
		m["anthropic"] = stored.Anthropic
	}
	if stored.ZAI != "" {
		m["z-ai"] = stored.ZAI
	}
	if stored.Kimi != "" {
		m["kimi"] = stored.Kimi
	}
	if stored.Vertex != "" {
		m["vertex"] = stored.Vertex
	}
	if stored.CodexCLI != "" {
		m["codex_cli"] = stored.CodexCLI
	}
	if stored.PiCLI != "" {
		m["pi_cli"] = stored.PiCLI
	}
	if stored.MiniMax != "" {
		m["minimax"] = stored.MiniMax
	}
	if len(stored.PiProviderKeys) > 0 {
		clean := map[string]string{}
		for provider, key := range stored.PiProviderKeys {
			provider = strings.ToLower(strings.TrimSpace(provider))
			key = strings.TrimSpace(key)
			if provider != "" && key != "" {
				clean[provider] = key
			}
		}
		if len(clean) > 0 {
			m["pi_provider_keys"] = clean
		}
	}
	return m, true, nil
}

// deriveSecretsKey derives the AES-256 key from AUTH_SECRET using HMAC-SHA256.
// Must match the derivation in server/secrets_routes.go.
// It returns nil without AUTH_SECRET: the old public default key is gone, so nothing can be
// decrypted with a key anyone could know. Trimmed like the server's own AUTH_SECRET reader.
func deriveSecretsKey() []byte {
	secret := strings.TrimSpace(os.Getenv("AUTH_SECRET"))
	if secret == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("secrets-encryption-key"))
	return mac.Sum(nil)
}

// decryptWithAAD decrypts AES-256-GCM encrypted data with the given AAD string.
func decryptWithAAD(data []byte, aad string) ([]byte, error) {
	key := deriveSecretsKey()
	if key == nil {
		return nil, fmt.Errorf("AUTH_SECRET is not set")
	}
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
	return aesGCM.Open(nil, nonce, ciphertext, []byte(aad))
}
