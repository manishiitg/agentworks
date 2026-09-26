// Live M0 proof: gateway fronts the real Context7 upstream. Run with:
//
//	GATEWAY_LIVE_TEST=1 go test ./e2e/ -run TestM0LiveContext7 -v
package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestM0LiveContext7(t *testing.T) {
	if os.Getenv("GATEWAY_LIVE_TEST") == "" {
		t.Skip("set GATEWAY_LIVE_TEST=1 for the live upstream proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	up, err := upstream.Dial(ctx, "https://mcp.context7.com/mcp")
	if err != nil {
		t.Fatalf("dial live upstream: %v", err)
	}
	defer up.Close()

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "live"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "live@example.com"})
	st.AddConnector(store.Connector{
		ID: "c1", WorkspaceID: "w1", Provider: "context7",
		Label: "context7", UpstreamURL: "https://mcp.context7.com/mcp", Status: store.StatusActive,
	})
	gw := mcpserver.New(st, auth.StaticToken{
		Token:    testToken,
		Identity: auth.Identity{UserID: "u1", WorkspaceID: "w1", Email: "live@example.com"},
	}, map[string]*upstream.Client{"c1": up})
	if err := gw.SyncTools(ctx, "w1"); err != nil {
		t.Fatalf("live sync: %v", err)
	}
	discovered := st.ListTools("w1")
	if len(discovered) == 0 {
		t.Fatalf("live upstream exposed zero tools")
	}
	t.Logf("discovered %d tools from live upstream", len(discovered))

	// Grant only resolve-library-id (read-only); everything else stays denied.
	const granted = "context7__resolve-library-id"
	found := false
	for _, d := range discovered {
		if d.PublicName == granted {
			found = true
		}
	}
	if !found {
		t.Skipf("%s not exposed, skipping live call proof", granted)
	}
	st.AddGrant(store.Grant{UserID: "u1", PublicName: granted})

	gwSrv := httptest.NewServer(gw.Handler())
	defer gwSrv.Close()
	c := dialGateway(t, gwSrv.URL+"/mcp")

	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("live tools/list: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0].Name != granted {
		t.Fatalf("live tools/list leaked ungranted tools: %+v", list.Tools)
	}

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = granted
	callReq.Params.Arguments = map[string]any{"libraryName": "react", "query": "react hooks"}
	res, err := c.CallTool(ctx, callReq)
	if err != nil {
		t.Fatalf("live tools/call: %v", err)
	}
	if res.IsError {
		t.Fatalf("live tools/call error result: %+v", res.Content)
	}
	t.Logf("live call ok, result blocks: %d", len(res.Content))

	events := st.ListAudit()
	if len(events) != 1 || events[0].Decision != store.DecisionAllow || events[0].Outcome != store.OutcomeOK {
		t.Fatalf("live audit = %+v, want one allow/ok", events)
	}
}
