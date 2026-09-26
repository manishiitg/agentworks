package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcpoauth"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

// apiCall hits the JSON admin API with the human token.
func apiCall(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("admin request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testHumanToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin call: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAdminGroupsAndConnectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	upstreamSrv := fakeUpstream(t)
	human := mcpoauth.User{ID: "u1", Username: "e2e", Email: "e2e@example.com", Provider: "e2e"}

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "admin"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "e2e@example.com"})

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	publicURL := "http://" + l.Addr().String()

	cat, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(cat.Providers) == 0 {
		t.Fatalf("empty catalog")
	}

	oauthSrv := mcpoauth.NewServer(mcpserver.OAuthConfig(publicURL,
		filepath.Join(t.TempDir(), "mcp-oauth.sqlite"), testHumanToken, human))
	gw := mcpserver.New(st, auth.OAuth{Server: oauthSrv, WorkspaceID: "w1"},
		map[string]*upstream.Client{}, oauthSrv)
	adm := &admin.Admin{Store: st, Gateway: gw, Catalog: cat,
		WorkspaceID: "w1", HumanToken: testHumanToken, PublicURL: publicURL}
	mux := gw.Handler()
	adm.APIRoutes(mux)
	adm.UIRoutes(mux)
	go http.Serve(l, mux) //nolint:errcheck

	// Add the fake upstream as a custom connector via the API.
	code, data := apiCall(t, "POST", publicURL+"/api/admin/connectors",
		`{"provider":"fake","label":"fake","url":`+strconv.Quote(upstreamSrv.URL+"/mcp")+`}`)
	if code != 201 {
		t.Fatalf("add connector: %d %s", code, data)
	}
	var created struct {
		ID string `json:"ID"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.ID == "" {
		t.Fatalf("add connector decode: %v %s", err, data)
	}

	// Tools appear after sync.
	if n := len(st.ListTools("w1")); n != 2 {
		t.Fatalf("synced tools = %d, want 2", n)
	}

	// Group grant flows to members: create group, add u1, grant the tool.
	if code, data := apiCall(t, "POST", publicURL+"/api/admin/groups", `{"id":"g1","name":"G One"}`); code != 201 {
		t.Fatalf("add group: %d %s", code, data)
	}
	if code, data := apiCall(t, "POST", publicURL+"/api/admin/groups/g1/members", `{"user_id":"u1"}`); code != 200 {
		t.Fatalf("add member: %d %s", code, data)
	}
	if code, data := apiCall(t, "POST", publicURL+"/api/admin/grants", `{"group_id":"g1","tool":"fake__allowed_tool","grant":true}`); code != 200 {
		t.Fatalf("group grant: %d %s", code, data)
	}

	access := fetchAccessToken(t, publicURL, testHumanToken)
	c := dialGateway(t, publicURL+"/mcp", access)
	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "fake__allowed_tool"
	if _, err := c.CallTool(ctx, callReq); err != nil {
		t.Fatalf("group-granted call: %v", err)
	}

	// Removing the member revokes access on the next call.
	if code, data := apiCall(t, "DELETE", publicURL+"/api/admin/groups/g1/members/u1", ""); code != 204 {
		t.Fatalf("remove member: %d %s", code, data)
	}
	if _, err := c.CallTool(ctx, callReq); err == nil {
		t.Fatalf("call after member removal succeeded, want denial")
	}

	// UI pages render.
	uiReq, _ := http.NewRequest("GET", publicURL+"/admin/tools", nil)
	uiReq.AddCookie(&http.Cookie{Name: "gw_admin", Value: testHumanToken})
	uiResp, err := http.DefaultClient.Do(uiReq)
	if err != nil {
		t.Fatalf("ui tools: %v", err)
	}
	defer uiResp.Body.Close()
	uiBody, _ := io.ReadAll(uiResp.Body)
	if uiResp.StatusCode != 200 || !strings.Contains(string(uiBody), "fake__allowed_tool") {
		t.Fatalf("ui tools page: %d, missing tool row", uiResp.StatusCode)
	}

	// Deleting the connector removes its tools; grants dangle and deny.
	if code, data := apiCall(t, "DELETE", publicURL+"/api/admin/connectors/"+created.ID, ""); code != 204 {
		t.Fatalf("delete connector: %d %s", code, data)
	}
	if n := len(st.ListTools("w1")); n != 0 {
		t.Fatalf("tools after delete = %d, want 0", n)
	}
}
