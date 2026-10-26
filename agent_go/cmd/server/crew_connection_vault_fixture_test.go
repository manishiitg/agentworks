package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type crewConnectionVaultFixture struct {
	catalog     string
	mu          sync.Mutex
	connections map[string]map[string]any
}

func (f *crewConnectionVaultFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.connections)
}

// Current central Vault is the external boundary. Platform project selection
// and every owner/token refusal still use the real handlers and workspace.
func newCrewConnectionVaultFixture(t *testing.T) *crewConnectionVaultFixture {
	t.Helper()
	f := &crewConnectionVaultFixture{catalog: filepath.Join(t.TempDir(), "mcp.json"), connections: map[string]map[string]any{}}
	if err := os.WriteFile(f.catalog, []byte(`{"mcpServers":{"Example":{"url":"https://mcp.example.com/mcp","protocol":"http","oauth":{"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("s", 32) {
			t.Error("Vault request lost service authentication")
			w.WriteHeader(401)
			return
		}
		actor := r.Header.Get("X-CapLayer-Actor")
		switch {
		case r.URL.Path == "/api/vaults":
			connections := []map[string]any{}
			if actor == "owner" {
				for _, c := range f.connections {
					connections = append(connections, c)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"vaults": []any{map[string]any{"group": map[string]string{"ID": "v-123456", "Name": personalVaultName}, "role": "owner", "connections": connections}}})
		case r.URL.Path == "/api/vaults/v-123456":
			json.NewEncoder(w).Encode(map[string]any{"role": "owner"})
		case r.URL.Path == "/api/vaults/v-123456/connectors" && r.Method == http.MethodPost:
			var in map[string]any
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				w.WriteHeader(400)
				return
			}
			id := fmt.Sprintf("c-%08x", len(f.connections)+1)
			oauth, status := "", "active"
			if in["Provider"] != nil {
				oauth, status = "Example", "pending"
			}
			c := map[string]any{"ID": id, "id": id, "VaultID": "v-123456", "Label": in["Label"], "Provider": "example", "Status": status, "OAuthServer": oauth, "OAuthCredentialID": id}
			f.connections[id] = c
			json.NewEncoder(w).Encode(c)
		case strings.HasPrefix(r.URL.Path, "/api/admin/connectors/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/admin/connectors/")
			c, ok := f.connections[id]
			if !ok {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(c)
		case r.URL.Path == "/api/admin/runtime/servers":
			servers := []any{}
			if actor == "owner" {
				for id := range f.connections {
					servers = append(servers, map[string]any{"id": id, "label": "Example", "provider": "example", "tools": []any{}})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"servers": servers, "groups": []any{}, "secrets": []any{}})
		default:
			t.Errorf("unexpected Vault boundary %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", strings.Repeat("s", 32))
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	return f
}
