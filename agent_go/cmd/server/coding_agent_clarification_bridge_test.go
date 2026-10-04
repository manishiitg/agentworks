package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Exercise the actual stdio MCP bridge used by the coding providers, through
// HTTP, the registered platform tool and the UI's answer endpoint. No bank
// workflow or model is involved in this transport test.
func TestCodingAgentClarificationThroughMCPBridge(t *testing.T) {
	binary := buildPulseTestMCPBridge(t)
	for _, provider := range []string{"muse-cli", "claude-code", "codex-cli"} {
		t.Run(provider, func(t *testing.T) {
			const session = "clarification-bridge-test"
			api := &StreamingAPI{eventStore: events.NewEventStore(100), activeSessions: map[string]*ActiveSessionInfo{session: {SessionID: session, UserID: "owner"}}}
			registrar := &placeMCPToolRegistrar{}
			if err := api.registerCodingAgentClarificationTool(registrar, session, provider); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/tools/custom/request_clarification" || r.Header.Get("X-Session-ID") != session || r.Header.Get("Authorization") != "Bearer test-clarification-token" {
					http.Error(w, "unknown session or tool", http.StatusForbidden)
					return
				}
				var args map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				result, err := registrar.exec(r.Context(), args)
				response := map[string]interface{}{"success": err == nil, "result": result}
				if err != nil {
					response["error"] = err.Error()
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			tools, _ := json.Marshal([]map[string]interface{}{{"name": "request_clarification", "description": "Ask for choices", "type": "custom", "input_schema": codingAgentClarificationSchema()}})
			bridge, err := client.NewStdioMCPClient(binary, append(os.Environ(), "MCP_API_URL="+server.URL, "MCP_API_TOKEN=test-clarification-token", "MCP_SESSION_ID="+session, "MCP_TOOLS="+string(tools)))
			if err != nil {
				t.Fatal(err)
			}
			defer bridge.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := bridge.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "clarification-test", Version: "1"}}}); err != nil {
				t.Fatal(err)
			}
			var previousPrompt string
			for turn := 0; turn < 2; turn++ {
				result := make(chan string, 1)
				go func() {
					req := mcp.CallToolRequest{}
					req.Params.Name = "request_clarification"
					req.Params.Arguments = clarificationTestArgs()
					out, err := bridge.CallTool(ctx, req)
					if err != nil {
						result <- err.Error()
						return
					}
					result <- fmt.Sprint(out.Content)
				}()
				p := pendingClarificationForTest(t, api, session)
				if previousPrompt == p.promptID {
					t.Fatal("prompts are not independent")
				}
				// This is exactly the UI payload, with one answer per question.
				body := `{"provider":"` + provider + `","prompt_id":"` + p.promptID + `","answers":[{"id":"scope","selected_labels":["Gmail"]},{"id":"purpose","selected_labels":["Send"]}]}`
				r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+session+"/coding-agent-question/answer", strings.NewReader(body))
				r = r.WithContext(requestWithUserForSessionAccess("owner").Context())
				r = mux.SetURLVars(r, map[string]string{"session_id": session})
				w := httptest.NewRecorder()
				api.handleCodingAgentQuestionAnswer(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("UI answer: %d %s", w.Code, w.Body.String())
				}
				select {
				case out := <-result:
					for _, want := range []string{`"status":"answered"`, `"Gmail"`, `"Send"`, p.promptID} {
						if !strings.Contains(out, want) {
							t.Fatalf("provider tool result lost %s: %s", want, out)
						}
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				previousPrompt = p.promptID
			}
		})
	}
}
