package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func TestConcurrentResyncAndDeleteCannotRestoreConnector(t *testing.T) {
	upstreamSrv := fakeUpstream(t)
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1"})
	gw := mcpserver.New(st, auth.OAuth{}, map[string]*upstream.Client{}, nil, upstream.DialOptions{AllowPrivate: true})
	adm := &admin.Admin{Store: st, Gateway: gw, WorkspaceID: "w1"}

	for i := 0; i < 10; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		connector, err := adm.AddConnectorCustom(ctx, "race", "race", "", upstreamSrv.URL+"/mcp")
		if err != nil {
			cancel()
			t.Fatalf("add connector %d: %v", i, err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = adm.SyncConnector(ctx, connector.ID) // deletion may win the race
		}()
		go func() {
			defer wg.Done()
			<-start
			if err := adm.DeleteConnector(connector.ID); err != nil {
				t.Errorf("delete connector %d: %v", i, err)
			}
		}()
		close(start)
		wg.Wait()
		cancel()
		if _, ok := st.GetConnector(connector.ID); ok {
			t.Fatalf("connector %d survived deletion", i)
		}
		if tools := st.ListTools("w1"); len(tools) != 0 {
			t.Fatalf("connector %d restored tools after deletion: %+v", i, tools)
		}
	}
}
