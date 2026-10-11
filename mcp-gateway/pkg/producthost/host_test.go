package producthost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

func TestEncryptedPermissionStateSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.sqlite")
	st, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddUser(store.User{ID: "other", WorkspaceID: "elsewhere"})
	h := New(st)
	row, err := h.SetLLMModelAccessWithDenials("w", "model", []string{"alice"}, nil, []string{}, 0, "admin")
	if err != nil || row.Revision != 1 {
		t.Fatal(row, err)
	}
	if _, err = h.SetLLMModelAccessWithDenials("w", "model", nil, nil, nil, 0, "admin"); !errors.Is(err, api.ErrRevisionConflict) {
		t.Fatal("stale revision accepted", err)
	}
	if _, err = h.SetLLMModelAccessWithDenials("w", "model", []string{"other"}, nil, nil, 1, "admin"); err == nil {
		t.Fatal("cross-workspace permission accepted")
	}
	if _, err = h.SetLLMModelAccessWithDenials("w", "model", nil, nil, []string{}, 1, "admin"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	row, ok := New(st).GetLLMModelAccess("w", "model")
	if !ok || row.Revision != 2 || len(row.UserIDs) != 0 || len(row.GroupIDs) != 0 {
		t.Fatal("empty revocation did not survive restart", row)
	}
}
func TestDurableAuditRoundTripAndAdmissionFailClosed(t *testing.T) {
	st := store.NewMemoryStore()
	h := New(st)
	e := api.AuditEvent{ID: "llm-one", CallID: "llm-one", WorkspaceID: "w", UserID: "alice", Timestamp: time.Now(), PublicName: "llm:model", Decision: "allow", Outcome: "ok", LLMCost: &api.LLMCost{Status: "interrupted_estimate", AmountNanoUSD: 123}}
	if h.BeginAudit(e) == nil {
		t.Fatal("memory-only audit admitted inference")
	}
	b, err := store.OpenAuditBackend(store.AuditOptions{Provider: "sqlite", StateDir: t.TempDir(), Retention: time.Hour, WriteMode: "durable"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st.SetAuditBackend(b)
	if err = h.BeginAudit(e); err != nil {
		t.Fatal(err)
	}
	rowsBefore, readErr := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w"})
	if readErr != nil || len(rowsBefore) != 1 || rowsBefore[0].Phase != "started" {
		t.Fatal("unmatched admission hidden", rowsBefore, readErr)
	}
	if err = h.AppendAudit(e); err != nil {
		t.Fatal(err)
	}
	rows, err := h.Query(context.Background(), api.AuditFilter{WorkspaceID: "w"})
	if err != nil || len(rows) != 1 || rows[0].LLMCost.AmountNanoUSD != 123 {
		t.Fatal("audit cost round trip/dedup failed", rows, err)
	}
	rows, err = h.Query(context.Background(), api.AuditFilter{WorkspaceID: "other"})
	if err != nil || len(rows) != 0 {
		t.Fatal("cross-workspace audit leak")
	}
}
func TestMountedProductRejectsUntrustedActorAndRoleHeaders(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	if err := st.EnsurePlatformGroup("w"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	secret := "private-test-service-credential-32-chars"
	f := func(h api.Host) (api.Product, error) {
		return api.Product{ID: "example", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := h.Authenticate(r)
			if !ok {
				w.WriteHeader(401)
				return
			}
			if !h.IsAdmin(r.Context(), id) {
				w.WriteHeader(403)
				return
			}
			w.WriteHeader(204)
		})}, nil
	}
	if err := Mount(mux, st, "w", func(string) (string, error) { return secret, nil }, []api.Factory{f}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token, origin, admin string
		want                 int
	}{{"", "", "1", 401}, {"wrong", "", "1", 401}, {secret, "https://evil.test", "1", 401}, {secret, "", "", 403}, {secret, "", "1", 204}} {
		r := httptest.NewRequest("GET", "/api/admin/products/example/test", nil)
		r.Header.Set("X-AgentWorks-Actor", "alice")
		r.Header.Set("X-AgentWorks-Admin", tc.admin)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d want %d", w.Code, tc.want)
		}
	}
}

func TestGroupMetadataRoundTripAndOwnersAreDetached(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddGroup(store.Group{ID: "g", WorkspaceID: "w", Name: "Team", Description: "Shared access", BuiltIn: true, Kind: "vault", Owners: []string{"alice"}})
	h := New(st)
	g, ok := h.GetGroup("g")
	if !ok || g.Description != "Shared access" || !g.BuiltIn || g.Kind != "vault" || len(g.Owners) != 1 || g.Owners[0] != "alice" {
		t.Fatal("group metadata dropped", g)
	}
	g.Owners[0] = "mutated"
	again, _ := h.GetGroup("g")
	if again.Owners[0] != "alice" {
		t.Fatal("returned owner slice aliases store state")
	}
}
