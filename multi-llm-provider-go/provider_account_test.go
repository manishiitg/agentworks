package llmproviders

import (
	"context"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"slices"
	"testing"
)

type accountCaptureModel struct{ opts *llmtypes.CallOptions }

func (m *accountCaptureModel) GetModelID() string { return "capture" }
func (m *accountCaptureModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return nil, nil
}
func (m *accountCaptureModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	m.opts = &llmtypes.CallOptions{}
	for _, option := range options {
		option(m.opts)
	}
	return &llmtypes.ContentResponse{}, nil
}

func TestProviderAccountBindingOverridesCallerAndCloneKeepsResolver(t *testing.T) {
	captured := &accountCaptureModel{}
	model := &accountBoundModel{Model: captured, environment: map[string]string{"HOME": "/B"}, credentials: map[string]string{"META_API_KEY": "key-B"}}
	_, err := model.GenerateContent(context.Background(), nil, llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": "/A"}), llmtypes.WithProviderAccountCredentials(map[string]string{"META_API_KEY": "key-A"}))
	if err != nil {
		t.Fatal(err)
	}
	env := llmtypes.MergeCodingAgentSecretEnvironment(nil, captured.opts)
	if !slices.Contains(env, "HOME=/B") || !slices.Contains(env, "META_API_KEY=key-B") {
		t.Fatal("caller replaced trusted account binding")
	}
	denied := errors.New("unauthorized connection")
	keys := &ProviderAPIKeys{ResolveConnection: func(context.Context, Provider, string) (*ProviderAPIKeys, error) { return nil, denied }, RuntimeEnvironment: map[string]string{"HOME": "/B"}}
	copy := keys.Clone()
	copy.RuntimeEnvironment["HOME"] = "/changed"
	if keys.RuntimeEnvironment["HOME"] != "/B" {
		t.Fatal("Clone shares mutable runtime paths")
	}
	_, err = InitializeLLM(Config{Provider: ProviderCodexCLI, ConnectionID: "private-B", APIKeys: copy})
	if !errors.Is(err, denied) {
		t.Fatal("connection failure fell back to another credential")
	}
	_, err = InitializeLLM(Config{Provider: ProviderCodexCLI, ConnectionID: "private-B"})
	if err == nil {
		t.Fatal("missing authorization resolver was accepted")
	}
}
