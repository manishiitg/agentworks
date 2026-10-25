package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildProviderAPIKeysFromEnvUsesClaudeCodeSetupToken(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "setup-token-for-test")

	keys := buildProviderAPIKeysFromEnv()
	if keys.ClaudeCodeOAuthToken == nil || *keys.ClaudeCodeOAuthToken != "setup-token-for-test" {
		t.Fatalf("ClaudeCodeOAuthToken = %#v, want configured setup token", keys.ClaudeCodeOAuthToken)
	}
}

func TestLLMDiscoveryHTTPShowsCursorLoginRequired(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "cursor-cli")
	withFakeExecutable(t, "cursor-agent")
	withCursorStatusJSON(t, `{"status":"unauthenticated","isAuthenticated":false,"message":"Not logged in"}`, nil)

	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodGet, "/api/llm-config/discovery", nil)
	rec := httptest.NewRecorder()
	api.handleDiscoverLLMSetup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discovery status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var response llmDiscoveryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if len(response.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(response.Candidates), response.Candidates)
	}
	candidate := response.Candidates[0]
	if candidate.Provider != "cursor-cli" {
		t.Fatalf("provider = %q, want cursor-cli", candidate.Provider)
	}
	if candidate.RuntimeAvailable == nil || !*candidate.RuntimeAvailable {
		t.Fatalf("runtime_available = %v, want true", candidate.RuntimeAvailable)
	}
	if candidate.AuthConfigured {
		t.Fatal("auth_configured = true, want false")
	}
	if candidate.Usable {
		t.Fatal("usable = true, want false for logged-out Cursor CLI")
	}
	if !strings.Contains(candidate.SetupHint, "cursor-agent login") {
		t.Fatalf("setup_hint = %q, want cursor-agent login hint", candidate.SetupHint)
	}
}

func TestBuildLLMDiscoveryNeverOffersDirectAPIProviders(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "openai,anthropic,vertex")
	t.Setenv("OPENAI_API_KEY", "sk-test")

	response := buildLLMDiscovery(context.Background())
	for _, candidate := range response.Candidates {
		if !llmguard.IsCodingAgentProvider(candidate.Provider) {
			t.Fatalf("discovery offered non-CLI provider %q", candidate.Provider)
		}
	}
}

func TestMuseCLIIsPublishedAsCodingAgent(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "muse-cli")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("META_API_KEY", "")

	if !isPublishedLLMProviderAllowed("muse-cli") {
		t.Fatal("muse-cli should be allowed in published provider lists")
	}
	foundSupported := false
	for _, provider := range getSupportedProviders() {
		if provider == "muse-cli" {
			foundSupported = true
		}
	}
	if !foundSupported {
		t.Fatalf("supported providers missing muse-cli: %v", getSupportedProviders())
	}

	response := buildLLMDiscovery(context.Background())
	if len(response.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(response.Candidates), response.Candidates)
	}
	candidate := response.Candidates[0]
	if candidate.Provider != "muse-cli" {
		t.Fatalf("provider = %q, want muse-cli", candidate.Provider)
	}
	if candidate.RuntimeCommand != "muse" {
		t.Fatalf("runtime_command = %q, want muse", candidate.RuntimeCommand)
	}
	if candidate.RuntimeAvailable == nil || *candidate.RuntimeAvailable {
		t.Fatalf("runtime_available = %v, want false for missing muse runtime", candidate.RuntimeAvailable)
	}
	if candidate.Usable {
		t.Fatal("usable = true, want false when muse runtime and auth are missing")
	}
	if got := providerDisplayLabel("muse-cli"); got != "Muse" {
		t.Fatalf("display label = %q, want Muse", got)
	}
	if got := discoverySetupHint("muse-cli", true); !strings.Contains(got, "muse") {
		t.Fatalf("install hint = %q, want muse mention", got)
	}
	if got := discoverySetupHint("muse-cli", false); !strings.Contains(got, "META_API_KEY") {
		t.Fatalf("auth hint = %q, want META_API_KEY mention", got)
	}
}

func TestPiCLIIsPublishedAsCodingAgent(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "pi-cli")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	if !isPublishedLLMProviderAllowed("pi-cli") {
		t.Fatal("pi-cli should be allowed in published provider lists")
	}
	foundSupported := false
	for _, provider := range getSupportedProviders() {
		if provider == "pi-cli" {
			foundSupported = true
		}
	}
	if !foundSupported {
		t.Fatalf("supported providers missing pi-cli: %v", getSupportedProviders())
	}

	response := buildLLMDiscovery(context.Background())
	if len(response.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(response.Candidates), response.Candidates)
	}
	candidate := response.Candidates[0]
	if candidate.Provider != "pi-cli" {
		t.Fatalf("provider = %q, want pi-cli", candidate.Provider)
	}
	if candidate.RuntimeCommand != "pi" {
		t.Fatalf("runtime_command = %q, want pi", candidate.RuntimeCommand)
	}
	if candidate.RuntimeAvailable == nil || *candidate.RuntimeAvailable {
		t.Fatalf("runtime_available = %v, want false for missing pi/npx runtime", candidate.RuntimeAvailable)
	}
	if candidate.Usable {
		t.Fatal("usable = true, want false when pi/npx runtime is missing")
	}
}

// TestIsPublishedLLMProviderAllowedDerivesFromRegistry pins the registry
// migration: the gate must accept exactly the shared ValidateProvider set
// (so new coding CLIs validate without an agent_go edit) while still
// rejecting retired media providers, empty, and unknown names.
func TestIsPublishedLLMProviderAllowedCodingAgentsOnly(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "pi-cli", "muse-cli"} {
		if !isPublishedLLMProviderAllowed(provider) {
			t.Errorf("%q should be allowed", provider)
		}
	}
	for _, provider := range []string{
		"", "  ", "nope", "elevenlabs", "deepgram",
		"bedrock", "openai", "vertex", "anthropic", "azure",
		"openrouter", "z-ai", "kimi", "minimax", "minimax-coding-plan",
	} {
		if isPublishedLLMProviderAllowed(provider) {
			t.Errorf("%q should be rejected", provider)
		}
	}
	if !isPublishedLLMProviderAllowed("  Claude-Code ") {
		t.Error("provider matching should be case- and whitespace-insensitive")
	}
}

func TestAgyAlwaysOfferedIncludingItsServerAccount(t *testing.T) {
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "")
	t.Setenv("AGY_ALPHA", "")
	for _, mode := range []string{"", "true"} {
		t.Setenv("MULTI_USER_MODE", mode)
		if !isPublishedLLMProviderAllowed("agy-cli") {
			t.Fatalf("AGY publication refused in mode %q", mode)
		}
		found := false
		for _, provider := range serverAccountProviders() {
			found = found || provider == "agy-cli"
		}
		if !found {
			t.Fatalf("AGY server account hidden in mode %q", mode)
		}
	}
}

// TestProviderManifestMarksDeprecatedAPIModelProviders is the regression for
// the 2026-08-20 direct-API-transport deprecation
// (docs/design/api_transport_vs_pi_tradeoff.md). Uses the real HTTP handler,
// matching TestProviderManifestMarksDeprecatedCodingAgents's pattern.
func TestProviderManifestOmitsDirectAPIProviders(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "openai,anthropic,vertex,bedrock,azure,minimax,elevenlabs,deepgram,pi-cli")
	t.Setenv("PATH", t.TempDir())

	api := &StreamingAPI{}
	req := httptest.NewRequest(http.MethodGet, "/api/llm-config/providers", nil)
	rec := httptest.NewRecorder()
	api.handleGetProviderManifest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	sawPi := false
	for _, p := range resp.Providers {
		if !llmguard.IsCodingAgentProvider(p.ID) {
			t.Errorf("manifest lists non-CLI provider %q", p.ID)
		}
		if p.ID == "pi-cli" {
			sawPi = true
		}
	}
	if !sawPi {
		t.Error("pi-cli missing from manifest")
	}
}
