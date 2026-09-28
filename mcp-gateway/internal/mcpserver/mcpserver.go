// Package mcpserver exposes the workspace's governed remote MCP endpoint.
package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/mcpoauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/auth"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/pii"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/policy"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/upstream"
)

// NamespacePrefix derives the public-name prefix shared by every tool of one
// connector instance. Single instance: "<provider>". Multi-instance:
// "<provider>_<slug>". Connectors sharing a prefix would collide in the
// name-keyed registry, so the admin API rejects the second one.
func NamespacePrefix(provider, slug string) string {
	return store.ConnectorNamespacePrefix(provider, slug)
}

// PublicName derives the stable gateway-visible tool name. Single instance:
// "<provider>__<tool>". Multi-instance: "<provider>_<slug>__<tool>".
func PublicName(provider, slug, upstreamName string) string {
	return NamespacePrefix(provider, slug) + "__" + upstreamName
}

// Gateway is one workspace's governed MCP endpoint.
type Gateway struct {
	store           *store.MemoryStore
	auth            auth.Authenticator
	operations      sync.Mutex // serialize add, resync, and remove for each gateway
	mu              sync.RWMutex
	upstreams       map[string]*upstream.Client // connector ID -> session
	oauth           *mcpoauth.Server
	mcp             *server.MCPServer
	upstreamOptions upstream.DialOptions
	schemas         sync.Map // approved fingerprint -> compiled input schema
}

type denyRemoteSchemaLoader struct{}

func (denyRemoteSchemaLoader) Load(string) (any, error) {
	return nil, fmt.Errorf("external schema references are unsupported")
}

func (g *Gateway) validateArguments(snap store.ToolSnapshot, args map[string]any) error {
	if len(snap.InputSchema) == 0 || len(snap.InputSchema) > 256*1024 {
		return fmt.Errorf("tool has no input schema")
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > 256*1024 {
		return fmt.Errorf("tool arguments exceed size limit")
	}
	if cached, ok := g.schemas.Load(snap.Fingerprint); ok {
		return cached.(*jsonschema.Schema).Validate(args)
	}
	var document any
	if err := json.Unmarshal(snap.InputSchema, &document); err != nil {
		return fmt.Errorf("invalid tool schema")
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(denyRemoteSchemaLoader{})
	const location = "https://gateway.invalid/input-schema"
	if err := compiler.AddResource(location, document); err != nil {
		return fmt.Errorf("invalid tool schema")
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return fmt.Errorf("invalid tool schema")
	}
	actual, _ := g.schemas.LoadOrStore(snap.Fingerprint, compiled)
	return actual.(*jsonschema.Schema).Validate(args)
}

// New builds the gateway MCP server. Call SyncTools before serving.
func New(st *store.MemoryStore, a auth.Authenticator, ups map[string]*upstream.Client, oauthSrv *mcpoauth.Server, options ...upstream.DialOptions) *Gateway {
	g := &Gateway{store: st, auth: a, upstreams: ups, oauth: oauthSrv}
	if len(options) > 0 {
		g.upstreamOptions = options[0]
	}
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
		if err := g.syncConnector(ctx, c); err != nil {
			return err
		}
	}
	log.Printf("gateway: synced workspace %s", workspaceID)
	return nil
}

func (g *Gateway) upstreamFor(connectorID string) (*upstream.Client, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	up, ok := g.upstreams[connectorID]
	return up, ok
}

func (g *Gateway) ValidateUpstreamURL(raw string) error {
	_, err := upstream.ValidateURL(raw, g.upstreamOptions)
	return err
}

func (g *Gateway) syncConnector(ctx context.Context, c store.Connector) error {
	up, ok := g.upstreamFor(c.ID)
	if !ok {
		return fmt.Errorf("connector %s (%s): no upstream session", c.ID, c.Label)
	}
	tools, err := up.Discover(ctx)
	if err != nil {
		return fmt.Errorf("connector %s (%s): %w", c.ID, c.Label, err)
	}
	for _, t := range tools {
		log.Printf("gateway: discovered %s -> %s", t.Name, PublicName(c.Provider, c.InstanceSlug, t.Name))
	}
	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		schema := schemaBytes(t)
		outputSchema := outputSchemaBytes(t)
		annotations, _ := json.Marshal(t.Annotations)
		snap := g.store.UpsertToolSnapshot(store.ToolSnapshot{
			ConnectorID:  c.ID,
			WorkspaceID:  c.WorkspaceID,
			UpstreamName: t.Name,
			PublicName:   PublicName(c.Provider, c.InstanceSlug, t.Name),
			Description:  t.Description,
			Title:        t.Title,
			InputSchema:  schema,
			OutputSchema: outputSchema,
			Annotations:  annotations,
			Fingerprint:  store.Fingerprint(t.Name, t.Description, schema, []byte(t.Title), outputSchema, annotations),
			DiscoveredAt: time.Now().UTC(),
		})
		seen[snap.PublicName] = true
		g.register(snap, t)
	}
	// Reconcile against the successful discovery: tools the upstream no
	// longer lists stop being advertised and authorized.
	g.store.DisableMissingTools(c.ID, seen)
	return nil
}

// AddConnector dials a new upstream instance and syncs its tools while
// serving. The connector row must already exist in the store.
func (g *Gateway) AddConnector(ctx context.Context, c store.Connector) error {
	g.operations.Lock()
	defer g.operations.Unlock()
	if _, ok := g.upstreamFor(c.ID); ok {
		return fmt.Errorf("connector %s already connected", c.ID)
	}
	up, err := upstream.DialWithOptions(ctx, c.UpstreamURL, g.upstreamOptions)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.upstreams[c.ID] = up
	g.mu.Unlock()
	if err := g.syncConnector(ctx, c); err != nil {
		g.mu.Lock()
		delete(g.upstreams, c.ID)
		g.mu.Unlock()
		up.Close()
		return err
	}
	return nil
}

// Resync rediscovers one connector's tools, dialing first if needed.
func (g *Gateway) Resync(ctx context.Context, c store.Connector) error {
	g.operations.Lock()
	defer g.operations.Unlock()
	current, ok := g.store.GetConnector(c.ID)
	if !ok || current.Status != store.StatusActive || current.WorkspaceID != c.WorkspaceID {
		return fmt.Errorf("connector %s is no longer active", c.ID)
	}
	c = current
	if _, ok := g.upstreamFor(c.ID); !ok {
		up, err := upstream.DialWithOptions(ctx, c.UpstreamURL, g.upstreamOptions)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.upstreams[c.ID] = up
		g.mu.Unlock()
	}
	return g.syncConnector(ctx, c)
}

// RemoveConnector disconnects an instance and drops its snapshots. The MCP
// server keeps the stale handler registrations, but they deny (unknown tool)
// and hide (list filter), so removal is effective immediately.
func (g *Gateway) RemoveConnector(id string) {
	g.operations.Lock()
	defer g.operations.Unlock()
	g.mu.Lock()
	up, ok := g.upstreams[id]
	delete(g.upstreams, id)
	g.mu.Unlock()
	if ok {
		up.Close()
	}
	g.store.DeleteConnector(id)
}

func (g *Gateway) register(snap store.ToolSnapshot, upstreamTool mcp.Tool) {
	public := mcp.Tool{
		Name:            snap.PublicName,
		Description:     upstreamTool.Description,
		InputSchema:     upstreamTool.InputSchema,
		RawInputSchema:  upstreamTool.RawInputSchema,
		OutputSchema:    upstreamTool.OutputSchema,
		RawOutputSchema: upstreamTool.RawOutputSchema,
		Annotations:     upstreamTool.Annotations,
		Title:           upstreamTool.Title,
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
	groups := g.store.GroupsOf(id.UserID)
	if id.ViaGroup != "" {
		groups = []string{id.ViaGroup}
	}
	queueReview := func(direction string, payload any, found pii.Decision) (approved, queued bool) {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false, false
		}
		hash := sha256.Sum256(append([]byte(id.WorkspaceID+"\x00"+id.UserID+"\x00"+snap.PublicName+"\x00"+direction+"\x00"), encoded...))
		key := hex.EncodeToString(hash[:])
		if g.store.ConsumePIIReview(id.WorkspaceID, id.UserID, snap.PublicName, direction, key) {
			return true, false
		}
		queued = g.store.AddPIIReview(store.PIIReview{
			ID: callID, WorkspaceID: id.WorkspaceID, UserID: id.UserID,
			ConnectorID: snap.ConnectorID, PublicName: snap.PublicName,
			Direction: direction, PayloadHash: key, DataTypes: found.DataTypes,
			Status: "pending", CreatedAt: start.UTC(),
		})
		return false, queued
	}

	deny := func(err error) (*mcp.CallToolResult, error) {
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(),
			WorkspaceID: id.WorkspaceID, UserID: id.UserID, ClientID: id.ClientID,
			GroupIDs:    groups,
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
	up, ok := g.upstreamFor(snap.ConnectorID)
	if !ok {
		return deny(policy.ErrConnectorDisabled)
	}
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok && req.Params.Arguments != nil {
		return deny(fmt.Errorf("arguments must be an object"))
	}
	if args == nil {
		args = map[string]any{}
	}
	current, ok := g.store.GetTool(snap.PublicName)
	if !ok || current.WorkspaceID != id.WorkspaceID || current.Status != store.StatusActive {
		return deny(policy.ErrToolNotActive)
	}
	if err := g.validateArguments(current, args); err != nil {
		return deny(fmt.Errorf("arguments do not match approved tool schema"))
	}
	rules := g.store.ListPIIRules(id.WorkspaceID)
	scope := pii.Scope{WorkspaceID: id.WorkspaceID, GroupIDs: groups, ConnectorID: snap.ConnectorID, PublicName: snap.PublicName, Direction: pii.Input}
	inspectedArgs, inputDecision, scanErr := pii.ScanJSON(args, scope, rules)
	inputReviewApproved, inputReviewQueued := false, false
	if inputDecision.Action == pii.Review {
		inputReviewApproved, inputReviewQueued = queueReview(pii.Input, args, inputDecision)
	}
	if scanErr != nil || inputDecision.Action == pii.Block || (inputDecision.Action == pii.Review && !inputReviewApproved) {
		action := inputDecision.Action
		if scanErr != nil {
			action = pii.Block
		}
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(), WorkspaceID: id.WorkspaceID,
			UserID: id.UserID, ClientID: id.ClientID, GroupIDs: groups, ConnectorID: snap.ConnectorID,
			PublicName: snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionDeny, Outcome: store.OutcomeDenied,
			DurationMs: time.Since(start).Milliseconds(), PIIAction: action, PIIDataTypes: inputDecision.DataTypes,
		})
		if action == pii.Review {
			if !inputReviewQueued {
				return nil, fmt.Errorf("input blocked: admin review queue is full (call %s)", callID)
			}
			return nil, fmt.Errorf("input requires admin review; retry after approval (call %s)", callID)
		}
		return nil, fmt.Errorf("input blocked by PII policy (call %s)", callID)
	}
	args = inspectedArgs.(map[string]any)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := up.Call(callCtx, snap.UpstreamName, args)
	if err != nil {
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(),
			WorkspaceID: id.WorkspaceID, UserID: id.UserID, ClientID: id.ClientID,
			GroupIDs:    groups,
			ConnectorID: snap.ConnectorID,
			PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionAllow, Outcome: store.OutcomeUpstreamError,
			DurationMs: time.Since(start).Milliseconds(), ErrorText: "upstream call failed",
		})
		return nil, fmt.Errorf("upstream call failed (call %s)", callID)
	}
	scope.Direction = pii.Output
	outputDecision, scanErr := inspectResult(res, scope, rules)
	if scanErr != nil || outputDecision.Action == pii.Block || outputDecision.Action == pii.Review {
		action := outputDecision.Action
		if scanErr != nil || action == pii.Review {
			action = pii.Block
		}
		g.store.AppendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(), WorkspaceID: id.WorkspaceID,
			UserID: id.UserID, ClientID: id.ClientID, GroupIDs: groups, ConnectorID: snap.ConnectorID,
			PublicName: snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionDeny, Outcome: store.OutcomeDenied,
			DurationMs: time.Since(start).Milliseconds(), PIIAction: action, PIIDataTypes: outputDecision.DataTypes,
		})
		return nil, fmt.Errorf("output blocked by PII policy (call %s)", callID)
	}
	g.store.AppendAudit(store.AuditEvent{
		ID: callID, CallID: callID, Timestamp: start.UTC(),
		WorkspaceID: id.WorkspaceID, UserID: id.UserID, ClientID: id.ClientID,
		GroupIDs:    groups,
		ConnectorID: snap.ConnectorID,
		PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
		Decision: store.DecisionAllow, Outcome: store.OutcomeOK,
		DurationMs:   time.Since(start).Milliseconds(),
		PIIAction:    combinedPIIAction(inputDecision.Action, outputDecision.Action),
		PIIDataTypes: append(inputDecision.DataTypes, outputDecision.DataTypes...),
	})
	return res, nil
}

func combinedPIIAction(first, second string) string {
	if first == pii.Review || second == pii.Review {
		return pii.Review
	}
	if first == pii.Mask || second == pii.Mask {
		return pii.Mask
	}
	return pii.Allow
}

// inspectResult modifies only inspectable text/JSON. Opaque content is denied
// because regex inspection cannot establish what it contains.
func inspectResult(res *mcp.CallToolResult, scope pii.Scope, rules []pii.Rule) (pii.Decision, error) {
	encoded, err := json.Marshal(res)
	if err != nil || len(encoded) > pii.MaxPayloadBytes {
		return pii.Decision{Action: pii.Block}, pii.ErrPayloadTooLarge
	}
	decision := pii.Decision{Action: pii.Allow}
	merge := func(found pii.Decision) {
		if found.Action == pii.Block || (found.Action == pii.Review && decision.Action != pii.Block) {
			decision.Action = found.Action
		} else if found.Action == pii.Mask && decision.Action == pii.Allow {
			decision.Action = pii.Mask
		}
		decision.MatchCount += found.MatchCount
		decision.DataTypes = append(decision.DataTypes, found.DataTypes...)
	}
	for index, item := range res.Content {
		var textContent mcp.TextContent
		switch content := item.(type) {
		case mcp.TextContent:
			textContent = content
		case *mcp.TextContent:
			textContent = *content
		default:
			return pii.Decision{Action: pii.Block}, fmt.Errorf("opaque MCP result content cannot be inspected")
		}
		masked, found, err := pii.ScanText(textContent.Text, scope, rules)
		if err != nil {
			return pii.Decision{Action: pii.Block}, err
		}
		merge(found)
		textContent.Text = masked
		res.Content[index] = textContent
	}
	if res.StructuredContent != nil {
		masked, found, err := pii.ScanJSON(res.StructuredContent, scope, rules)
		if err != nil {
			return pii.Decision{Action: pii.Block}, err
		}
		merge(found)
		res.StructuredContent = masked
		res.RawStructuredContent = nil
	}
	return decision, nil
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

// Handler returns the HTTP mux: OAuth AS endpoints, consent UI, and the
// bearer-authenticated Streamable HTTP endpoint at /mcp. Callers may mount
// more routes (admin API/UI) on the returned mux.
func (g *Gateway) Handler() *http.ServeMux {
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
// API; the local human enters the admin secret for each consent. Individual
// sign-in must replace this before the gateway is exposed publicly.
func serveConsentUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	fmt.Fprint(w, consentUIPage)
}

const consentUIPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>MCP Gateway: approve access</title></head>
<body style="font-family:system-ui;max-width:40rem;margin:3rem auto;padding:0 1rem">
<h1>Approve MCP access</h1>
<p id="desc">Loading request…</p>
<label>Local admin token <input id="token" type="password" size="40" autocomplete="off"></label>
<p><button id="approve">Approve</button> <button id="deny">Deny</button></p>
<p id="err" style="color:red"></p>
<script>
const q = new URLSearchParams(location.search).get("request") || "";
async function api(method, body) {
  const token = document.getElementById("token").value.trim();
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

// schemaBytes returns the normalized input schema for storage and
// fingerprinting: the raw upstream bytes when present, else the parsed
// struct serialized. Normalization (unmarshal+remarshal) keeps semantically
// identical schemas byte-identical across resyncs, so property or
// required-field changes are what flips the fingerprint.
func schemaBytes(t mcp.Tool) []byte {
	var raw []byte
	if len(t.RawInputSchema) > 0 {
		raw = t.RawInputSchema
	} else if t.InputSchema.Type == "" {
		return []byte(`{}`)
	} else {
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return []byte(t.InputSchema.Type)
		}
		raw = b
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	norm, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return norm
}

func outputSchemaBytes(t mcp.Tool) []byte {
	if len(t.RawOutputSchema) == 0 && t.OutputSchema.Type == "" {
		return nil
	}
	raw := t.RawOutputSchema
	if len(raw) == 0 {
		encoded, err := json.Marshal(t.OutputSchema)
		if err != nil {
			return nil
		}
		raw = encoded
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}
