package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Construction requires a model; generation uses the local HTTP provider below.
type builderKnowledgeModel struct{}

func (*builderKnowledgeModel) GetModelID() string { return "gpt-4o" }
func (m *builderKnowledgeModel) GetModelMetadata(string) (*llmtypes.ModelMetadata, error) {
	return &llmtypes.ModelMetadata{ModelID: m.GetModelID()}, nil
}
func (*builderKnowledgeModel) GenerateContent(context.Context, []llmtypes.MessageContent, ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return nil, fmt.Errorf("test generation must use the local HTTP provider")
}

func TestExternalBuilderModelReadsBrainWithoutShell(t *testing.T) {
	service, admin, workspace, folderID := knowledgeIntegrationFixture(t)
	path := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), workspace, "workflow.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["brain_access"] = "read"
	data, _ = json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	const marker = "BUILDER-DIRECT-KB-94817"
	if _, err = service.CallTool(t.Context(), admin, "brain_update", map[string]any{"action": "create", "folder_id": folderID, "filename": "smoke.md", "title": "Smoke", "type": "note", "content": marker, "request_id": "builder-marker"}); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	common.SetSessionWorkflowPath(sessionID, workspace)
	defer common.ClearSessionShellConfig(sessionID)
	claims := &UserClaims{UserID: "admin", ExternalBuilderOperationID: "builder-test", AccessToken: &accesstokens.Token{Scopes: []string{"knowledgebase:read", "builder:chat"}}}
	code, bridge := externalBuilderTransport(claims)
	tools, executors, categories := createKnowledgebaseTools("admin", sessionID, workspace)
	definition := mcpagent.AgentDefinition{}
	for _, tool := range tools {
		if tool.Function == nil || externalBuilderToolDenied(claims, tool.Function.Name) {
			continue
		}
		encoded, _ := json.Marshal(tool.Function.Parameters)
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		name := tool.Function.Name
		definition.Tools.Direct = append(definition.Tools.Direct, mcpagent.ToolDefinition{Name: name, Description: tool.Function.Description, InputSchema: schema, DisplayGroup: categories[name], Execute: executors[name].(func(context.Context, map[string]interface{}) (string, error))})
	}
	config := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(config, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		message := map[string]any{"role": "assistant", "content": marker}
		finish := "stop"
		if calls.Add(1) == 1 {
			names := []string{}
			for _, tool := range request.Tools {
				names = append(names, tool.Function.Name)
			}
			if !slices.Contains(names, "brain_read") || slices.Contains(names, "execute_shell_command") || slices.Contains(names, "brain_update") {
				http.Error(w, fmt.Sprintf("incorrect Builder tools: %v", names), 400)
				return
			}
			message["content"] = nil
			message["tool_calls"] = []any{map[string]any{"id": "read-binding", "type": "function", "function": map[string]any{"name": "brain_read", "arguments": `{"action":"read","path":"Imported/smoke.md"}`}}}
			finish = "tool_calls"
		} else if !strings.Contains(string(request.Messages), marker) {
			http.Error(w, "KB tool result did not reach model", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "builder-chat", "object": "chat.completion", "model": "gpt-4o", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20}})
	}))
	t.Cleanup(provider.Close)
	t.Setenv("OPENAI_BASE_URL", provider.URL+"/v1/")
	key := "local-provider-test-key"

	agent, err := mcpagent.NewAgentFromDefinition(t.Context(), definition, mcpagent.RuntimeConfig{
		Model: &builderKnowledgeModel{}, MCPConfigPath: config,
		Generation:    mcpagent.GenerationRuntimeConfig{Provider: llm.ProviderOpenAI, MaxTurns: 3, LLM: mcpagent.AgentLLMConfiguration{Primary: mcpagent.LLMModel{Provider: "openai", ModelID: "gpt-4o", APIKey: &key}}},
		Tools:         mcpagent.ToolRuntimeConfig{CodeExecution: code, Discovery: true, AdditionalBridge: bridge},
		MCP:           mcpagent.MCPRuntimeConfig{SessionID: sessionID, UserID: "admin"},
		Observability: mcpagent.ObservabilityRuntimeConfig{Logger: loggerv2.NewNoop()},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := knowledgeTestCaller(context.WithValue(t.Context(), common.ChatSessionIDKey, sessionID), "admin")
	result, err := agent.Run(ctx, mcpagent.Turn{Input: "Read the marker from the attached KB."})
	if err != nil || result.Text != marker || calls.Load() != 2 {
		t.Fatalf("Builder read failed: %+v calls=%d error=%v", result, calls.Load(), err)
	}
}

func TestExternalBuilderTransportKeepsPermissionBoundary(t *testing.T) {
	for _, kb := range []bool{false, true} {
		claims := &UserClaims{ExternalBuilderOperationID: "op", AccessToken: &accesstokens.Token{}}
		if kb {
			claims.AccessToken.Scopes = []string{"knowledgebase:read"}
		}
		code, names := externalBuilderTransport(claims)
		if code || slices.Contains(names, "brain_read") != kb {
			t.Fatal("incorrect transport", code, names)
		}
		for _, name := range names {
			if externalBuilderToolDenied(claims, name) {
				t.Fatal("denied tool projected", name)
			}
		}
		for _, forbidden := range []string{"execute_shell_command", "diff_patch_workspace_file", "set_workflow_contract_version", "manage_vault_access"} {
			if slices.Contains(names, forbidden) {
				t.Fatal("unsafe tool projected", forbidden)
			}
		}
	}
	code, names := externalBuilderTransport(&UserClaims{})
	if !code || len(names) != 0 {
		t.Fatal("app transport changed")
	}
}
