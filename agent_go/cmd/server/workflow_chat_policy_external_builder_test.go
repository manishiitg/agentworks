package server

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// The owner (or an editor) over MCP is the same Builder as on the website:
// same capabilities and tools. Only the token's Brain scope narrows it.
func TestExternalBuilderHasWebsiteBuilderTools(t *testing.T) {
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: "Workflow/test", ExternalBuilderOperationID: "op"}
	external := resolveWorkflowChatPolicy("chat", req, nil, false)
	req.ExternalBuilderOperationID = ""
	web := resolveWorkflowChatPolicy("chat", req, nil, false)
	if external.Mode != "builder" || !maps.Equal(external.Capabilities, web.Capabilities) {
		t.Fatalf("MCP Builder capabilities differ from the website: %v vs %v", external.Capabilities, web.Capabilities)
	}

	builderTools := agentworksproduct.ChatTools("builder")
	website := newAgentWorksChatToolGate("builder")
	for _, kb := range []bool{false, true} {
		claims := &UserClaims{ExternalBuilderOperationID: "op", AccessToken: &accesstokens.Token{}}
		if kb {
			claims.AccessToken.Scopes = []string{"knowledgebase:read"}
		}
		gate := newAgentWorksChatToolGate("builder")
		gate.DenyWhere(func(name string) bool { return externalBuilderToolDenied(claims, name) })
		code, bridge := externalWorkflowTransport(claims, false, gate)
		if code {
			t.Fatal("token-backed MCP Builder must use the direct bridge")
		}
		for _, name := range builderTools {
			if !website.Allows(name) {
				continue
			}
			want := kb || !slices.Contains(knowledgebase.ToolNames(), knowledgebase.CanonicalToolName(name))
			if slices.Contains(bridge, name) != want || accessTokenRunToolDenied(claims, name) == want {
				t.Fatalf("Builder tool %s: on bridge=%v, want %v (brain scope %v)", name, slices.Contains(bridge, name), want, kb)
			}
		}
	}

	// The same token without a Builder operation is a Run caller and keeps the Run denylist.
	run := &UserClaims{AccessToken: &accesstokens.Token{}}
	if !accessTokenRunToolDenied(run, "execute_shell_command") {
		t.Fatal("Run token gained the shell")
	}
	req.ExternalBuilderOperationID = "op"
	req.PinRunMode = true
	if p := resolveWorkflowChatPolicy("chat", req, nil, readOnlyForRequest(WorkflowAccessOwner, req)); p.Mode != "run" {
		t.Fatal("Builder operation overrode a Run pin")
	}
}

func TestExternalBuilderProvenanceCannotComeFromJSON(t *testing.T) {
	var req QueryRequest
	if err := json.Unmarshal([]byte(`{"external_builder_operation_id":"forged","ExternalBuilderOperationID":"forged"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.ExternalBuilderOperationID != "" {
		t.Fatal("untrusted JSON supplied authority")
	}
	req.ExternalBuilderOperationID = "trusted"
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err = json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["ExternalBuilderOperationID"]; ok {
		t.Fatal("internal authority serialized to caller")
	}
}
