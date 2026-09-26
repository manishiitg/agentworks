// Package mcpserver exposes the workspace's governed remote MCP endpoint.
package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcpoauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

// PublicName derives the stable gateway-visible tool name. Single instance:
// "<provider>__<tool>". Multi-instance: "<provider>_<slug>__<tool>".
func PublicName(provider, slug, upstreamName string) string {
	if slug == "" {
		return provider + "__" + upstreamName
	}
	return provider + "_" + slug + "__" + upstreamName
}

// Gateway is one workspace's governed MCP endpoint.
type Gateway struct {
	store     *store.MemoryStore
	auth      auth.Authenticator
	upstreams map[string]*upstream.Client // connector ID -> session
	oauth     *mcpoauth.Server
	mcp       *server.MCPServer
}

// New builds the gateway MCP server. Call SyncTools before serving.
func New(st *store.MemoryStore, a auth.Authenticator, ups map[string]*upstream.Client, oauthSrv *mcpoauth.Server) *Gateway {
	g := &Gateway{store: st, auth: a, upstreams: ups, oauth: oauthSrv}
	hooks := &server.Hooks{}
	// Shape tools/list per caller (visibility only). Deliberately not a
	// ToolFilter: filters also block calls before the handler runs, which
	// would skip the audit trail. Execution-time Authorize in handleCall
	// stays authoritative.
	hooks.AddAfterListTools(func(ctx context.Context, _ any, _ *mcp.ListToolsRequest, result *mcp.ListToolsResult) {
		id, ok := auth.FromContext(ctx)
		if !ok {
			result.Tools = nil
			return
		}
		out := make([]mcp.Tool, 0, len(result.Tools))
		for _, t := range result.Tools {
			if policy.Visible(st, id, t.Name) {
				out = append(out, t)
			}
		}
		result.Tools = out
	})
	g.mcp = server.NewMCPServer("mcp-gateway", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithHooks(hooks),
	)
	return g
}

// SyncTools discovers every active connector's tools, snapshots them, and
// registers their public names. All discovered tools are registered; grants
// control visibility and execution.
func (g *Gateway) SyncTools(ctx context.Context, workspaceID string) error {
	for _, c := range g.store.ListConnectors(workspaceID) {
		if c.Status != store.StatusActive {
			continue
		}
		up, ok := g.upstreams[c.ID]
		if !ok {
			return fmt.Errorf("connector %s (%s): no upstream session", c.ID, c.Label)
		}
		tools, err := up.Discover(ctx)
		if err != nil {
			return fmt.Errorf("connector %s (%s): %w", c.ID, c.Label, err)
		}
		for _, t := range tools {
			snap := g.store.UpsertToolSnapshot(store.ToolSnapshot{
				ConnectorID:  c.ID,
				WorkspaceID:  c.WorkspaceID,
				UpstreamName: t.Name,
				PublicName:   PublicName(c.Provider, c.InstanceSlug, t.Name),
				Description:  t.Description,
				Fingerprint:  store.Fingerprint(t.Name, t.Description, rawSchema(t)),
				DiscoveredAt: time.Now().UTC(),
			})
			g.register(snap, t)
		}
		log.Printf("gateway: synced workspace %s", workspaceID)
	}
	return nil
}

func (g *Gateway) register(snap store.ToolSnapshot, upstreamTool mcp.Tool) {
	public := mcp.Tool{
		Name:           snap.PublicName,
		Description:    upstreamTool.Description,
		InputSchema:    upstreamTool.InputSchema,
		RawInputSchema: upstreamTool.RawInputSchema,
	}
	g.mcp.AddTool(public, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return g.handleCall(ctx, snap, req)
	})
}

// handleCall is the authoritative enforcement point: authenticate (done by
// middleware) → authorize → upstream invoke with timeout → audit everything.
func (g *Gateway) handleCall(ctx context.Context, snap store.ToolSnapshot, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, _ := auth.FromContext(ctx)
	callID := newCallID()
	start := time.Now()

	deny := func(err error) (*mcp.CallToolResult, error) {
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(),
			WorkspaceID: id.WorkspaceID, UserID: id.UserID,
			ConnectorID: snap.ConnectorID,
			PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionDeny, Outcome: store.OutcomeDenied,
			DurationMs: time.Since(start).Milliseconds(), ErrorText: err.Error(),
		})
		return nil, fmt.Errorf("denied: %w", err)
	}

	if _, err := policy.Authorize(g.store, id, snap.PublicName); err != nil {
		return deny(err)
	}
	up, ok := g.upstreams[snap.ConnectorID]
	if !ok {
		return deny(policy.ErrConnectorDisabled)
	}
	args, _ := req.Params.Arguments.(map[string]any)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := up.Call(callCtx, snap.UpstreamName, args)
	if err != nil {
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(),
			WorkspaceID: id.WorkspaceID, UserID: id.UserID,
			ConnectorID: snap.ConnectorID,
			PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionAllow, Outcome: store.OutcomeUpstreamError,
			DurationMs: time.Since(start).Milliseconds(), ErrorText: err.Error(),
		})
		return nil, fmt.Errorf("upstream %s: %w", snap.UpstreamName, err)
	}
	g.store.AppendAudit(store.AuditEvent{
		ID: callID, CallID: callID, Timestamp: start.UTC(),
		WorkspaceID: id.WorkspaceID, UserID: id.UserID,
		ConnectorID: snap.ConnectorID,
		PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
		Decision: store.DecisionAllow, Outcome: store.OutcomeOK,
		DurationMs: time.Since(start).Milliseconds(),
	})
	return res, nil
}

// OAuth endpoint paths for this deployment.
const (
	OAuthProtectedResourcePath = "/.well-known/oauth-protected-resource/mcp"
	OAuthMetadataPath          = "/.well-known/oauth-authorization-server"
	OAuthRegisterPath          = "/api/oauth/mcp/register"
	OAuthAuthorizePath         = "/api/oauth/mcp/authorize"
	OAuthTokenPath             = "/api/oauth/mcp/token"
	OAuthConsentPath           = "/api/oauth/mcp/consent"
	OAuthConnectionsPath       = "/api/oauth/mcp/connections"
	ConsentUIPath              = "/oauth/consent"
)

// OAuthConfig wires the shared authorization server to this deployment.
func OAuthConfig(publicURL, statePath, humanToken string, humanUser mcpoauth.User) mcpoauth.Config {
	human := auth.HumanSession{Token: humanToken, User: humanUser}
	cfg := mcpoauth.Config{
		PublicURL:             publicURL,
		ResourcePath:          "/mcp",
		Scopes:                []string{"mcp"},
		AccessPrefix:          "gw_mcp_",
		RefreshPrefix:         "gw_mcp_refresh_",
		ConsentUIPath:         ConsentUIPath,
		ProtectedResourcePath: OAuthProtectedResourcePath,
		RegisterPath:          OAuthRegisterPath,
		AuthorizePath:         OAuthAuthorizePath,
		TokenPath:             OAuthTokenPath,
		CurrentUser:           human.CurrentUser,
	}
	cfg.OpenStore = func() (*mcpoauth.Store, error) { return mcpoauth.OpenStore(statePath, cfg) }
	return cfg
}

// Handler returns the HTTP handler: OAuth AS endpoints, consent UI, and the
// bearer-authenticated Streamable HTTP endpoint at /mcp.
func (g *Gateway) Handler() http.Handler {
	httpSrv := server.NewStreamableHTTPServer(g.mcp,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			if id, ok := r.Context().Value(identityKey{}).(auth.Identity); ok {
				return auth.WithIdentity(ctx, id)
			}
			return ctx
		}),
	)
	mux := http.NewServeMux()
	mux.Handle("/mcp", g.requireAuth(httpSrv))
	mux.HandleFunc(OAuthProtectedResourcePath, g.oauth.HandleProtectedResource)
	mux.HandleFunc(OAuthMetadataPath, g.oauth.HandleMetadata)
	mux.HandleFunc(OAuthRegisterPath, g.oauth.HandleRegister)
	mux.HandleFunc(OAuthAuthorizePath, g.oauth.HandleAuthorize)
	mux.HandleFunc(OAuthTokenPath, g.oauth.HandleToken)
	mux.HandleFunc(OAuthConsentPath, g.oauth.HandleConsent)
	mux.HandleFunc(OAuthConnectionsPath, g.oauth.HandleConnections)
	mux.HandleFunc("DELETE "+OAuthConnectionsPath+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		g.oauth.RevokeConnection(w, r, r.PathValue("id"))
	})
	mux.HandleFunc(ConsentUIPath, serveConsentUI)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	return mux
}

type identityKey struct{}

// requireAuth validates the OAuth bearer token on every MCP request.
func (g *Gateway) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		}
		id, err := g.auth.Authenticate(r.Context(), token)
		if err != nil {
			g.oauth.Challenge(w)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}

// serveConsentUI renders the M0 approval page. It drives the JSON consent
// API; the human pastes the M0 session token once (M1 replaces this with the
// shared-IdP session and AgentWorks-styled UI).
func serveConsentUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, consentUIPage)
}

const consentUIPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>MCP Gateway: approve access</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem">
<h1>Approve MCP access</h1>
<p id="desc">Loading request…</p>
<label>Session token <input id="token" type="password" size="40" placeholder="M0 human token"></label>
<p><button id="approve">Approve</button> <button id="deny">Deny</button></p>
<p id="err" style="color:red"></p>
<script>
const q = new URLSearchParams(location.search).get("request") || "";
const saved = sessionStorage.getItem("gw_human");
if (saved) document.getElementById("token").value = saved;
async function api(method, body) {
  const token = document.getElementById("token").value.trim();
  sessionStorage.setItem("gw_human", token);
  const r = await fetch("/api/oauth/mcp/consent?request=" + encodeURIComponent(q), {
    method, headers: {"Authorization": "Bearer " + token, "Content-Type": "application/json"},
    body: body ? JSON.stringify(body) : undefined});
  if (!r.ok) throw new Error("consent API " + r.status);
  return r.json();
}
api("GET").then(d => {
  document.getElementById("desc").textContent =
    (d.client_name || "A client") + " requests scopes: " + (d.scopes || []).join(", ");
}).catch(e => document.getElementById("err").textContent = String(e));
async function decide(decision) {
  try { const d = await api("POST", {decision}); location.href = d.redirect_url; }
  catch (e) { document.getElementById("err").textContent = String(e); }
}
document.getElementById("approve").onclick = () => decide("approve");
document.getElementById("deny").onclick = () => decide("deny");
</script>
</body></html>`

func newCallID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "call_" + hex.EncodeToString(b[:])
}

func rawSchema(t mcp.Tool) []byte {
	if len(t.RawInputSchema) > 0 {
		return t.RawInputSchema
	}
	return []byte(t.InputSchema.Type)
}
