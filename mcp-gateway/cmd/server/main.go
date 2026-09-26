// Command server runs the MCP Gateway: one workspace's governed remote MCP
// endpoint plus the (M1+) admin API.
//
// M0 configuration is static: one workspace, one user, one upstream
// connector, grants named by GATEWAY_GRANT_TOOLS (comma-separated upstream
// tool names, granted after discovery).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("gateway: %v", err)
	}
}

func run() error {
	port := env("GATEWAY_PORT", "8080")
	token := env("GATEWAY_TOKEN", "m0-dev-token")
	upstreamURL := env("GATEWAY_UPSTREAM_URL", "https://mcp.context7.com/mcp")
	provider := env("GATEWAY_PROVIDER", "context7")
	grants := strings.Split(env("GATEWAY_GRANT_TOOLS", ""), ",")

	if os.Getenv("GATEWAY_TOKEN") == "" {
		log.Printf("gateway: GATEWAY_TOKEN unset, using insecure dev default")
	}

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "m0"})
	st.AddUser(store.User{ID: "u1", WorkspaceID: "w1", Email: "m0@example.com"})
	st.AddConnector(store.Connector{
		ID: "c1", WorkspaceID: "w1", Provider: provider,
		Label: provider, UpstreamURL: upstreamURL, Status: store.StatusActive,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	up, err := upstream.Dial(ctx, upstreamURL)
	if err != nil {
		return err
	}
	defer up.Close()

	gw := mcpserver.New(st, auth.StaticToken{
		Token:    token,
		Identity: auth.Identity{UserID: "u1", WorkspaceID: "w1", Email: "m0@example.com"},
	}, map[string]*upstream.Client{"c1": up})

	if err := gw.SyncTools(ctx, "w1"); err != nil {
		return err
	}
	for _, name := range grants {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		public := mcpserver.PublicName(provider, "", name)
		if _, ok := st.GetTool(public); !ok {
			log.Printf("gateway: grant target %q not discovered, skipping", name)
			continue
		}
		st.AddGrant(store.Grant{UserID: "u1", PublicName: public})
		log.Printf("gateway: granted %s", public)
	}

	log.Printf("gateway: listening on :%s (upstream %s)", port, upstreamURL)
	return http.ListenAndServe(":"+port, gw.Handler())
}
