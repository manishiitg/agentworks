// Command server runs the MCP Gateway: one workspace's governed remote MCP
// endpoint plus the (M1+) admin API.
//
// M0 configuration is static: one workspace, one human, one upstream
// connector, grants named by GATEWAY_GRANT_TOOLS (comma-separated upstream
// tool names, granted after discovery). MCP clients sign in through the
// shared OAuth authorization server; the human approves consent with
// GATEWAY_HUMAN_TOKEN until the shared IdP lands.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
	"github.com/manishiitg/coding-agent-loop/mcpoauth"
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
	upstreamURL := env("GATEWAY_UPSTREAM_URL", "https://mcp.context7.com/mcp")
	provider := env("GATEWAY_PROVIDER", "context7")
	grants := strings.Split(env("GATEWAY_GRANT_TOOLS", ""), ",")
	publicURL := env("GATEWAY_PUBLIC_URL", "http://127.0.0.1:"+port)
	stateDir := env("GATEWAY_STATE_DIR", filepath.Join(".", "var"))
	humanToken := env("GATEWAY_HUMAN_TOKEN", "m0-human-token")
	human := mcpoauth.User{ID: "u1", Username: "m0", Email: "m0@example.com", Provider: "m0-static"}

	if os.Getenv("GATEWAY_HUMAN_TOKEN") == "" {
		log.Printf("gateway: GATEWAY_HUMAN_TOKEN unset, using insecure dev default")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}

	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "m0"})
	st.AddUser(store.User{ID: human.ID, WorkspaceID: "w1", Email: human.Email})
	st.AddConnector(store.Connector{
		ID: "c1", WorkspaceID: "w1", Provider: provider,
		Label: provider, UpstreamURL: upstreamURL, Status: store.StatusActive,
	})

	oauthSrv := mcpoauth.NewServer(mcpserver.OAuthConfig(publicURL, filepath.Join(stateDir, "mcp-oauth.sqlite"), humanToken, human))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	up, err := upstream.Dial(ctx, upstreamURL)
	if err != nil {
		return err
	}
	defer up.Close()

	gw := mcpserver.New(st, auth.OAuth{Server: oauthSrv, WorkspaceID: "w1"},
		map[string]*upstream.Client{"c1": up}, oauthSrv)

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
		st.AddGrant(store.Grant{UserID: human.ID, PublicName: public})
		log.Printf("gateway: granted %s", public)
	}

	log.Printf("gateway: listening on :%s (upstream %s)", port, upstreamURL)
	return http.ListenAndServe(":"+port, gw.Handler())
}
