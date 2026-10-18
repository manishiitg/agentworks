package codeproduct

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestCodeRunLoadsSavedProjectPurpose(t *testing.T) {
	const root = "_users/owner/Chats/Code/projects/support"
	var mu sync.Mutex
	readStatus := http.StatusOK
	manifest := map[string]any{"product": "code", "description": "Reply with PROJECT_CONTEXT_OK.", "identity": map[string]any{"role": "Support"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/documents/"+root+"/product.json" || r.Header.Get("X-User-ID") != "caller" {
			http.Error(w, "unexpected project read", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if readStatus != http.StatusOK {
			http.Error(w, "project read unavailable", readStatus)
			return
		}
		content, _ := json.Marshal(manifest)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"filepath": root + "/product.json", "content": string(content)}})
	}))
	defer server.Close()
	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, server.URL); err != nil {
		t.Fatal(err)
	}
	load := func() map[string]string {
		t.Helper()
		variables, err := registry.PromptVariables(context.Background(), ProfileID, agentprofiles.RuntimeContext{UserID: "caller", SessionID: "run-chat", WorkspacePath: root})
		if err != nil {
			t.Fatal(err)
		}
		profile := BuiltinAgentProfile()
		profile.SystemPromptTemplate = workproduct.RunPromptTemplate()
		prompt, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{ProjectTitle: "Support", Product: variables})
		if err != nil || !strings.Contains(prompt, "Purpose: "+manifest["description"].(string)) {
			t.Fatalf("Code Run lost the saved project purpose: %q, %v", prompt, err)
		}
		return variables
	}
	before := load()
	if before["WORK_IDENTITY_KEY"] != "Support\nReply with PROJECT_CONTEXT_OK." {
		t.Fatalf("retained session has no saved identity key: %v", before)
	}
	// Existing Code projects can have a purpose without a role. Re-read it on
	// each turn so editing the purpose also changes the retained-session key.
	mu.Lock()
	manifest["identity"] = map[string]any{"role": ""}
	manifest["description"] = "Reply with UPDATED_CONTEXT_OK."
	mu.Unlock()
	after := load()
	if after["WORK_IDENTITY_KEY"] != "\nReply with UPDATED_CONTEXT_OK." || after["WORK_IDENTITY_KEY"] == before["WORK_IDENTITY_KEY"] {
		t.Fatalf("saved purpose edit did not refresh session identity: %v", after)
	}
	mu.Lock()
	readStatus = http.StatusNotFound
	mu.Unlock()
	variables, err := registry.PromptVariables(context.Background(), ProfileID, agentprofiles.RuntimeContext{UserID: "caller", WorkspacePath: root})
	if err != nil || variables["WORK_IDENTITY"] != "" || variables["WORK_IDENTITY_KEY"] != "\n" {
		t.Fatalf("legacy Code without product.json stopped working: %v, %v", variables, err)
	}
	profile := BuiltinAgentProfile()
	profile.SystemPromptTemplate = workproduct.RunPromptTemplate()
	if _, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{ProjectTitle: "Legacy", Product: variables}); err != nil {
		t.Fatalf("legacy Code Run cannot render: %v", err)
	}
	mu.Lock()
	readStatus = http.StatusForbidden
	mu.Unlock()
	if _, err := registry.PromptVariables(context.Background(), ProfileID, agentprofiles.RuntimeContext{UserID: "caller", WorkspacePath: root}); err == nil {
		t.Fatal("refused project read was mistaken for an absent manifest")
	}
}
