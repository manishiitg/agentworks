package e2e

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestPaginatedUpstreamKeepsAllApprovedToolsOnResync(t *testing.T) {
	srv := server.NewMCPServer("paged-upstream", "0.1", server.WithPaginationLimit(1))
	for i := 1; i <= 3; i++ {
		srv.AddTool(mcp.Tool{Name: fmt.Sprintf("tool%d", i)}, echoHandler())
	}
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv, server.WithEndpointPath("/mcp"), server.WithStateLess(true)))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1"})
	connector := store.Connector{ID: "c1", WorkspaceID: "w1", Provider: "paged", UpstreamURL: ts.URL + "/mcp", Status: store.StatusActive}
	st.AddConnector(connector)
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	if err := gw.AddConnector(ctx, connector); err != nil {
		t.Fatalf("add paginated upstream: %v", err)
	}
	defer gw.RemoveConnector(connector.ID)

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("paged__tool%d", i)
		snap, ok := st.GetTool(name)
		if !ok {
			t.Fatalf("missing tool from page %d", i)
		}
		if snap.Status != store.StatusActive || snap.ApprovedFingerprint != snap.Fingerprint {
			t.Fatalf("initial connection did not approve %s: %+v", name, snap)
		}
	}
	if err := gw.Resync(ctx, connector); err != nil {
		t.Fatalf("resync paginated upstream: %v", err)
	}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("paged__tool%d", i)
		snap, ok := st.GetTool(name)
		if !ok || snap.Status != store.StatusActive {
			t.Fatalf("%s lost approval on paginated resync: %+v", name, snap)
		}
	}
}
