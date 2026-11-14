package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// PLAT-746-1005: exercise real HTTP/MCP admission and real Crew upload writes.
// The workspace capture boundary is faked; browser IPC/cropping has its own
// focused regression and neither test is evidence of a live browser gesture.
func TestCrewChatContextMCPUsesSameSourceAndDestinationGuardsAndOrdinaryUploads(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	const token = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f, _ := newExternalCrewCommandFixture(t, func(router *gin.Engine) {
		router.POST("/api/browser/live/:session/context", func(c *gin.Context) {
			calls.Add(1)
			var in models.BrowserChatContextRequest
			if c.ShouldBindJSON(&in) != nil {
				c.Status(400)
				return
			}
			out := models.BrowserChatContext{DocumentToken: token, URL: "https://example.com/current", Width: 100, Height: 100, Selector: "#selected", Text: "Selected page text", Rect: models.BrowserContextRect{X: 5, Y: 5, Width: 10, Height: 10}}
			if in.X != nil {
				out.ImageBase64 = base64.StdEncoding.EncodeToString(pngData.Bytes())
			}
			c.JSON(200, out)
		})
	})
	const conversation = "team:chat:root-owner"
	const session = "context-owner-session"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","main_chat_key":"`+conversation+`"}`)
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: conversation, WorkspacePath: externalCommandCrewRoot, SessionID: session, Title: "Owner agent"}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, conversation): record}}); err != nil {
		t.Fatal(err)
	}
	source := browserSessionForWorkspace("owner", externalCommandCrewRoot)
	browser.GetSessionTracker().Touch(source, session, session)
	t.Cleanup(func() { browser.GetSessionTracker().Remove(source) })
	body := map[string]any{"source": "browser", "operation": "probe", "conversation_key": conversation, "session_id": session, "browser_session": source}
	httpCall := func(actor string, changes map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		input := map[string]any{}
		for k, v := range body {
			input[k] = v
		}
		for k, v := range changes {
			input[k] = v
		}
		r := sharedSecretsRequest(http.MethodPost, "/api/agent-profiles/work/projects/team/chat-context", actor, input)
		r = mux.SetURLVars(r, map[string]string{"id": "work", "project_id": "team"})
		w := httptest.NewRecorder()
		f.api.handleCrewChatContext(w, r)
		return w
	}
	allowed := httpCall("owner", nil)
	if allowed.Code != 200 || !strings.Contains(allowed.Body.String(), token) {
		t.Fatalf("owner probe refused: %d %s", allowed.Code, allowed.Body.String())
	}
	if _, _, _, visible := f.api.externalCrewResolve(t.Context(), &UserClaims{UserID: "other", Username: "other"}, "team"); !visible {
		t.Fatal("shared source refusal fixture lacks normal Crew access")
	}
	for _, tc := range []struct {
		actor   string
		changes map[string]any
		status  int
	}{
		{"other", nil, 403},
		{"owner", map[string]any{"session_id": "private-unregistered"}, 403},
		{"owner", map[string]any{"browser_session": "session-not-this-crew--browser"}, 409},
	} {
		before := calls.Load()
		denied := httpCall(tc.actor, tc.changes)
		if denied.Code != tc.status || calls.Load() != before {
			t.Fatalf("refusal crossed source boundary: %d %s", denied.Code, denied.Body.String())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	server := serveExternalMCP(t, f.api, &UserClaims{UserID: "owner", Username: "owner"})
	client := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, client)
	spec := callRemoteTool(t, ctx, client, externalMCPToolSpec, map[string]any{"names": "workspace"})
	requireRemoteSuccess(t, spec, "context catalog")
	var schema struct {
		Schemas map[string]struct {
			InputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"inputSchema"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(marshalStructured(t, spec)), &schema); err != nil {
		t.Fatal(err)
	}
	var actions struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(schema.Schemas["workspace"].InputSchema.Properties["action"], &actions); err != nil || !slices.Contains(actions.Enum, "add_context") {
		t.Fatalf("real MCP omitted context action: %v", err)
	}
	result := callRemoteTool(t, ctx, client, externalMCPToolCall, map[string]any{"name": "workspace", "arguments": map[string]any{"action": "add_context", "workspace_id": "work:team", "context_source": "browser", "context_op": "add", "conversation_key": conversation, "session_id": session, "browser_session": source, "document_token": token, "x": 7, "y": 7}})
	requireRemoteSuccess(t, result, "context add")
	if text := marshalStructured(t, result); !strings.Contains(text, "preview_path") || strings.Contains(text, "image_base64") {
		t.Fatalf("wrong completed attachment response: %s", text)
	}
	uploads := filepath.Join(f.docs, externalCommandCrewRoot, "uploads")
	files, err := os.ReadDir(uploads)
	if err != nil || len(files) != 2 {
		t.Fatalf("ordinary attachments missing: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(uploads, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(file.Name(), ".png") {
			if !bytes.Equal(data, pngData.Bytes()) {
				t.Fatal("crop changed during ordinary upload")
			}
		} else {
			var captured map[string]any
			if json.Unmarshal(data, &captured) != nil || captured["text"] != "Selected page text" || captured["untrusted_context"] != true {
				t.Fatal("context metadata missing")
			}
		}
	}
}
