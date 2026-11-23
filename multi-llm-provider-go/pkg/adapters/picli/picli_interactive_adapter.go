package picli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"fmt"

	"os"
	"os/exec"

	"sort"

	"strings"

	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

const (
	defaultPiInteractiveIdleTimeout = codingtimeout.DefaultPersistentSessionIdle

	EnvPiBinary = "PI_BIN"

	EnvPiInteractiveIdleTimeoutSeconds = "PI_CLI_INTERACTIVE_IDLE_TIMEOUT_SECONDS"

	EnvPiInteractiveUseNpxFallback   = "PI_CLI_USE_NPX_FALLBACK"
	EnvPiInteractiveNpmIgnoreScripts = "PI_CLI_NPM_IGNORE_SCRIPTS"

	EnvPiNodeOptions       = "PI_CLI_NODE_OPTIONS"
	EnvPiNodeMaxOldSpaceMB = "PI_CLI_NODE_MAX_OLD_SPACE_MB"

	defaultPiNodeMaxOldSpaceMB     = "4096"
	defaultPiInteractiveNpxPackage = "@earendil-works/pi-coding-agent"
	defaultPiProvider              = "google"
	defaultPiModel                 = "gemini-3.8-flash"
)

// piEffectiveThinkingLevel is the --thinking level for a launch: the deployment's pin for this model when its template fixes
// one, else what the caller asked for.
func piEffectiveThinkingLevel(provider, model string, opts *llmtypes.CallOptions) string {
	if tmpl, err := LoadPiAgentTemplate(); err == nil {
		if pinned, ok := tmpl.PinnedThinkingLevel(provider, model); ok {
			return pinned
		}
	}
	return piThinkingLevelFromOptions(opts)
}

func piThinkingLevelFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil {
		return ""
	}
	level := strings.ToLower(strings.TrimSpace(opts.ReasoningEffort))
	if level == "" {
		level = strings.ToLower(strings.TrimSpace(opts.ThinkingLevel))
	}
	switch level {
	case "none", "off":
		return "off"
	case "minimal", "low", "medium", "high", "xhigh":
		return level
	case "max":
		return "xhigh"
	default:
		return ""
	}
}

func piNodeOptionsEnv() []string {
	if configured := strings.TrimSpace(os.Getenv(EnvPiNodeOptions)); configured != "" {
		return []string{"NODE_OPTIONS=" + configured}
	}
	existing := strings.TrimSpace(os.Getenv("NODE_OPTIONS"))
	if strings.Contains(existing, "--max-old-space-size") {
		return nil
	}
	maxOldSpaceMB := strings.TrimSpace(os.Getenv(EnvPiNodeMaxOldSpaceMB))
	if maxOldSpaceMB == "" {
		maxOldSpaceMB = defaultPiNodeMaxOldSpaceMB
	}
	switch strings.ToLower(maxOldSpaceMB) {
	case "0", "false", "no", "off", "disabled":
		return nil
	}
	value := "--max-old-space-size=" + maxOldSpaceMB
	if existing != "" {
		value = existing + " " + value
	}
	return []string{"NODE_OPTIONS=" + value}
}

func normalizePiMCPConfig(configJSON string) ([]byte, error) {
	decoder := json.NewDecoder(strings.NewReader(configJSON))
	decoder.UseNumber()
	var config map[string]interface{}
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("pi MCP config is not valid JSON: %w", err)
	}
	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok || len(servers) == 0 {
		return nil, fmt.Errorf("pi MCP config must contain a non-empty mcpServers object")
	}

	delete(config, "settings")
	config["autoEnableCodemode"] = false
	for name, rawServer := range servers {
		server, ok := rawServer.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("pi MCP config server %s must be an object", name)
		}
		delete(server, "directTools")
		delete(server, "lifecycle")
		if _, exists := server["exposure"]; !exists {
			server["exposure"] = "direct"
		}
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode Pi MCP config: %w", err)
	}
	return append(body, '\n'), nil
}

func cleanPiWorkingDirKey(workingDir string) string { return pathidentity.Key(workingDir) }

func piMCPConfigFingerprint(config string) string {
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(config), &decoded); err == nil {
		if mcpServers, ok := decoded["mcpServers"].(map[string]interface{}); ok {
			for _, srv := range mcpServers {
				if srvMap, ok := srv.(map[string]interface{}); ok {
					if env, ok := srvMap["env"].(map[string]interface{}); ok {
						delete(env, "MCP_SESSION_ID")
						delete(env, "MCP_VIRTUAL_SCOPE_ID")

						delete(env, "MCP_API_TOKEN")
						delete(env, "MCP_AUTH")

						delete(env, "MCP_READY_FILE")
						if apiURL, ok := env["MCP_API_URL"].(string); ok {
							if idx := strings.Index(apiURL, "/s/"); idx != -1 {
								env["MCP_API_URL"] = apiURL[:idx]
							}
						}
					}
				}
			}
		}
		if canonical, err := json.Marshal(decoded); err == nil {
			config = string(canonical)
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(config)))
	return hex.EncodeToString(sum[:])
}

func piCommandPrefix() ([]string, []string, error) {
	if configured := strings.TrimSpace(os.Getenv(EnvPiBinary)); configured != "" {
		return []string{configured}, piNpmEnv(), nil
	}
	if path, err := exec.LookPath("pi"); err == nil {
		return []string{path}, piNpmEnv(), nil
	}
	if piNpxFallbackEnabled() {
		if path, err := exec.LookPath("npx"); err == nil {
			return []string{path, "--yes", defaultPiInteractiveNpxPackage}, piNpmEnv(), nil
		}
	}
	return nil, nil, fmt.Errorf("pi not found in PATH and npx fallback is unavailable. Install with `npm install -g --ignore-scripts @earendil-works/pi-coding-agent`, set PI_BIN, or install npx")
}

func piNpxFallbackEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvPiInteractiveUseNpxFallback))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func piNpmEnv() []string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvPiInteractiveNpmIgnoreScripts))) {
	case "0", "false", "no", "off":
		return nil
	default:
		return []string{"npm_config_ignore_scripts=true"}
	}
}

func piAPIKeyEnv(provider, apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "google", "google-vertex":
		return []string{"GEMINI_API_KEY=" + apiKey, "GOOGLE_API_KEY=" + apiKey, "PI_API_KEY=" + apiKey}
	case "openai":
		return []string{"OPENAI_API_KEY=" + apiKey}
	case "anthropic":
		return []string{"ANTHROPIC_API_KEY=" + apiKey}
	case "openrouter":
		return []string{"OPENROUTER_API_KEY=" + apiKey}
	case "deepseek":
		return []string{"DEEPSEEK_API_KEY=" + apiKey}
	case "nvidia":
		return []string{"NVIDIA_API_KEY=" + apiKey}
	case "mistral":
		return []string{"MISTRAL_API_KEY=" + apiKey}
	case "groq":
		return []string{"GROQ_API_KEY=" + apiKey}
	case "cerebras":
		return []string{"CEREBRAS_API_KEY=" + apiKey}
	case "xai":
		return []string{"XAI_API_KEY=" + apiKey}
	case "zai":
		return []string{"ZAI_API_KEY=" + apiKey}
	case "zai-coding-cn":
		return []string{"ZAI_CODING_CN_API_KEY=" + apiKey}
	case "opencode", "opencode-go":
		return []string{"OPENCODE_API_KEY=" + apiKey}
	case "fireworks":
		return []string{"FIREWORKS_API_KEY=" + apiKey}
	case "together":
		return []string{"TOGETHER_API_KEY=" + apiKey}
	case "kimi-coding":
		return []string{"KIMI_API_KEY=" + apiKey}
	case "moonshotai", "moonshotai-cn":
		return []string{"MOONSHOT_API_KEY=" + apiKey}
	case "minimax":
		return []string{"MINIMAX_API_KEY=" + apiKey}
	case "minimax-cn":
		return []string{"MINIMAX_CN_API_KEY=" + apiKey}
	case "vercel-ai-gateway":
		return []string{"AI_GATEWAY_API_KEY=" + apiKey}
	default:
		normalized := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(provider))))
		if normalized == "" {
			return []string{"PI_API_KEY=" + apiKey}
		}
		return []string{normalized + "_API_KEY=" + apiKey}
	}
}

func piBridgeShellEnvFromMCPConfig(configJSON string) []string {
	bridgeEnv := piBridgeEnvFromMCPConfig(configJSON)
	if len(bridgeEnv) == 0 {
		return nil
	}

	apiURL := strings.TrimRight(strings.TrimSpace(bridgeEnv["MCP_API_URL"]), "/")
	sessionID := strings.TrimSpace(bridgeEnv["MCP_SESSION_ID"])
	scopedURL := piSessionScopedMCPURL(apiURL, sessionID)
	token := strings.TrimSpace(bridgeEnv["MCP_API_TOKEN"])

	out := map[string]string{}
	if scopedURL != "" {
		out["MCP_API_URL"] = scopedURL
		out["MCP_MCP"] = scopedURL + "/tools/mcp"
		out["MCP_CUSTOM"] = scopedURL + "/tools/custom"
		out["MCP_VIRTUAL"] = scopedURL + "/tools/virtual"
	}
	if token != "" {
		out["MCP_API_TOKEN"] = token
		out["MCP_AUTH"] = "Authorization: Bearer " + token
	}
	if sessionID != "" {
		out["MCP_SESSION_ID"] = sessionID
	}
	if virtualScopeID := strings.TrimSpace(bridgeEnv["MCP_VIRTUAL_SCOPE_ID"]); virtualScopeID != "" {
		out["MCP_VIRTUAL_SCOPE_ID"] = virtualScopeID
	}
	if len(out) == 0 {
		return nil
	}

	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+out[key])
	}
	return env
}

func piBridgeEnvFromMCPConfig(configJSON string) map[string]string {
	decoder := json.NewDecoder(strings.NewReader(configJSON))
	decoder.UseNumber()
	var config map[string]interface{}
	if err := decoder.Decode(&config); err != nil {
		return nil
	}
	servers, _ := config["mcpServers"].(map[string]interface{})
	rawBridge, _ := servers["api-bridge"].(map[string]interface{})
	rawEnv, _ := rawBridge["env"].(map[string]interface{})
	if len(rawEnv) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"MCP_API_URL":          true,
		"MCP_API_TOKEN":        true,
		"MCP_SESSION_ID":       true,
		"MCP_VIRTUAL_SCOPE_ID": true,
	}
	env := make(map[string]string, len(allowed))
	for key, value := range rawEnv {
		if !allowed[key] {
			continue
		}
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			env[key] = s
		}
	}
	return env
}

func piSessionScopedMCPURL(apiURL, sessionID string) string {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" || strings.TrimSpace(sessionID) == "" || strings.Contains(apiURL, "/s/") {
		return apiURL
	}
	return apiURL + "/s/" + strings.TrimSpace(sessionID)
}

// piGenericBridgeToolName reports whether name is pi's own generic wrapper
// label for an MCP-bridge-routed tool call (e.g. "mcp"), instead of the real
// underlying tool. Mirrors the detection this file's embedded output-guard
// extension already uses (looksLikeMcpResult, piMCPOutputGuardExtensionSource
// below) -- kept in sync deliberately, not shared, since one is Go and the
// other TypeScript embedded as a string.
func piGenericBridgeToolName(name string) bool {
	return name == "mcp" ||
		strings.HasPrefix(name, "mcp_") ||
		strings.HasPrefix(name, "api_bridge_") ||
		strings.Contains(name, "_mcp_")
}

// piRealToolNameFromBridgeArgs recovers the real tool pi's MCP bridge wrapper
// actually called, from the wrapper call's own arguments. Confirmed live
// against a real session transcript: a bridge-routed tool call's arguments
// look like {"tool": "api_bridge_execute_shell_command", "args": "{...}"},
// because pi's own bridge exposes one generic "mcp" tool to the model whose
// arguments name the real target -- the model's tool-call decision always
// names a specific tool, it just isn't the top-level "toolName" field pi
// reports on tool_execution_start/end.
func piRealToolNameFromBridgeArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var payload struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(args, &payload); err == nil && strings.TrimSpace(payload.Tool) != "" {
		return strings.TrimSpace(payload.Tool)
	}
	// The wrapper's own args object was large enough to get truncated before
	// the "tool" field could round-trip normally -- pi's bridge puts "tool"
	// first, so it usually survives inside the truncated preview string even
	// when the (much larger) nested args string doesn't. The preview is a
	// truncated JSON PREFIX, not complete/parseable JSON on its own, so
	// recover "tool" with a plain substring search rather than a JSON parse.
	var truncated piMarkerTruncatedPayload
	if err := json.Unmarshal(args, &truncated); err != nil || !truncated.Truncated {
		return ""
	}
	return piExtractStringField(truncated.Preview, "tool")
}

// piExtractStringField finds `"<field>":"<value>"` inside text that may be a
// truncated (incomplete) JSON prefix, where json.Unmarshal would fail. Not a
// general JSON parser -- only handles a plain, unescaped string value, which
// is all a tool name ever is.
func piExtractStringField(text, field string) string {
	needle := `"` + field + `"`
	idx := strings.Index(text, needle)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(needle):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// piResultTextFromMarker renders a tool_execution_end marker's real result
// (pi's own `result: any` event field, forwarded verbatim by the marker
// extension) as display text, instead of the hardcoded "ok"/"error"
// placeholder this adapter used before the real field was even read. A bare
// JSON string unmarshals into text directly; anything else is displayed as
// compact JSON so at least the real shape is visible.
func piResultTextFromMarker(result json.RawMessage) string {
	trimmed := strings.TrimSpace(string(result))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var asString string
	if err := json.Unmarshal(result, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	return trimmed
}

// CleanupPiCLIInteractiveSessions removes Pi CLI tmux sessions registered by
// this process.
func CleanupPiCLIInteractiveSessions(ctx context.Context) error {
	closePiRPCSessions()
	return nil
}

// ClosePiCLIInteractiveSessionForOwner closes the persistent Pi session for
// the given owner, if one exists.
func ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	closePiRPCSession(strings.TrimSpace(ownerSessionID))
}

// InteractiveSessionRegistered reports whether the owner's Pi TUI is
// registered — the live-injection transport exists. Cheap registry lookup
// (no tmux round-trip) for steer gating; delivery re-verifies liveness.
func InteractiveSessionRegistered(ownerSessionID string) bool {
	s := piRPCSessionForOwner(ownerSessionID)
	if s == nil {
		return false
	}
	s.mu.Lock()
	r := s.rpc
	s.mu.Unlock()
	if r == nil {
		return false
	}
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

// SendPiInteractiveInput sends user input to a live Pi interactive session.
func SendPiInteractiveInput(ctx context.Context, ownerSessionID, message string) error {
	return sendPiRPCInput(ctx, ownerSessionID, message)
}

func resolvePiProviderModel(modelID, providerOverride string) (string, string) {
	provider := strings.TrimSpace(providerOverride)
	model := strings.TrimSpace(modelID)
	if model == "" || model == "pi-cli" {
		model = DefaultModelID
	}
	if slash := strings.Index(model, "/"); slash > 0 {
		if provider == "" {
			provider = strings.TrimSpace(model[:slash])
		}
		model = strings.TrimSpace(model[slash+1:])
	}
	if provider == "" {
		provider = defaultPiProvider
	}
	if model == "" {
		model = defaultPiModel
	}
	return provider, model
}

func intPtr(v int) *int {
	return &v
}

func piInteractiveIdleTimeout() time.Duration {
	if parsed, ok := piDurationFromEnv(EnvPiInteractiveIdleTimeoutSeconds); ok {
		return parsed
	}
	return defaultPiInteractiveIdleTimeout
}

func piDurationFromEnv(key string) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	seconds, err := time.ParseDuration(raw + "s")
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return seconds, true
}

func generatePiNativeSessionID() string {
	return "mlp-pi-" + piRandomHex(8)
}

func isValidPiNativeSessionID(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if !isPiSessionIDEdgeChar(sessionID[0]) || !isPiSessionIDEdgeChar(sessionID[len(sessionID)-1]) {
		return false
	}
	for i := 0; i < len(sessionID); i++ {
		ch := sessionID[i]
		if isPiSessionIDEdgeChar(ch) || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func isPiSessionIDEdgeChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
}

func piRandomHex(n int) string {
	if n <= 0 {
		n = 4
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

type piMarkerTruncatedPayload struct {
	Truncated bool   `json:"mlpMarkerTruncated"`
	Preview   string `json:"preview"`
}
