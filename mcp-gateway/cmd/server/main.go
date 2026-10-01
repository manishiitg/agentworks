// Command server runs the local-alpha MCP Gateway: one workspace's governed
// remote MCP endpoint plus the admin API.
//
// M0 configuration is static: one workspace, one human, one upstream
// connector, grants named by GATEWAY_GRANT_TOOLS (comma-separated upstream
// tool names, granted after discovery). MCP clients sign in through the
// shared OAuth authorization server; the local human approves consent with
// GATEWAY_HUMAN_TOKEN until individual sign-in lands.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/admin"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/catalog"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/mcpserver"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/setupagent"
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

// resolveBind defaults local runs to loopback. validateExposure rejects all
// non-loopback binds for this local-alpha release.
func resolveBind(bind, humanToken string) (string, error) {
	if bind == "" {
		bind = "127.0.0.1"
	}
	ip := net.ParseIP(bind)
	if bind != "localhost" && (ip == nil || !ip.IsLoopback()) &&
		(len(humanToken) < 16 || humanToken == "local-admin" || humanToken == "m0-human-token") {
		return "", errors.New("refusing non-loopback bind without a non-default GATEWAY_HUMAN_TOKEN of at least 16 characters")
	}
	return bind, nil
}

// Validate the advertised endpoint independently of the process bind address.
func validatePublicURL(raw, humanToken string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("GATEWAY_PUBLIC_URL must be an http(s) origin without a path, credentials, query, or fragment")
	}
	if !loopbackURL(raw) {
		if u.Scheme != "https" {
			return errors.New("public GATEWAY_PUBLIC_URL must use HTTPS")
		}
		if len(humanToken) < 32 || humanToken == "local-admin" || humanToken == "m0-human-token" {
			return errors.New("public GATEWAY_PUBLIC_URL requires an explicit GATEWAY_HUMAN_TOKEN of at least 32 characters")
		}
	}
	return nil
}

func validateExposure(bind, publicURL string) error {
	// This release has one static OAuth human and local governance. Keep
	// the alpha on the same machine until individual MCP OAuth identity and
	// production deployment requirements are implemented. This check cannot detect an independently configured
	// reverse proxy, which operators must keep private.
	if !loopbackURL(publicURL) {
		return errors.New("public CapLayer is unavailable until individual MCP sign-in and production deployment requirements are configured")
	}
	ip := net.ParseIP(bind)
	if bind != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("CapLayer alpha must bind to loopback")
	}
	return nil
}

// localAdminToken creates a fresh secret for this process. The console user
// reads the 0600 file and enters it once per browser session. No loopback
// request receives admin authority merely because of its source address.
func localAdminToken(stateDir string) (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random[:])
	f, err := os.CreateTemp(stateDir, ".admin-token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return "", err
	}
	if _, err := f.WriteString(token); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(stateDir, "admin-token")
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	log.Printf("gateway: local admin token written to %s", path)
	return token, nil
}

func run() error {
	port := env("GATEWAY_PORT", "8080")
	upstreamURL := env("GATEWAY_UPSTREAM_URL", "https://mcp.context7.com/mcp")
	provider := env("GATEWAY_PROVIDER", "context7")
	grants := strings.Split(env("GATEWAY_GRANT_TOOLS", ""), ",")
	publicURL := env("GATEWAY_PUBLIC_URL", "http://127.0.0.1:"+port)
	stateDir := env("GATEWAY_STATE_DIR", filepath.Join(".", "var"))
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	humanToken := os.Getenv("GATEWAY_HUMAN_TOKEN")
	if humanToken == "" && loopbackURL(publicURL) {
		var err error
		humanToken, err = localAdminToken(stateDir)
		if err != nil {
			return err
		}
	}
	if len(humanToken) < 32 || humanToken == "local-admin" || humanToken == "m0-human-token" {
		return errors.New("GATEWAY_HUMAN_TOKEN must be a unique secret of at least 32 characters, or unset for an auto-generated local token")
	}
	if err := validatePublicURL(publicURL, humanToken); err != nil {
		return err
	}
	publicURL = strings.TrimRight(publicURL, "/")
	bind, err := resolveBind(env("GATEWAY_BIND", ""), humanToken)
	if err != nil {
		return err
	}
	if err := validateExposure(bind, publicURL); err != nil {
		return err
	}
	allowPrivateUpstreams := os.Getenv("GATEWAY_ALLOW_PRIVATE_UPSTREAMS") == "1"
	if os.Getenv("GATEWAY_LOCAL_ADMIN") == "1" {
		return errors.New("GATEWAY_LOCAL_ADMIN no longer bypasses authentication; remove this setting")
	}
	human := mcpoauth.User{ID: "u1", Username: "m0", Email: "m0@example.com", Provider: "m0-static"}

	cat, err := catalog.Load()
	if path := strings.TrimSpace(os.Getenv("GATEWAY_CATALOG_PATH")); path != "" {
		cat, err = catalog.LoadFile(path)
	}
	if err != nil {
		return err
	}
	log.Printf("gateway: catalog has %d providers", len(cat.Providers))

	databasePath, keyPath := gatewayConfigurationPaths(stateDir)
	if err := store.MigrateSQLiteConfiguration(filepath.Join(stateDir, "gateway.sqlite"), databasePath, keyPath); err != nil {
		return fmt.Errorf("relocate gateway configuration: %w", err)
	}
	st, err := store.NewSQLiteStoreWithKey(databasePath, keyPath)
	if err != nil {
		return fmt.Errorf("load gateway configuration: %w", err)
	}
	defer st.Close()
	st.AddWorkspace(store.Workspace{ID: "w1", Name: "m0"})
	st.AddUser(store.User{ID: human.ID, WorkspaceID: "w1", Email: human.Email})
	if err := st.EnableSQLWorkspace("w1"); err != nil {
		return fmt.Errorf("initialize project SQL tables: %w", err)
	}
	if os.Getenv("GATEWAY_DEMO") != "" {
		seedDemo(st)
	}

	oauthSrv := mcpoauth.NewServer(mcpserver.OAuthConfig(publicURL, filepath.Join(stateDir, "mcp-oauth.sqlite"), humanToken, human))

	gw := mcpserver.New(st, auth.OAuth{Server: oauthSrv, WorkspaceID: "w1", Keys: st},
		map[string]*upstream.Client{}, oauthSrv, upstream.DialOptions{AllowPrivate: allowPrivateUpstreams})

	if productURL := strings.TrimSpace(os.Getenv("GATEWAY_PRODUCT_URL")); productURL != "" {
		resolve, err := upstream.SharedOAuth(productURL, humanToken)
		if err != nil {
			return err
		}
		gw.SetSharedOAuth(resolve)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	newInitialConnector := false
	if upstreamURL != "" && upstreamURL != "none" {
		if _, exists := st.GetConnector("c1"); !exists {
			newInitialConnector = true
			st.AddConnector(store.Connector{
				ID: "c1", WorkspaceID: "w1", Provider: catalog.Key(provider),
				Label: provider, UpstreamURL: upstreamURL, Status: store.StatusActive,
			})
		}
	}
	for _, c := range st.ListConnectors("w1") {
		if c.Status != store.StatusActive {
			continue
		}
		var reconnectErr error
		if c.ID == "c1" && newInitialConnector {
			reconnectErr = gw.AddConnector(ctx, c)
		} else {
			reconnectErr = gw.Resync(ctx, c)
		}
		if err := reconnectErr; err != nil {
			// An unavailable upstream must not discard saved permissions.
			log.Printf("gateway: could not reconnect %s: %v", c.ID, err)
		}
	}
	if err := st.PersistenceError(); err != nil {
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
		log.Printf("gateway: granted %s (tool still requires admin approval)", public)
	}

	adm := &admin.Admin{
		Store: st, Gateway: gw, Catalog: cat,
		WorkspaceID: "w1", HumanToken: humanToken, PublicURL: publicURL,
	}
	if endpoint := os.Getenv("CAPLAYER_AGENT_API_URL"); endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !loopbackURL(endpoint)) {
			return errors.New("CAPLAYER_AGENT_API_URL must be HTTPS or a loopback HTTP endpoint")
		}
		adm.SetupAgent = &setupagent.Agent{Endpoint: endpoint, APIKey: os.Getenv("CAPLAYER_AGENT_API_KEY"), Model: os.Getenv("CAPLAYER_AGENT_MODEL"), AvailableModels: strings.Split(os.Getenv("CAPLAYER_AGENT_MODELS"), ",")}
	}
	mux := gw.Handler()
	adm.APIRoutes(mux)
	adm.UIRoutes(mux)
	if staticDir := os.Getenv("CAPLAYER_STATIC_DIR"); staticDir != "" {
		productAPI, err := productAPIBase(os.Getenv("CAPLAYER_PRODUCT_API_URL"))
		if err != nil {
			return err
		}
		if info, err := os.Stat(filepath.Join(staticDir, "caplayer.html")); err != nil || info.IsDir() {
			return errors.New("CAPLAYER_STATIC_DIR must contain caplayer.html")
		}
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/caplayer.html", http.StatusTemporaryRedirect)
		})
		mux.Handle("GET /caplayer.html", http.FileServer(http.Dir(staticDir)))
		mux.HandleFunc("GET /caplayer-config.js", func(w http.ResponseWriter, r *http.Request) {
			config, _ := json.Marshal(map[string]any{"apiBaseUrl": productAPI, "workspaceApiBaseUrl": productAPI + "/api/wp", "gatewayUrl": publicURL, "enabledProductSurfaces": []string{"mcp-gateway"}})
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(append(append([]byte("window.__APP_RUNTIME_CONFIG__="), config...), ';'))
		})
		mux.Handle("GET /assets/", http.FileServer(http.Dir(staticDir)))
	}

	log.Printf("gateway: listening on %s:%s (upstream %s)", bind, port, upstreamURL)
	return http.ListenAndServe(net.JoinHostPort(bind, port), admin.LocalhostCORS(mux))
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// seedDemo creates local-test users and groups.
func seedDemo(st *store.MemoryStore) {
	st.AddUser(store.User{ID: "alice", WorkspaceID: "w1", Email: "alice@example.com"})
	st.AddUser(store.User{ID: "bob", WorkspaceID: "w1", Email: "bob@example.com"})
	st.AddGroup(store.Group{ID: "eng", WorkspaceID: "w1", Name: "Engineering"})
	st.AddGroup(store.Group{ID: "support", WorkspaceID: "w1", Name: "Support"})
	st.AddMember("eng", "alice")
	st.AddMember("support", "bob")
}

// The standalone UI uses the same product account service, never a gateway
// token login. Only this public API URL is exposed in its runtime config.
func productAPIBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopbackURL(u.String()))) {
		return "", errors.New("CAPLAYER_PRODUCT_API_URL must be the existing product API base URL (HTTPS or loopback)")
	}
	return u.String(), nil
}

// The launcher supplies the actual CapLayer project folder. Standalone callers
// without a workspace retain the established state-directory location.
func gatewayConfigurationPaths(stateDir string) (string, string) {
	path := filepath.Join(stateDir, "gateway.sqlite")
	if root := strings.TrimSpace(os.Getenv("GATEWAY_WORKSPACE_DIR")); root != "" {
		path = filepath.Join(root, "db", "gateway.sqlite")
	}
	return path, filepath.Join(stateDir, "gateway.sqlite.key")
}
