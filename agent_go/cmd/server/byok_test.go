package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// PLAT-717: an OpenRouter key account hands its key to Pi under provider
// "openrouter" only (the adapter exports it as OPENROUTER_API_KEY), and the
// model browser and chat picker get OpenRouter's own free and tool flags.
func TestByokOpenRouterKeyReachesPiOnlyAndFreeFlagsReachThePicker(t *testing.T) {
	key := "sk-or-v1-test"
	keys, err := connectionCredentialKeys(storedProviderConnection{ProviderConnection: ProviderConnection{Provider: "pi-cli", UnderlyingProvider: "openrouter", AuthMethod: "api_key"}, Credential: key})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys.PiProviderKeys) != 1 || keys.PiProviderKeys["openrouter"] != key {
		t.Fatalf("Pi provider keys = %#v", keys.PiProviderKeys)
	}
	if keys.OpenRouter != nil || keys.PiCLI != nil || keys.PiCustomProvider != nil || keys.ClaudeCodeOAuthToken != nil || keys.CodexCLI != nil {
		t.Fatalf("the key reached another field: %#v", keys)
	}
	if env := picli.PiProviderKeyEnv("openrouter"); env != "OPENROUTER_API_KEY" {
		t.Fatalf("Pi key variable for openrouter = %q", env)
	}

	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":"vendor/paid-model","name":"Paid","context_length":200000,"pricing":{"prompt":"0.000002","completion":"0.00001"},"supported_parameters":["tools"]},
			{"id":"vendor/chat-only:free","name":"Chat only","context_length":32000,"pricing":{"prompt":"0","completion":"0"},"supported_parameters":["temperature"]},
			{"id":"vendor/agent:free","name":"Agent","context_length":262144,"pricing":{"prompt":"0","completion":"0"},"supported_parameters":["tools","tool_choice"]}]}`))
	}))
	defer catalog.Close()
	saved := openRouterModelsAPIURL
	openRouterModelsAPIURL = catalog.URL
	defer func() { openRouterModelsAPIURL = saved }()
	byokCatalogCache.Lock()
	delete(byokCatalogCache.models, "openrouter")
	byokCatalogCache.Unlock()

	models, err := listByokModels(context.Background(), byokServices["openrouter"], "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]byokModel{}
	for _, model := range models {
		byID[model.ModelID] = model
	}
	agent, chatOnly, paid := byID["openrouter/vendor/agent:free"], byID["openrouter/vendor/chat-only:free"], byID["openrouter/vendor/paid-model"]
	if !agent.IsFree || agent.SupportsTools == nil || !*agent.SupportsTools || !agent.Recommended {
		t.Fatalf("free tool model = %+v", agent)
	}
	if !chatOnly.IsFree || chatOnly.Recommended {
		t.Fatalf("a free model without tools must not be recommended for agents: %+v", chatOnly)
	}
	if paid.IsFree || paid.CostInput != 2 || paid.CostOutput != 10 {
		t.Fatalf("paid model price per 1M = %+v", paid)
	}
	if models[0].ModelID != agent.ModelID {
		t.Fatalf("recommended free models are not listed first: %v", models[0].ModelID)
	}
	if got := defaultByokModel(byokServices["openrouter"], models); got != agent.ModelID {
		t.Fatalf("default model = %q", got)
	}
}
