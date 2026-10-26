package agentworksclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// Real write failures used to omit the server request ID, making a 502
// impossible to match. Correlation must not copy secrets from the request.
func TestFailedHTTPTraceExcludesRequestSecrets(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "gateway-123")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("not JSON"))
	}))
	defer s.Close()
	c, _ := New(s.URL, "header-secret")
	_, err := c.request(context.Background(), http.MethodPost, "/api/external/v1/call?access_token=query-secret", map[string]string{"password": "body-secret"}, true)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway || apiErr.Request == nil {
		t.Fatalf("missing HTTP correlation: %v", err)
	}
	r := apiErr.Request
	if r.ID != "gateway-123" || r.Method != http.MethodPost || r.Path != "/api/external/v1/call" || r.DurationMS < 0 {
		t.Fatalf("wrong trace: %+v", r)
	}
	if _, e := time.Parse(time.RFC3339Nano, r.StartedAt); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(apiErr)
	for _, secret := range []string{"header-secret", "query-secret", "body-secret", "access_token", "password", "not JSON"} {
		if strings.Contains(string(raw)+apiErr.Error(), secret) {
			t.Fatalf("request data leaked into trace: %s", secret)
		}
	}
}

func TestHTTPRoundtripAndLogin(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			if r.Header.Get("Authorization") != "" {
				t.Error("login sent existing token")
			}
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request["username"] != "alice" || request["password"] != "pass" || request["provider"] != "simple" {
				t.Errorf("login: %#v", request)
			}
			_, _ = w.Write([]byte(`{"token":"new-token","user":{"id":"alice"}}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing token")
		}
		switch r.URL.Path {
		case "/api/external/v1/tools":
			if r.Method != http.MethodGet {
				t.Error("wrong discovery method")
			}
			_, _ = w.Write([]byte(`{"tools":[{"name":"get_plan","description":"Read plan","inputSchema":{"type":"object","properties":{"workflow_id":{"type":"string"}},"required":["workflow_id"]}}]}`))
		case "/api/external/v1/call":
			calls++
			var body struct {
				Name      string
				Arguments map[string]any
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Name != "get_plan" || body.Arguments["workflow_id"] != "wf-1" {
				t.Errorf("unexpected body: %#v", body)
			}
			_, _ = w.Write([]byte(`{"revision":"abc","plan":{"steps":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c, err := New(s.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := c.Tools(context.Background())
	if err != nil || len(definitions) != 1 {
		t.Fatalf("discovery: %v %v", definitions, err)
	}
	result, err := c.Call(context.Background(), "get_plan", map[string]any{"workflow_id": "wf-1"})
	if err != nil || !strings.Contains(string(result), `"revision":"abc"`) || calls != 1 {
		t.Fatalf("call: %s %v", result, err)
	}
	token, err := c.Login(context.Background(), "simple", "alice", "pass")
	if err != nil || token != "new-token" {
		t.Fatalf("login: %q %v", token, err)
	}
}

func TestTransportSecurityAndErrors(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?token=x", "ftp://localhost", "https://example.com/#x", "https:///missing"} {
		if _, err := New(raw, "secret"); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	for _, raw := range []string{"https://example.com/base/", "http://localhost:8080", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		if _, err := New(raw, "secret"); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c, _ := New(origin.URL, "secret")
	_, err := c.Call(context.Background(), "write_file", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 307 || redirected.Load() {
		t.Fatalf("redirect was followed or not reported: %v", err)
	}
	for _, tc := range []struct {
		body    string
		code    string
		message string
	}{{`{"error":{"code":"revision_conflict","message":"Read the current revision"}}`, "revision_conflict", "Read the current revision"}, {`{"error":"Invalid credentials"}`, "http_error", "Invalid credentials"}, {`<html>not JSON</html>`, "http_error", "Conflict"}} {
		t.Run(tc.message, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			client, _ := New(s.URL, "secret")
			_, err := client.Call(context.Background(), "update_scripted_step", nil)
			var e *APIError
			if !errors.As(err, &e) || e.Code != tc.code || e.Message != tc.message {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestMissingAuthenticationAndCancellation(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer s.Close()
	c, _ := New(s.URL, "")
	if _, err := c.Tools(context.Background()); err == nil {
		t.Fatal("accepted unauthenticated call")
	}
	c, _ = New(s.URL, "token")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, "get_plan", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("request reached server")
	}
}

func TestNoContentCancellationSuccess(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer s.Close()
	c, _ := New(s.URL, "token")
	result, err := c.Call(context.Background(), "builder_cancel", map[string]any{"workflow_id": "wf", "session_id": "s"})
	if err != nil || string(result) != `{}` {
		t.Fatalf("cancellation: %s %v", result, err)
	}
}

func TestPrivateAtomicConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg := Config{Server: "https://agentworks.example", Token: "first"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %o", info.Mode().Perm())
	}
	cfg.Token = "second"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got != cfg {
		t.Fatalf("read: %#v %v", got, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary credential left behind: %#v", entries)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("accepted world-readable credentials")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(link, cfg); err == nil {
		t.Fatal("replaced symbolic link")
	}
	if _, err := LoadConfig(link); err == nil {
		t.Fatal("read symbolic link")
	}
}

type fakeCaller struct {
	definitions []Tool
	name        string
	arguments   map[string]any
	err         error
}

func (f *fakeCaller) Tools(context.Context) ([]Tool, error) { return f.definitions, nil }
func (f *fakeCaller) Call(_ context.Context, name string, args map[string]any) (json.RawMessage, error) {
	f.name = name
	f.arguments = args
	return json.RawMessage(`{"saved":true}`), f.err
}

func TestMCPSchemaParityAndDispatch(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"workflow_id":{"type":"string"},"patch":{"anyOf":[{"type":"string"},{"type":"object"}]}},"required":["workflow_id"],"additionalProperties":false}`)
	caller := &fakeCaller{definitions: []Tool{{Name: "update_scripted_step", Description: "Native server tool", InputSchema: schema}, {Name: "get_plan", InputSchema: json.RawMessage(`{"type":"object"}`)}}}
	bridge, err := NewMCPServer(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	tool := bridge.GetTool("update_scripted_step")
	if tool == nil {
		t.Fatal("missing tool")
	}
	encoded, err := json.Marshal(tool.Tool)
	if err != nil {
		t.Fatal(err)
	}
	var description map[string]any
	_ = json.Unmarshal(encoded, &description)
	var expected any
	_ = json.Unmarshal(schema, &expected)
	actual := description["inputSchema"].(map[string]any)
	common := actual["properties"].(map[string]any)
	if common["guidance_session_id"] == nil {
		t.Fatal("stdio lacks common guidance envelope")
	}
	delete(common, "guidance_session_id")
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("schema changed: %s", encoded)
	}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "update_scripted_step", Arguments: map[string]any{"workflow_id": "wf-1", "patch": "x"}}}
	result, err := tool.Handler(context.Background(), request)
	if err != nil || result.IsError || caller.name != "update_scripted_step" || caller.arguments["patch"] != "x" {
		t.Fatalf("dispatch: %#v %v", result, err)
	}
	if result.StructuredContent == nil {
		t.Fatal("MCP success lacks structured content")
	}
	caller.err = &APIError{Status: 409, Code: "revision_conflict", Message: "stale"}
	result, err = tool.Handler(context.Background(), request)
	if err != nil || !result.IsError {
		t.Fatalf("MCP error not surfaced: %#v %v", result, err)
	}
}

// PLAT-856: temporary restart failures must not require hand-written send loops,
// duplicate a stored submission, or retry access refusals/unkeyed mutations.
func TestMessageSendRetriesSameSubmissionOnly(t *testing.T) {
	for _, tc := range []struct {
		name, action, submission string
		status                   int
		retry                    bool
	}{
		{"restart", "send", "stable", 503, true},
		{"accepted_then_gateway_lost_reply", "send", "stable", 502, true},
		{"forbidden", "send", "stable", 403, false},
		{"unkeyed", "send", "", 503, false},
		{"other_mutation", "delete", "stable", 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts, stored := 0, 0
			seen := map[string]bool{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ Arguments map[string]any }
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Arguments["submission_id"] != tc.submission {
					t.Error("submission changed")
				}
				attempts++
				if (tc.status == 502 || attempts > 1) && !seen[tc.submission] {
					seen[tc.submission] = true
					stored++
				}
				if attempts == 1 {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(`{"message_id":"original","accepted":true}`))
			}))
			defer srv.Close()
			c, _ := New(srv.URL, "token")
			raw, err := c.Call(context.Background(), "messages", map[string]any{"action": tc.action, "submission_id": tc.submission, "message": "body"})
			if tc.retry {
				if err != nil || attempts != 2 || stored != 1 || !strings.Contains(string(raw), "original") {
					t.Fatalf("retry: %s %v attempts=%d stored=%d", raw, err, attempts, stored)
				}
			} else if err == nil || attempts != 1 {
				t.Fatalf("must refuse once: %v attempts=%d", err, attempts)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := retryAgentRequest(ctx, "send", func(context.Context) (json.RawMessage, error) { calls++; cancel(); return nil, &APIError{Status: 503} })
	if err == nil || calls != 1 {
		t.Fatal("cancelled send retried")
	}
}
