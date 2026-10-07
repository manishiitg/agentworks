package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	llm "github.com/manishiitg/multi-llm-provider-go"
)

func TestListProviderModelsIncludesProviderTierDefaults(t *testing.T) {
	expected, ok := llm.GetCodingAgentDefaultTierModels(llm.ProviderClaudeCode)
	if !ok {
		t.Fatal("Claude Code provider defaults are unavailable")
	}

	var payload struct {
		Provider          string                            `json:"provider"`
		Models            []json.RawMessage                 `json:"models"`
		DefaultTierModels *llm.CodingAgentDefaultTierModels `json:"default_tier_models"`
	}
	if err := json.Unmarshal([]byte(listProviderModelsJSON(string(llm.ProviderClaudeCode))), &payload); err != nil {
		t.Fatalf("decode provider models: %v", err)
	}
	if payload.Provider != string(llm.ProviderClaudeCode) {
		t.Fatalf("provider = %q, want %q", payload.Provider, llm.ProviderClaudeCode)
	}
	if len(payload.Models) == 0 {
		t.Fatal("provider model catalog is empty")
	}
	if payload.DefaultTierModels == nil {
		t.Fatal("default_tier_models missing from provider model response")
	}
	if payload.DefaultTierModels.High.ModelID != expected.High.ModelID {
		t.Fatalf("high default = %q, want %q", payload.DefaultTierModels.High.ModelID, expected.High.ModelID)
	}
	if payload.DefaultTierModels.Pulse.ModelID != expected.Pulse.ModelID {
		t.Fatalf("pulse default = %q, want %q", payload.DefaultTierModels.Pulse.ModelID, expected.Pulse.ModelID)
	}
}

func TestProviderAuthConfiguredTreatsPiProviderKeysAsPiAuth(t *testing.T) {
	configured, source := providerAuthConfigured("pi-cli", &llm.ProviderAPIKeys{
		PiProviderKeys: map[string]string{"zai": "zai-key"},
	})
	if !configured {
		t.Fatalf("pi-cli auth configured = false, want true")
	}
	if source != "Provider-specific Pi API key or workspace provider auth" {
		t.Fatalf("pi-cli auth source = %q", source)
	}
}

func TestProviderAuthConfiguredDetectsPiLocalLoginFromAvailableModels(t *testing.T) {
	original := listPiCLIModelsFn
	listPiCLIModelsFn = func() ([]dynamicModelEntry, error) {
		return []dynamicModelEntry{{ModelID: "google/gemini-3.8-flash", Group: "Gemini"}}, nil
	}
	invalidatePiProviderCaches()
	t.Cleanup(func() {
		listPiCLIModelsFn = original
		invalidatePiProviderCaches()
	})

	configured, source := providerAuthConfigured("pi-cli", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("pi-cli auth configured = false, want local Pi login detected from --list-models")
	}
	if source != "Pi provider login or workspace provider auth" {
		t.Fatalf("pi-cli auth source = %q", source)
	}
}

func TestRetiredMediaProvidersAreNeitherCollectableNorAdvertised(t *testing.T) {
	keys := &StoredProviderKeys{}
	for _, provider := range []string{"minimax", "elevenlabs", "deepgram"} {
		if setStoredProviderAPIKey(keys, provider, "test-key") {
			t.Errorf("setStoredProviderAPIKey(%q) accepted a retired media provider", provider)
		}
	}

	capabilities := buildLLMCapabilities(context.Background(), "all", false)
	for _, capability := range []string{
		"read_image", "generate_image", "generate_video",
		"text_to_speech", "speech_to_text", "generate_music",
	} {
		if _, ok := capabilities[capability]; ok {
			t.Errorf("retired media capability %q was advertised", capability)
		}
	}

	if got := buildLLMCapabilities(context.Background(), "text_to_speech", false); len(got) != 2 {
		t.Fatalf("retired capability response = %#v, want only schema_version and notes", got)
	}
}

func TestBuildLLMDiscoveryShowsCursorLoginRequired(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "cursor-cli")
	withFakeExecutable(t, "cursor-agent")
	withCursorStatusJSON(t, `{"status":"unauthenticated","isAuthenticated":false,"message":"Not logged in"}`, nil)

	configured, source := providerAuthConfigured("cursor-cli", &llm.ProviderAPIKeys{})
	if configured {
		t.Fatal("cursor-cli auth configured = true, want false for logged-out CLI")
	}
	if source != "Cursor CLI login or CURSOR_API_KEY/workspace provider auth" {
		t.Fatalf("cursor-cli auth source = %q", source)
	}

	response := buildLLMDiscovery(context.Background())
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
	if !containsLLMCapabilityString(candidate.Options, "composer-2.5") {
		t.Fatalf("options = %v, want composer-2.5 available as explicit Cursor model", candidate.Options)
	}
}

func TestProviderAuthConfiguredAcceptsCursorWorkspaceKey(t *testing.T) {
	key := "cursor-key"
	withCursorStatusJSON(t, `{"status":"unauthenticated","isAuthenticated":false}`, nil)

	configured, source := providerAuthConfigured("cursor-cli", &llm.ProviderAPIKeys{
		CursorCLI: &key,
	})
	if !configured {
		t.Fatal("cursor-cli auth configured = false, want true for workspace key")
	}
	if source != "CURSOR_API_KEY or workspace provider auth" {
		t.Fatalf("cursor-cli auth source = %q", source)
	}
}

func TestProviderAuthConfiguredAcceptsCursorAuthenticatedJSON(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withFakeExecutable(t, "cursor-agent")
	withCursorStatusJSON(t, `{"status":"authenticated","isAuthenticated":true}`, nil)

	configured, source := providerAuthConfigured("cursor-cli", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("cursor-cli auth configured = false, want true for authenticated local Cursor CLI")
	}
	if source != "Cursor CLI login or CURSOR_API_KEY/workspace provider auth" {
		t.Fatalf("cursor-cli auth source = %q", source)
	}
}

func TestProviderAuthConfiguredAcceptsCursorPlainLoggedInStatus(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withFakeExecutable(t, "cursor-agent")
	withCursorStatusJSON(t, `Logged in as manish@example.com`, nil)

	configured, _ := providerAuthConfigured("cursor-cli", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("cursor-cli auth configured = false, want true for plain logged-in Cursor CLI status")
	}
}

func TestProviderAuthConfiguredUsesActualClaudeLoginStatus(t *testing.T) {
	withFakeExecutable(t, "claude")
	withClaudeStatus(t, `{"loggedIn":false,"authMethod":"none"}`, nil)
	configured, _ := providerAuthConfigured("claude-code", &llm.ProviderAPIKeys{})
	if configured {
		t.Fatal("claude-code auth configured = true for a logged-out installed CLI")
	}

	withClaudeStatus(t, `{"loggedIn":true,"authMethod":"claude.ai"}`, nil)
	configured, _ = providerAuthConfigured("claude-code", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("claude-code auth configured = false for a confirmed CLI login")
	}
}

func TestProviderAuthConfiguredUsesActualCodexLoginStatus(t *testing.T) {
	withFakeExecutable(t, "codex")
	withCodexStatus(t, "Not logged in", nil)
	configured, _ := providerAuthConfigured("codex-cli", &llm.ProviderAPIKeys{})
	if configured {
		t.Fatal("codex-cli auth configured = true for a logged-out installed CLI")
	}

	withCodexStatus(t, "Logged in using ChatGPT", nil)
	configured, _ = providerAuthConfigured("codex-cli", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("codex-cli auth configured = false for a confirmed CLI login")
	}
}

func TestProviderAuthConfiguredUsesActualAgyLoginStatus(t *testing.T) {
	withFakeExecutable(t, "agy")
	// agy exits 0 even when logged out: the sign-in marker in the text is
	// the only signal.
	withAgyModels(t, "Fetching available models...\nError: Please sign in to view available models. Launch the CLI without arguments to sign in.\n", nil)
	configured, _ := providerAuthConfigured("agy-cli", &llm.ProviderAPIKeys{})
	if configured {
		t.Fatal("agy-cli auth configured = true for a logged-out installed CLI")
	}

	withAgyModels(t, "Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\n", nil)
	configured, _ = providerAuthConfigured("agy-cli", &llm.ProviderAPIKeys{})
	if !configured {
		t.Fatal("agy-cli auth configured = false for a confirmed CLI login")
	}
}

func withAgyModels(t *testing.T, output string, err error) {
	t.Helper()
	previous := agyCLIModelsCommand
	resetCLIAuthProbeCache(&agyCLIAuthProbeCache)
	agyCLIModelsCommand = func(context.Context) ([]byte, error) { return []byte(output), err }
	t.Cleanup(func() {
		agyCLIModelsCommand = previous
		resetCLIAuthProbeCache(&agyCLIAuthProbeCache)
	})
}

// PLAT-666: agy chats get GEMINI_API_KEY/GOOGLE_API_KEY from the service
// environment; the login probe must see the same keys, or an API-key-mode host
// shows Needs authentication while its chats work. Other secrets stay out.
func TestAgyCLIProbeEnvCarriesTheChatGeminiKeys(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-test-key")
	t.Setenv("GOOGLE_API_KEY", "google-test-key")
	t.Setenv("ANTHROPIC_API_KEY", "must-not-leak")
	env := strings.Join(agyCLIProbeEnv(), "\n")
	for _, want := range []string{"GEMINI_API_KEY=gemini-test-key", "GOOGLE_API_KEY=google-test-key"} {
		if !strings.Contains(env, want) {
			t.Fatalf("agy probe env is missing %s", strings.SplitN(want, "=", 2)[0])
		}
	}
	if strings.Contains(env, "ANTHROPIC_API_KEY") {
		t.Fatal("agy probe env leaked an unrelated secret")
	}
}

func TestCursorCLIAuthProbeKeepsLastConfirmedLoginOnTransientFailure(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withFakeExecutable(t, "cursor-agent")

	previous := cursorCLIStatusJSON
	resetCursorCLIAuthProbeCache()
	var probeCalls atomic.Int32
	cursorCLIStatusJSON = func(context.Context) ([]byte, error) {
		if probeCalls.Add(1) == 1 {
			return []byte(`{"status":"authenticated","isAuthenticated":true}`), nil
		}
		return nil, context.DeadlineExceeded
	}
	t.Cleanup(func() {
		cursorCLIStatusJSON = previous
		resetCursorCLIAuthProbeCache()
	})

	authenticated, conclusive := cursorCLILocalAuthState()
	if !authenticated || !conclusive {
		t.Fatalf("first probe = authenticated:%v conclusive:%v, want true/true", authenticated, conclusive)
	}

	// A stale entry is served immediately; the transient failure happens in the
	// background refresh and must not flip a confirmed login to logged out.
	cursorCLIAuthProbeCache.Lock()
	cursorCLIAuthProbeCache.checkedAt = time.Now().Add(-31 * time.Second)
	cursorCLIAuthProbeCache.Unlock()
	authenticated, conclusive = cursorCLILocalAuthState()
	if !authenticated || !conclusive {
		t.Fatalf("stale read = authenticated:%v conclusive:%v, want cached true/true", authenticated, conclusive)
	}
	waitForAuthRefresh(t, &cursorCLIAuthProbeCache)
	if got := probeCalls.Load(); got != 2 {
		t.Fatalf("status probe calls = %d, want 2", got)
	}
	authenticated, conclusive = cursorCLILocalAuthState()
	if !authenticated || !conclusive {
		t.Fatalf("after transient refresh = authenticated:%v conclusive:%v, want preserved true/true", authenticated, conclusive)
	}
}

func waitForAuthRefresh(t *testing.T, cache *cliAuthProbeCache) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cache.Lock()
		done := !cache.refreshing && time.Since(cache.checkedAt) < cliAuthProbeTTL
		cache.Unlock()
		if done {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background auth refresh did not finish")
}

func TestValidateCursorCLIReportsLoginRequiredBeforeTmuxRun(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("CURSOR_API_KEY", "")
	withFakeExecutable(t, "cursor-agent")
	withFakeExecutable(t, "tmux")
	withCursorStatusJSON(t, `{"status":"unauthenticated","isAuthenticated":false,"message":"Not logged in"}`, nil)

	resp := validateProviderConfig(llm.APIKeyValidationRequest{
		Provider: "cursor-cli",
		ModelID:  "composer-2.5",
	})
	if resp.Valid {
		t.Fatal("valid = true, want false for logged-out Cursor CLI")
	}
	if !strings.Contains(resp.Message, "cursor-agent login") {
		t.Fatalf("message = %q, want cursor-agent login hint", resp.Message)
	}
}

func withFakeExecutable(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func withCursorStatusJSON(t *testing.T, output string, err error) {
	t.Helper()
	previous := cursorCLIStatusJSON
	resetCursorCLIAuthProbeCache()
	cursorCLIStatusJSON = func(context.Context) ([]byte, error) {
		return []byte(output), err
	}
	t.Cleanup(func() {
		cursorCLIStatusJSON = previous
		resetCursorCLIAuthProbeCache()
	})
}

func withClaudeStatus(t *testing.T, output string, err error) {
	t.Helper()
	previous := claudeCLIAuthStatusCommand
	resetCLIAuthProbeCache(&claudeCLIAuthProbeCache)
	claudeCLIAuthStatusCommand = func(context.Context) ([]byte, error) { return []byte(output), err }
	t.Cleanup(func() {
		claudeCLIAuthStatusCommand = previous
		resetCLIAuthProbeCache(&claudeCLIAuthProbeCache)
	})
}

func withCodexStatus(t *testing.T, output string, err error) {
	t.Helper()
	previous := codexCLIAuthStatusCommand
	resetCLIAuthProbeCache(&codexCLIAuthProbeCache)
	codexCLIAuthStatusCommand = func(context.Context) ([]byte, error) { return []byte(output), err }
	t.Cleanup(func() {
		codexCLIAuthStatusCommand = previous
		resetCLIAuthProbeCache(&codexCLIAuthProbeCache)
	})
}

func resetCLIAuthProbeCache(cache *cliAuthProbeCache) {
	cache.Lock()
	defer cache.Unlock()
	cache.checkedAt = time.Time{}
	cache.authenticated = false
	cache.conclusive = false
}

func resetCursorCLIAuthProbeCache() {
	resetCLIAuthProbeCache(&cursorCLIAuthProbeCache)
}

func containsLLMCapabilityString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
