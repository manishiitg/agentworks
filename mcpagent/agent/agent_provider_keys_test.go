package mcpagent

import (
	"context"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"
)

type providerKeyCarrierModel struct {
	keys *llm.ProviderAPIKeys
}

func (m *providerKeyCarrierModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return nil, nil
}

func (m *providerKeyCarrierModel) GetModelID() string {
	return "test-model"
}

func (m *providerKeyCarrierModel) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	return nil, nil
}

func (m *providerKeyCarrierModel) GetAPIKeys() *llm.ProviderAPIKeys {
	return m.keys
}

func TestGetLLMModelConfigIncludesZAIAndMiniMaxKeys(t *testing.T) {
	zaiKey := "zai-key"
	kimiKey := "kimi-key"
	minimaxKey := "minimax-key"
	piKey := "pi-key"

	tests := []struct {
		name     string
		provider llm.Provider
		keys     *llm.ProviderAPIKeys
		want     *string
	}{
		{
			name:     "z-ai",
			provider: llm.ProviderZAI,
			keys: &llm.ProviderAPIKeys{
				ZAI: &zaiKey,
			},
			want: &zaiKey,
		},
		{
			name:     "minimax",
			provider: llm.ProviderMiniMax,
			keys: &llm.ProviderAPIKeys{
				MiniMax: &minimaxKey,
			},
			want: &minimaxKey,
		},
		{
			name:     "kimi",
			provider: llm.ProviderKimi,
			keys: &llm.ProviderAPIKeys{
				Kimi: &kimiKey,
			},
			want: &kimiKey,
		},
		{
			name:     "pi-cli",
			provider: llm.ProviderPiCLI,
			keys: &llm.ProviderAPIKeys{
				PiCLI: &piKey,
			},
			want: &piKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				provider: tt.provider,
				modelID:  "test-model",
				apiKeys:  tt.keys,
			}

			config := agent.getLLMModelConfig()
			if config.APIKey == nil || *config.APIKey != *tt.want {
				t.Fatalf("expected api key %q, got %#v", *tt.want, config.APIKey)
			}
		})
	}
}

func TestExtractAPIKeysFromLLMPreservesCodingCLIKeys(t *testing.T) {
	zaiKey := "zai-key"
	kimiKey := "kimi-key"
	codexKey := "codex-key"
	piKey := "pi-key"
	claudeOAuthToken := "claude-workflow-token"

	model := &providerKeyCarrierModel{
		keys: &llm.ProviderAPIKeys{
			ZAI:                  &zaiKey,
			Kimi:                 &kimiKey,
			CodexCLI:             &codexKey,
			PiCLI:                &piKey,
			ClaudeCodeOAuthToken: &claudeOAuthToken,
		},
	}

	keys := extractAPIKeysFromLLM(model)
	if keys == nil {
		t.Fatal("expected extracted keys, got nil")
	}
	if keys.ZAI == nil || *keys.ZAI != zaiKey {
		t.Fatalf("expected ZAI key %q, got %#v", zaiKey, keys.ZAI)
	}
	if keys.Kimi == nil || *keys.Kimi != kimiKey {
		t.Fatalf("expected Kimi key %q, got %#v", kimiKey, keys.Kimi)
	}
	if keys.CodexCLI == nil || *keys.CodexCLI != codexKey {
		t.Fatalf("expected Codex CLI key %q, got %#v", codexKey, keys.CodexCLI)
	}
	if keys.PiCLI == nil || *keys.PiCLI != piKey {
		t.Fatalf("expected Pi CLI key %q, got %#v", piKey, keys.PiCLI)
	}
	if keys.ClaudeCodeOAuthToken == nil || *keys.ClaudeCodeOAuthToken != claudeOAuthToken {
		t.Fatalf("expected Claude Code OAuth token to be preserved, got %#v", keys.ClaudeCodeOAuthToken)
	}
}

func TestGetEffectiveLLMConfigPreservesSelectedZAIModel(t *testing.T) {
	agent := &Agent{provider: llm.ProviderZAI, modelID: zaiadapter.ModelGLM51}
	config := agent.getEffectiveLLMConfig()
	if config.Primary.Provider != string(llm.ProviderZAI) || config.Primary.ModelID != zaiadapter.ModelGLM51 {
		t.Fatalf("selected model changed: %+v", config.Primary)
	}
}
