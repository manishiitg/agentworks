package virtualtools

import (
	"context"
	"testing"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
)

// A read_image call over the CLI's tool bridge is a plain HTTP request with no turn context. The image
// analysis must still run on the account the session runs on (a user's own Claude login), not the server's:
// the turn's keys are attached when the context carries none, and never replace keys that are already there.
func TestInjectSelectedLLMConfigCarriesTheTurnsAccount(t *testing.T) {
	turn := &llm.ProviderAPIKeys{}
	selected := mcpagent.LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5-5", ConnectionID: "conn-user"}
	var gotKeys *llm.ProviderAPIKeys
	var gotCfg any
	inner := func(ctx context.Context, _ map[string]any) (string, error) {
		gotKeys = ProviderAccountKeysFromContext(ctx)
		gotCfg = ctx.Value(mcpagent.ToolExecutionLLMConfigKey)
		return "ok", nil
	}
	wrapped := injectSelectedLLMConfig(inner, selected, turn)

	if _, err := wrapped(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotKeys != turn {
		t.Fatal("a bridge call did not receive the turn's provider account")
	}
	if cfg, ok := gotCfg.(mcpagent.LLMModel); !ok || cfg.ConnectionID != "conn-user" {
		t.Fatalf("the selected model (with its connection) was not injected: %#v", gotCfg)
	}

	// keys already in the context win
	other := &llm.ProviderAPIKeys{}
	if _, err := wrapped(WithProviderAccountKeys(context.Background(), other), nil); err != nil {
		t.Fatal(err)
	}
	if gotKeys != other {
		t.Fatal("keys already carried by the context were replaced")
	}

	// no turn keys: nothing is attached, as before
	if _, err := injectSelectedLLMConfig(inner, selected, nil)(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if gotKeys != nil {
		t.Fatal("keys appeared out of nowhere")
	}
}
