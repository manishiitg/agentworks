package workproduct

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func TestWorkIdentityToolPersistsAndPromptVariablesReloadIt(t *testing.T) {
	const projectPath = "Chats/Work/projects/demo"
	manifest := `{"schema_version":1,"product":"work","id":"demo","title":"Demo","description":"Ship the demo Crew.","updated_at":"old","capabilities":{"selected_servers":[]}}`
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/"+projectPath+"/product.json" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data":    map[string]interface{}{"filepath": projectPath + "/product.json", "content": manifest},
			})
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			manifest = payload.Content
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, server.URL); err != nil {
		t.Fatalf("register Crew runtime: %v", err)
	}
	var emitted []*orchestratorevents.ProductInteractionEvent
	interaction := &agentprofiles.InteractionBinding{Kind: "identity_updated", Render: "product.refresh"}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "work.set-identity", Interaction: interaction}, agentprofiles.ToolRuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: projectPath, Product: "work", Interaction: interaction,
		Emit: func(event any) {
			if payload, ok := event.(*orchestratorevents.ProductInteractionEvent); ok {
				emitted = append(emitted, payload)
			}
		},
	})
	if err != nil {
		t.Fatalf("build Crew identity tool: %v", err)
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"operation": "set",
		"icon":      "🛠️",
		"name":      "Nova",
	})
	if err != nil {
		t.Fatalf("set Crew identity: %v", err)
	}
	if !strings.Contains(result, "Adopt it immediately") || !strings.Contains(result, "Name: Nova") {
		t.Fatalf("unexpected tool result: %q", result)
	}

	mu.Lock()
	var saved map[string]interface{}
	if err := json.Unmarshal([]byte(manifest), &saved); err != nil {
		mu.Unlock()
		t.Fatalf("decode saved manifest: %v", err)
	}
	mu.Unlock()
	if saved["title"] != "Demo" || saved["updated_at"] == "old" {
		t.Fatalf("project metadata was not preserved/updated: %+v", saved)
	}
	identity, _ := saved["identity"].(map[string]interface{})
	if identity["icon"] != "🛠️" || identity["name"] != "Nova" {
		t.Fatalf("identity was not saved: %+v", identity)
	}
	if len(emitted) != 1 || emitted[0].Product != "work" || emitted[0].Kind != "identity_updated" || emitted[0].Payload["operation"] != "set" {
		t.Fatalf("identity update event was not emitted: %+v", emitted)
	}

	variables, err := registry.PromptVariables(context.Background(), "work", agentprofiles.RuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: projectPath,
	})
	if err != nil {
		t.Fatalf("load Crew prompt variables: %v", err)
	}
	if got := variables["WORK_IDENTITY"]; !strings.Contains(got, "Purpose: Ship the demo Crew.") || !strings.Contains(got, "Icon: 🛠️") || !strings.Contains(got, "Name: Nova") {
		t.Fatalf("prompt identity was not reloaded: %q", got)
	}
	if got := variables["WORK_IDENTITY_KEY"]; got != "Ship the demo Crew." {
		t.Fatalf("identity key should carry only the purpose: %q", got)
	}

	if _, err := tool.Execute(context.Background(), map[string]interface{}{"operation": "clear"}); err != nil {
		t.Fatalf("clear Crew identity: %v", err)
	}
	if len(emitted) != 2 || emitted[1].Payload["operation"] != "clear" {
		t.Fatalf("identity clear interaction was not emitted: %+v", emitted)
	}
	variables, err = registry.PromptVariables(context.Background(), "work", agentprofiles.RuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: projectPath,
	})
	if err != nil {
		t.Fatalf("reload cleared Crew prompt variables: %v", err)
	}
	// Clear removes presentation only: the required purpose stays.
	if got := variables["WORK_IDENTITY"]; !strings.Contains(got, "Purpose: Ship the demo Crew.") || strings.Contains(got, "Nova") || strings.Contains(got, "Icon:") {
		t.Fatalf("clear should preserve only the purpose: %q", got)
	}
}

func TestWorkIdentityRequiresPurpose(t *testing.T) {
	const projectPath = "Chats/Work/projects/required"
	manifest := `{"schema_version":1,"product":"work","id":"required","title":"Required","capabilities":{"selected_servers":[]}}`
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/"+projectPath+"/product.json" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data":    map[string]interface{}{"filepath": projectPath + "/product.json", "content": manifest},
			})
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			manifest = payload.Content
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, server.URL); err != nil {
		t.Fatalf("register Crew runtime: %v", err)
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "work.set-identity"}, agentprofiles.ToolRuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: projectPath, Product: "work",
	})
	if err != nil {
		t.Fatalf("build Crew identity tool: %v", err)
	}

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"operation": "set", "icon": "🤖", "name": "Helper",
	})
	if err != nil {
		t.Fatalf("set without purpose: %v", err)
	}
	if !strings.Contains(result, "A purpose is required") {
		t.Fatalf("missing-purpose set should be refused: %q", result)
	}

	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"operation": "set", "purpose": "Help out.",
	})
	if err != nil || !strings.Contains(result, "Adopt it immediately") {
		t.Fatalf("complete set should save: %q, %v", result, err)
	}

	// Explicitly emptying the purpose on an existing identity is refused.
	result, err = tool.Execute(context.Background(), map[string]interface{}{
		"operation": "set", "purpose": "",
	})
	if err != nil {
		t.Fatalf("set empty purpose: %v", err)
	}
	if !strings.Contains(result, "A purpose is required") {
		t.Fatalf("emptying the purpose should be refused: %q", result)
	}
}

func TestWorkIdentityStaysCompact(t *testing.T) {
	identity := workIdentity{
		Icon: strings.Repeat("x", workIdentityIconLimit+1),
		Name: "Nova",
	}
	if got := validateWorkIdentity(identity); got != "The identity icon is invalid: icon must be 8 characters or fewer." {
		t.Fatalf("unexpected icon validation: %q", got)
	}

	identity.Icon = "N"
	identity.Name = strings.Repeat("x", workIdentityNameLimit+1)
	if got := validateWorkIdentity(identity); got != "The identity name must be at most 60 characters." {
		t.Fatalf("unexpected name validation: %q", got)
	}
}

func TestWorkIdentityAcceptsUploadedImage(t *testing.T) {
	image := "data:image/png;base64," + strings.Repeat("A", 1024)
	identity := workIdentity{Icon: image, Name: "Nova"}
	if got := validateWorkIdentity(identity); got != "" {
		t.Fatalf("uploaded image icon should validate, got %q", got)
	}
	rendered := renderWorkIdentity(identity)
	if strings.Contains(rendered, "AAAA") || !strings.Contains(rendered, "Icon: (custom uploaded image)") {
		t.Fatalf("uploaded image icon must be redacted from prompts, got %q", rendered)
	}
}

func TestCreateCrewProjectToolCreatesIdentifiedPersistentProject(t *testing.T) {
	writes := map[string]string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/api/documents/Chats/Work/projects/") {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if r.Header.Get("X-User-ID") != "user-1" {
			http.Error(w, "missing user scope", http.StatusUnauthorized)
			return
		}
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		writes[strings.TrimPrefix(r.URL.Path, "/api/documents/")] = payload.Content
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	}))
	defer server.Close()

	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, server.URL); err != nil {
		t.Fatalf("register Crew runtime: %v", err)
	}
	var emitted []*orchestratorevents.ProductInteractionEvent
	interaction := &agentprofiles.InteractionBinding{Kind: "project_created", Render: "product.refresh"}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "work.create-project", Interaction: interaction}, agentprofiles.ToolRuntimeContext{
		UserID: "user-1", SessionID: "session-1", WorkspacePath: "Chats/Work/projects/current", Product: "work", Interaction: interaction,
		Emit: func(event any) {
			if payload, ok := event.(*orchestratorevents.ProductInteractionEvent); ok {
				emitted = append(emitted, payload)
			}
		},
	})
	if err != nil {
		t.Fatalf("build create Crew tool: %v", err)
	}
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"name":        "Launch Crew",
		"icon":        "🚀",
		"description": "Own the launch.",
	})
	if err != nil {
		t.Fatalf("create Crew: %v", err)
	}
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(result), &response); err != nil {
		t.Fatalf("decode tool response: %v", err)
	}
	workspacePath, _ := response["workspace_path"].(string)
	if !strings.HasPrefix(workspacePath, "Chats/Work/projects/launch-crew-") || response["status"] != "created" {
		t.Fatalf("unexpected tool response: %+v", response)
	}

	mu.Lock()
	productRaw := writes[workspacePath+"/product.json"]
	workflowRaw := writes[workspacePath+"/workflow.json"]
	_, hasCodeFolder := writes[workspacePath+"/code/.gitkeep"]
	mu.Unlock()
	var product map[string]interface{}
	if err := json.Unmarshal([]byte(productRaw), &product); err != nil {
		t.Fatalf("decode product manifest: %v", err)
	}
	identity, _ := product["identity"].(map[string]interface{})
	if product["product"] != "work" || product["title"] != "Launch Crew" || identity["name"] != "Launch Crew" || identity["icon"] != "🚀" {
		t.Fatalf("Crew identity was not persisted: %+v", product)
	}
	var workflow map[string]interface{}
	if err := json.Unmarshal([]byte(workflowRaw), &workflow); err != nil {
		t.Fatalf("decode workflow manifest: %v", err)
	}
	if workflow["label"] != "Launch Crew" || !hasCodeFolder {
		t.Fatalf("basic Crew files were not initialized: workflow=%+v code=%v", workflow, hasCodeFolder)
	}
	if len(emitted) != 1 || emitted[0].Kind != "project_created" || emitted[0].Payload["project_id"] != response["project_id"] {
		t.Fatalf("project refresh event was not emitted: %+v", emitted)
	}
}
