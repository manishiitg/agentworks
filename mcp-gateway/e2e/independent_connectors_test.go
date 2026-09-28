package e2e

import (
	"context"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestSlowResyncDoesNotBlockOtherConnectorDelete(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var block atomic.Bool
	hooks := &server.Hooks{}
	hooks.AddAfterListTools(func(_ context.Context, _ any, _ *mcp.ListToolsRequest, _ *mcp.ListToolsResult) {
		if block.Load() {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		}
	})
	slow := server.NewMCPServer("slow", "0.1", server.WithHooks(hooks))
	slow.AddTool(mcp.Tool{Name: "ping"}, echoHandler())
	slowHTTP := httptest.NewServer(server.NewStreamableHTTPServer(slow, server.WithEndpointPath("/mcp"), server.WithStateLess(true)))
	defer slowHTTP.Close()
	defer unblock()
	fastHTTP := fakeUpstream(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1"})
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w1"}
	slowConnector, err := adm.AddConnectorCustom(ctx, "slow", "slow", "", slowHTTP.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	fastConnector, err := adm.AddConnectorCustom(ctx, "fast", "fast", "", fastHTTP.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	block.Store(true)
	syncDone := make(chan error, 1)
	go func() { syncDone <- adm.SyncConnector(ctx, slowConnector.ID) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("slow resync did not reach upstream listing")
	}
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- adm.DeleteConnector(fastConnector.ID) }()
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("delete unrelated connector: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated connector deletion waited on slow resync")
	}
	block.Store(false)
	unblock()
	if err := <-syncDone; err != nil {
		t.Fatalf("slow resync after release: %v", err)
	}
	gw.RemoveConnector(slowConnector.ID)
}
