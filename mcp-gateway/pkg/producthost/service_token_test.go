package producthost

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestServiceTokenRequiresDedicatedCredential(t *testing.T) {
	const key = "AGENTWORKS_PRODUCT_LLM_GATEWAY_SERVICE_TOKEN"
	t.Setenv(key, "")
	t.Setenv(key+"_FILE", "")
	t.Setenv("GATEWAY_HUMAN_TOKEN", strings.Repeat("v", 32))
	if _, err := ServiceTokenFromEnv("llm-gateway"); err == nil {
		t.Fatal("Vault admin credential accepted as fallback")
	}
	t.Setenv(key, strings.Repeat("p", 32))
	file := filepath.Join(t.TempDir(), "service-token")
	want := strings.Repeat("f", 32)
	if err := os.WriteFile(file, []byte(want+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(key+"_FILE", file)
	if got, err := ServiceTokenFromEnv("llm-gateway"); err != nil || got != want {
		t.Fatal("file did not take precedence", err)
	}
	t.Setenv(key+"_FILE", file+"-missing")
	if _, err := ServiceTokenFromEnv("llm-gateway"); err == nil {
		t.Fatal("unreadable file fell back to env")
	}
	if _, err := ServiceTokenFromEnv("../other"); err == nil {
		t.Fatal("invalid product accepted")
	}
}

func TestMountSeparatesProductCredentials(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	factory := func(id string) api.Factory {
		return func(h api.Host) (api.Product, error) {
			return api.Product{ID: id, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actor, ok := h.Authenticate(r)
				if !ok || !h.IsAdmin(r.Context(), actor) {
					w.WriteHeader(403)
					return
				}
				w.WriteHeader(204)
			})}, nil
		}
	}
	factories := []api.Factory{factory("one"), factory("two")}
	tokens := map[string]string{"one": strings.Repeat("1", 32), "two": strings.Repeat("2", 32)}
	mux := http.NewServeMux()
	if err := Mount(mux, st, "w", func(id string) (string, error) { return tokens[id], nil }, factories); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		for _, owner := range []string{"one", "two"} {
			r := httptest.NewRequest("GET", "/api/admin/products/"+id+"/test", nil)
			r.Header.Set("Authorization", "Bearer "+tokens[owner])
			r.Header.Set("X-AgentWorks-Actor", "alice")
			r.Header.Set("X-AgentWorks-Admin", "1")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			want := 401
			if owner == id {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("product %s credential %s: got %d want %d", id, owner, w.Code, want)
			}
		}
	}
	if err := Mount(http.NewServeMux(), st, "w", func(string) (string, error) { return tokens["one"], nil }, factories); err == nil {
		t.Fatal("shared product credential accepted")
	}
}
