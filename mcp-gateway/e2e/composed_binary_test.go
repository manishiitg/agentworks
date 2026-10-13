package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// TestComposedVaultBinaryMigration exercises an installed artifact, rather than
// substituting the public in-process gateway for private-runtime verification.
func TestComposedVaultBinaryMigration(t *testing.T) {
	private := os.Getenv("GATEWAY_E2E_BINARY")
	public := os.Getenv("GATEWAY_E2E_PUBLIC_BINARY")
	if private == "" || public == "" {
		t.Skip("set GATEWAY_E2E_BINARY and GATEWAY_E2E_PUBLIC_BINARY")
	}
	upstream, srv := mutableUpstream(t)
	var calls atomic.Int32
	srv.AddTool(schemaTool("fetch", `{"type":"object","properties":{"project":{"type":"string"}},"required":["project"]}`), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return mcp.NewToolResultText("ok"), nil
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	state := t.TempDir()
	credential := strings.Repeat("fake-test-", 4)
	request := func(method, path string, body any) []byte {
		t.Helper()
		var input io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = strings.NewReader(string(data))
		}
		req, err := http.NewRequest(method, origin+path, input)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, data)
		}
		return data
	}
	start := func(binary, backend string) func() {
		t.Helper()
		logPath := filepath.Join(state, backend+".log")
		log, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), "AGENTWORKS_VAULT_BACKEND="+backend, "GATEWAY_PORT="+port, "GATEWAY_BIND=127.0.0.1", "GATEWAY_PUBLIC_URL="+origin, "GATEWAY_AUTH_MODE=local", "GATEWAY_PRODUCT_URL=", "GATEWAY_STATE_DIR="+filepath.Join(state, "state"), "GATEWAY_WORKSPACE_DIR=", "GATEWAY_HUMAN_TOKEN="+credential, "GATEWAY_HUMAN_TOKEN_FILE=", "GATEWAY_UPSTREAM_URL=none", "GATEWAY_DEMO=", "GATEWAY_GRANT_TOOLS=", "GATEWAY_ALLOW_PRIVATE_UPSTREAMS=1", "CAPLAYER_STATIC_DIR=", "CAPLAYER_AGENT_API_URL=", "VAULT_AUDIT_PROVIDER=sqlite", "VAULT_AUDIT_WRITE_MODE=durable")
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					_ = cmd.Process.Kill()
					<-done
					t.Error("Vault did not drain gracefully")
				}
				log.Close()
			})
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-done:
				data, _ := os.ReadFile(logPath)
				t.Fatalf("Vault exited: %s", data)
			default:
			}
			response, err := http.Get(origin + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return stop
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("Vault listener not ready")
		return stop
	}
	var access, name, connector string
	auditCount := func() int {
		var body struct {
			Events []store.AuditEvent `json:"events"`
		}
		if err := json.Unmarshal(request(http.MethodGet, "/api/admin/audit?limit=100", nil), &body); err != nil {
			t.Fatal(err)
		}
		return len(body.Events)
	}
	for phase, entry := range []struct{ binary, backend string }{{public, "builtin"}, {private, "private"}, {public, "builtin"}} {
		stop := start(entry.binary, entry.backend)
		if phase == 0 {
			var connected struct {
				Connector store.Connector `json:"connector"`
			}
			if err := json.Unmarshal(request(http.MethodPost, "/api/admin/setup/tool", map[string]any{"operation": "connect_server", "arguments": map[string]any{"name": "Migration", "url": upstream.URL + "/mcp"}}), &connected); err != nil {
				t.Fatal(err)
			}
			connector = connected.Connector.ID
			var inventory struct {
				Tools []store.ToolSnapshot `json:"tools"`
			}
			if err := json.Unmarshal(request(http.MethodGet, "/api/admin/tools", nil), &inventory); err != nil {
				t.Fatal(err)
			}
			var tool store.ToolSnapshot
			for _, candidate := range inventory.Tools {
				if candidate.ConnectorID == connector && strings.HasSuffix(candidate.PublicName, "__fetch") {
					tool = candidate
					break
				}
			}
			name = tool.PublicName
			if name == "" {
				t.Fatal("connected tool absent")
			}
			// Remove default Platform access before applying the restricted test group.
			request(http.MethodDelete, "/api/admin/groups/"+store.PlatformGroupID("w1")+"/servers/"+connector, nil)
			request(http.MethodPost, "/api/admin/groups", map[string]any{"ID": "migration", "Name": "Migration"})
			request(http.MethodPost, "/api/admin/groups/migration/members", map[string]any{"user_id": "u1"})
			request(http.MethodPost, "/api/admin/access/packages", map[string]any{"id": "migration-regex", "group_id": "migration", "name": "Only one project", "rules": []any{map[string]any{"public_name": name, "fingerprint": tool.Fingerprint, "conditions": []any{map[string]any{"path": "/project", "op": "matches", "value": "one", "description": "Only project one is allowed"}}}}})
			access = fetchAccessToken(t, origin, credential)
		}
		if count := auditCount(); count != phase*2 {
			t.Fatalf("phase %d lost prior audit rows: %d", phase, count)
		}
		client := dialGateway(t, origin+"/mcp", access)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = map[string]any{"project": "one"}
		result, err := client.CallTool(ctx, req)
		if err != nil || result.IsError {
			t.Fatalf("phase %d allowed call: %+v %v", phase, result, err)
		}
		req.Params.Arguments = map[string]any{"project": "two"}
		result, err = client.CallTool(ctx, req)
		if err == nil && !result.IsError {
			t.Fatalf("phase %d regex restriction disappeared", phase)
		}
		cancel()
		client.Close()
		if got := calls.Load(); got != int32(phase+1) {
			t.Fatalf("denied call reached upstream: %d", got)
		}
		if count := auditCount(); count != (phase+1)*2 {
			t.Fatalf("phase %d missing allowed/denied audit rows: %d", phase, count)
		}
		stop()
	}
}
