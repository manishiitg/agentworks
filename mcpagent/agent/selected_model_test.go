package mcpagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestLegacyFallbackConfigurationIsDiscarded(t *testing.T) {
	var config AgentLLMConfiguration
	if err := json.Unmarshal([]byte(`{"primary":{"provider":"codex-cli","model_id":"selected"},"fallbacks":[{"provider":"claude-code","model_id":"other"}]}`), &config); err != nil {
		t.Fatal(err)
	}
	a := &Agent{llmConfig: config}
	t.Setenv("CODEX_CLI_FALLBACK_MODELS", "other")
	t.Setenv("CODEX_CLI_CROSS_PROVIDER_FALLBACK_MODELS", "claude-code/other")
	effective := a.getEffectiveLLMConfig()
	encoded, err := json.Marshal(effective)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "fallback") || effective.Primary.Provider != "codex-cli" || effective.Primary.ModelID != "selected" {
		t.Fatalf("legacy fallback survived or selection changed: %s", encoded)
	}
}

func TestQuotaExhaustionDoesNotSwitchProvider(t *testing.T) {
	t.Setenv("CODEX_CLI_FALLBACK_MODELS", "other")
	t.Setenv("CODEX_CLI_CROSS_PROVIDER_FALLBACK_MODELS", "claude-code/other")
	reset := time.Now().Add(time.Hour)
	a := &Agent{provider: llm.ProviderCodexCLI, modelID: "selected-quota-test", logger: loggerv2.NewNoop(), quotaExhaustedModels: map[quotaModelKey]time.Time{modelQuotaKey(LLMModel{Provider: "codex-cli", ModelID: "selected-quota-test"}): reset}}
	_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
	if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted || !llmerrors.RetryAtOrZero(err).Equal(reset) {
		t.Fatalf("quota error/reset was lost: %v", err)
	}
	if a.provider != llm.ProviderCodexCLI || a.modelID != "selected-quota-test" {
		t.Fatalf("selected agent changed: %s/%s", a.provider, a.modelID)
	}
}

func TestInitializationFailureDoesNotUseLegacyBackup(t *testing.T) {
	var config AgentLLMConfiguration
	if err := json.Unmarshal([]byte(`{"primary":{"provider":"unavailable-provider","model_id":"selected"},"fallbacks":[{"provider":"codex-cli","model_id":"backup"}]}`), &config); err != nil {
		t.Fatal(err)
	}
	listener := &recordingAgentEventListener{}
	a := &Agent{provider: llm.Provider(config.Primary.Provider), modelID: config.Primary.ModelID, llmConfig: config, logger: loggerv2.NewNoop(), listeners: []AgentEventListener{listener}}
	_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "unavailable-provider") {
		t.Fatalf("selected initialization failure was lost: %v", err)
	}
	if a.modelID != "selected" || string(a.provider) != "unavailable-provider" {
		t.Fatalf("selection mutated: %s/%s", a.provider, a.modelID)
	}
	for _, event := range listener.events {
		if strings.Contains(string(event.Type), "fallback") || string(event.Type) == "model_change" || string(event.Type) == "retry_attempt" {
			t.Fatalf("unexpected recovery event: %s", event.Type)
		}
	}
}

// A quota refusal belongs to the selected account, in both the current Agent
// and a fresh Agent for the next turn. Reaching initialization's account
// resolver proves B passed the quota gate without a real provider call or
// credentials. The resolver stops the test before any CLI is started.
func TestQuotaCooldownIsScopedToAccount(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, account := range []string{"account-A", ""} {
			name := "local/" + account
			if shared {
				name = "shared/" + account
			}
			t.Run(name, func(t *testing.T) {
				model := LLMModel{Provider: "codex-cli", ModelID: t.Name(), ConnectionID: account}
				key := modelQuotaKey(model)
				t.Cleanup(func() { forgetSharedModelQuotaExhaustion(key) })
				reset := time.Now().Add(time.Hour)
				resolved := ""
				stop := errors.New("test reached account resolution")
				keys := &llm.ProviderAPIKeys{ResolveConnection: func(_ context.Context, provider llm.Provider, connectionID string) (*llm.ProviderAPIKeys, error) {
					if provider != llm.ProviderCodexCLI {
						t.Fatalf("unexpected provider: %s", provider)
					}
					resolved = connectionID
					return nil, stop
				}}
				a := &Agent{llmConfig: AgentLLMConfiguration{Primary: model}, logger: loggerv2.NewNoop(), apiKeys: keys}
				if shared {
					markModelQuotaExhaustedShared(key, reset)
				} else {
					a.quotaExhaustedModels = map[quotaModelKey]time.Time{key: reset}
				}
				checkExhausted := func() {
					t.Helper()
					_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 1)
					if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted || !llmerrors.RetryAtOrZero(err).Equal(reset) {
						t.Fatalf("account A lost its quota/reset: %v", err)
					}
				}
				checkExhausted()
				if resolved != "" {
					t.Fatal("exhausted account reached provider initialization")
				}
				a.llmConfig.Primary.ConnectionID = "account-B"
				_, _, err := generateContentWithRetry(a, context.Background(), nil, nil, 2)
				if !errors.Is(err, stop) || resolved != "account-B" {
					t.Fatalf("account B did not reach its own provider initialization: %v", err)
				}
				resolved = ""
				a.llmConfig.Primary.ConnectionID = account
				if account == "" {
					// Explicit and implicit server accounts share the same cooldown.
					a.llmConfig.Primary.ConnectionID = "global:" + model.Provider
				}
				checkExhausted()
				if resolved != "" {
					t.Fatal("account A lost its cached refusal after switching back")
				}
			})
		}
	}
}

// PLAT-746: a native goal operation can have taken effect before a transport
// failure. Never retry it as a fresh operation or switch its selected program.
func TestNativeGoalOperationIsNotRetried(t *testing.T) {
	calls := 0
	cause := &llmerrors.Error{Kind: llmerrors.KindNetwork, Provider: "codex-cli", Err: errors.New("connection lost after native operation")}
	keys := &llm.ProviderAPIKeys{ResolveConnection: func(context.Context, llm.Provider, string) (*llm.ProviderAPIKeys, error) { calls++; return nil, cause }}
	model := LLMModel{Provider: "codex-cli", ModelID: "goal-test-model", ConnectionID: "goal-account"}
	agent := &Agent{sessionID: "goal-chat", provider: llm.ProviderCodexCLI, modelID: model.ModelID, llmConfig: AgentLLMConfiguration{Primary: model}, logger: loggerv2.NewNoop(), apiKeys: keys}
	ctx := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: "goal-chat", Action: "set", Objective: "finish task"})
	_, _, err := generateContentWithRetry(agent, ctx, nil, nil, 1)
	if !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("goal operation retried: calls=%d err=%v", calls, err)
	}
	if agent.provider != llm.ProviderCodexCLI || agent.modelID != model.ModelID {
		t.Fatal("goal switched its program")
	}
}

// PLAT-746-988: native read/pause/clear do not invoke a model, even when cold.
// They still resolve the authorized account; set/resume/children keep cooldown.
func TestNativeGoalNonModelControlsPreserveQuotaCooldown(t *testing.T) {
	for _, tc := range []struct {
		action, session string
		allowed         bool
	}{{"show", "chat", true}, {"pause", "chat", true}, {"clear", "chat", true}, {"set", "chat", false}, {"resume", "chat", false}, {"clear", "child", false}} {
		calls := 0
		stop := errors.New("stopped at authorized account resolution")
		model := LLMModel{Provider: "codex-cli", ModelID: "goal-nonmodel-" + tc.action + tc.session, ConnectionID: "account"}
		key := modelQuotaKey(model)
		reset := time.Now().Add(time.Hour)
		agent := &Agent{sessionID: "chat", logger: loggerv2.NewNoop(), llmConfig: AgentLLMConfiguration{Primary: model}, apiKeys: &llm.ProviderAPIKeys{ResolveConnection: func(context.Context, llm.Provider, string) (*llm.ProviderAPIKeys, error) { calls++; return nil, stop }}, quotaExhaustedModels: map[quotaModelKey]time.Time{key: reset}}
		ctx := llmtypes.WithNativeGoalCommand(context.Background(), llmtypes.NativeGoalCommand{SessionID: tc.session, Action: tc.action})
		_, _, err := generateContentWithRetry(agent, ctx, nil, nil, 1)
		if tc.allowed {
			if !errors.Is(err, stop) || calls != 1 {
				t.Fatalf("non-model control did not reach account: %s %v", tc.action, err)
			}
		} else if llmerrors.KindOf(err) != llmerrors.KindQuotaExhausted || calls != 0 {
			t.Fatal("model/child work bypassed quota", err)
		}
		if !agent.quotaExhaustedModels[key].Equal(reset) {
			t.Fatal("native control erased quota cooldown")
		}
	}
}
