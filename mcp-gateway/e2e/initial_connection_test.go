package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestAdminConnectionApprovesInitialToolsAndStillRequiresGroupAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ts, srv := mutableUpstream(t)
	srv.AddTool(schemaTool("read", `{"type":"object","properties":{"path":{"type":"string"}}}`), echoHandler())
	srv.AddTool(schemaTool("write", `{"type":"object"}`), echoHandler())
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "readers", WorkspaceID: "w"})
	st.AddMember("readers", "alice")
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w"}
	connector, err := adm.AddConnectorCustom(ctx, "local", "local", "", ts.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	defer gw.RemoveConnector(connector.ID)
	alice := auth.Identity{UserID: "alice", WorkspaceID: "w"}
	for _, name := range []string{"local__read", "local__write"} {
		snap, ok := st.GetTool(name)
		if !ok || snap.Status != store.StatusActive || snap.ApprovedFingerprint != snap.Fingerprint {
			t.Fatalf("initial definition not approved: %+v", snap)
		}
		if _, err := policy.Authorize(st, alice, name); !errors.Is(err, policy.ErrNoGrant) {
			t.Fatalf("connection gave alice access to %s: %v", name, err)
		}
	}
	st.AddGroupGrant(store.GroupGrant{GroupID: "readers", PublicName: "local__read"})
	if _, err := policy.Authorize(st, alice, "local__read"); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Authorize(st, alice, "local__write"); !errors.Is(err, policy.ErrNoGrant) {
		t.Fatalf("read grant also allowed write: %v", err)
	}
	if err := adm.SyncConnector(ctx, connector.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Authorize(st, alice, "local__read"); err != nil {
		t.Fatalf("unchanged sync lost access: %v", err)
	}
	srv.AddTool(schemaTool("read", `{"type":"object","properties":{"path":{"type":"string"},"recursive":{"type":"boolean"}}}`), echoHandler())
	srv.AddTool(schemaTool("new_tool", `{"type":"object"}`), echoHandler())
	if err := adm.SyncConnector(ctx, connector.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"local__read", "local__new_tool"} {
		snap, ok := st.GetTool(name)
		if !ok || snap.Status != store.StatusQuarantined {
			t.Fatalf("later definition silently approved: %+v", snap)
		}
		if _, err := policy.Authorize(st, alice, name); !errors.Is(err, policy.ErrToolNotActive) {
			t.Fatalf("unreviewed later definition accessible: %s: %v", name, err)
		}
	}
	unchanged, _ := st.GetTool("local__write")
	if unchanged.Status != store.StatusActive {
		t.Fatal("unchanged definition lost approval")
	}
}

func TestAgentConnectsCustomServerWithoutGrantingAccess(t *testing.T) {
	ts := fakeUpstream(t)
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w"})
	gw := mcpserver.New(st, nil, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w", HumanToken: testHumanToken}
	mux := http.NewServeMux()
	adm.APIRoutes(mux)
	request := func(token, arguments string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/setup/tool", strings.NewReader(`{"operation":"connect_server","arguments":`+arguments+`}`))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	valid := `{"name":"Local test","url":` + strconv.Quote(ts.URL+"/mcp") + `}`
	if rec := request("wrong", valid); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized connection: %d %s", rec.Code, rec.Body)
	}
	for _, args := range []string{`{"name":"","url":"https://example.com/mcp"}`, `{"name":"test","url":"http://example.com/mcp"}`, `{"name":"test","url":"https://user:password@example.com/mcp"}`, strings.TrimSuffix(valid, "}") + `,"bearer_token":"not-for-models"}`} {
		if rec := request(testHumanToken, args); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid connection accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if len(st.ListConnectors("w")) != 0 {
		t.Fatal("denied setup changed inventory")
	}
	rec := request(testHumanToken, valid)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tool_count":2`) || !strings.Contains(rec.Body.String(), `"group_access_assigned":false`) {
		t.Fatalf("connection failed: %d %s", rec.Code, rec.Body)
	}
	connectors := st.ListConnectors("w")
	if len(connectors) != 1 {
		t.Fatalf("unexpected inventory: %+v", connectors)
	}
	defer gw.RemoveConnector(connectors[0].ID)
	for _, tool := range st.ListTools("w") {
		if tool.Status != store.StatusActive || tool.ApprovedFingerprint != tool.Fingerprint {
			t.Fatalf("initial definition not approved: %+v", tool)
		}
		if _, err := policy.Authorize(st, auth.Identity{UserID: "alice", WorkspaceID: "w"}, tool.PublicName); !errors.Is(err, policy.ErrNoGrant) {
			t.Fatalf("connection assigned access: %v", err)
		}
	}
	if rec := request(testHumanToken, valid); rec.Code != http.StatusBadRequest || len(st.ListConnectors("w")) != 1 {
		t.Fatal("duplicate connection was created")
	}
}
