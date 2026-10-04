// Package mcpserver exposes the workspace's governed remote MCP endpoint.
package mcpserver

import (
	"context"
	"crypto/rand"
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
	operationsMu    sync.Mutex // guards per-connector operation locks and their reference counts
	operations      map[string]*connectorOperation
	mu              sync.RWMutex
	upstreams       map[string]*upstream.Client // connector ID -> session
	oauth           *mcpoauth.Server
	mcp             *server.MCPServer
	upstreamOptions upstream.DialOptions
	sharedOAuth     func(context.Context, string, string, string) (string, error)
	schemas         sync.Map // approved fingerprint -> compiled input schema
}

type connectorOperation struct {
	mu   sync.Mutex
	refs int
}

// lockConnector serializes changes to one connector without letting a slow
// upstream block unrelated connector operations. Reference counting keeps
// the lock map bounded after connectors are removed.
func (g *Gateway) lockConnector(id string) func() {
	g.operationsMu.Lock()
	if g.operations == nil {
		g.operations = make(map[string]*connectorOperation)
	}
	op := g.operations[id]
	if op == nil {
		op = &connectorOperation{}
		g.operations[id] = op
	}
	op.refs++
	g.operationsMu.Unlock()
	op.mu.Lock()
	return func() {
		op.mu.Unlock()
		g.operationsMu.Lock()
		op.refs--
		if op.refs == 0 {
			delete(g.operations, id)
		}
		g.operationsMu.Unlock()
	}
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

// ValidateArguments is shared by live calls and admin simulations.
func (g *Gateway) ValidateArguments(snap store.ToolSnapshot, args map[string]any) error {
	return g.validateArguments(snap, args)
}

// New builds the gateway MCP server. RestoreTools before serving saved workspaces.
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
			snap, found := st.GetTool(t.Name)
			connector, scoped := ctx.Value(connectorKey{}).(string)
			visible := policy.Visible(st, id, t.Name)
			if ctx.Value(builderKey{}) == true {
				_, err := policy.AuthorizeSetup(st, id, t.Name)
				visible = err == nil
			}
			if visible && (!scoped || (found && snap.ConnectorID == connector)) {
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

// RestoreTools registers persisted definitions without changing approval or
// grants. Calls still validate live policy and rediscover a missing upstream
// before execution. Run before serving, so temporary startup OAuth failures do
// not turn existing tools into protocol-level "tool not found" errors.
func (g *Gateway) RestoreTools(workspaceID string) {
	for _, snap := range g.store.ListTools(workspaceID) {
		tool := mcp.Tool{Description: snap.Description, Title: snap.Title,
			RawInputSchema: json.RawMessage(snap.InputSchema), RawOutputSchema: json.RawMessage(snap.OutputSchema)}
		_ = json.Unmarshal(snap.Annotations, &tool.Annotations)
		g.register(snap, tool)
	}
}

// SyncTools discovers every active connector's tools, snapshots them, and
// registers their public names. All discovered tools are registered; grants
// control visibility and execution.
func (g *Gateway) SyncTools(ctx context.Context, workspaceID string) error {
	for _, c := range g.store.ListConnectors(workspaceID) {
		if c.Status != store.StatusActive {
			continue
		}
		if err := g.syncConnector(ctx, c, false); err != nil {
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

// The host owns authorization, encrypted storage and refresh. The gateway
// binds the shared identity to this connector's exact upstream URL.
func (g *Gateway) SetSharedOAuth(resolve func(context.Context, string, string, string) (string, error)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sharedOAuth = resolve
}

func (g *Gateway) HasSharedOAuth() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sharedOAuth != nil
}

func (g *Gateway) connectorOptions(c store.Connector) (upstream.DialOptions, error) {
	opts := g.upstreamOptions
	opts.BearerToken = g.store.ConnectorBearer(c.ID)
	if c.OAuthServer != "" {
		g.mu.RLock()
		resolve := g.sharedOAuth
		g.mu.RUnlock()
		if resolve == nil {
			return opts, fmt.Errorf("shared OAuth service is not configured")
		}
		opts.BearerToken = ""
		opts.AccessToken = func(ctx context.Context) (string, error) {
			return resolve(ctx, c.OAuthServer, c.UpstreamURL, c.OAuthCredentialID)
		}
	}
	return opts, nil
}

func (g *Gateway) syncConnector(ctx context.Context, c store.Connector, approveInitial bool) error {
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
		// Connecting a new MCP explicitly trusts this initial discovery. Later
		// background/admin syncs must still quarantine new or changed definitions.
		status := ""
		if approveInitial {
			status = store.StatusActive
		}
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
			Status:       status,
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
// serving. A new connector's initial tool definitions are approved by this
// explicit configuration action. Approval grants no user/group access. An
// existing connector with snapshots keeps the normal change-review behavior.
// The connector row must already exist in the store.
func (g *Gateway) AddConnector(ctx context.Context, c store.Connector) error {
	defer g.lockConnector(c.ID)()
	if _, ok := g.upstreamFor(c.ID); ok {
		return fmt.Errorf("connector %s already connected", c.ID)
	}
	opts, err := g.connectorOptions(c)
	if err != nil {
		return err
	}
	up, err := upstream.DialWithOptions(ctx, c.UpstreamURL, opts)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.upstreams[c.ID] = up
	g.mu.Unlock()
	approveInitial := len(g.store.ListToolsForConnector(c.ID)) == 0
	if err := g.syncConnector(ctx, c, approveInitial); err != nil {
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
	defer g.lockConnector(c.ID)()
	return g.resyncConnector(ctx, c)
}

// ensureConnected retries initialization/discovery, never an actual tool call.
// The same connector lock serializes this with startup, sync and reauthorization.
func (g *Gateway) ensureConnected(ctx context.Context, workspaceID, connectorID string) (*upstream.Client, error) {
	defer g.lockConnector(connectorID)()
	c, ok := g.store.GetConnector(connectorID)
	if !ok || c.WorkspaceID != workspaceID || c.Status != store.StatusActive {
		return nil, policy.ErrConnectorDisabled
	}
	if up, ok := g.upstreamFor(connectorID); ok {
		return up, nil
	}
	if err := g.resyncConnector(ctx, c); err != nil {
		return nil, err
	}
	up, _ := g.upstreamFor(connectorID)
	return up, nil
}

// Caller holds the connector operation lock.
func (g *Gateway) resyncConnector(ctx context.Context, c store.Connector) error {
	current, ok := g.store.GetConnector(c.ID)
	if !ok || (current.Status != store.StatusActive && current.Status != store.StatusAuthRequired) || current.WorkspaceID != c.WorkspaceID {
		return fmt.Errorf("connector %s is no longer active", c.ID)
	}
	c = current
	// OAuth reauthorization may change accounts. Never retain the previous
	// account's MCP session when discovering tools for the replacement.
	if c.OAuthCredentialID != "" {
		g.mu.Lock()
		old := g.upstreams[c.ID]
		delete(g.upstreams, c.ID)
		g.mu.Unlock()
		if old != nil {
			old.Close()
		}
	}
	dialed := false
	if _, ok := g.upstreamFor(c.ID); !ok {
		opts, err := g.connectorOptions(c)
		if err != nil {
			return err
		}
		up, err := upstream.DialWithOptions(ctx, c.UpstreamURL, opts)
		if err != nil {
			return err
		}
		g.mu.Lock()
		g.upstreams[c.ID] = up
		g.mu.Unlock()
		dialed = true
	}
	initial := c.Status == store.StatusAuthRequired && len(g.store.ListToolsForConnector(c.ID)) == 0
	if err := g.syncConnector(ctx, c, initial); err != nil {
		if dialed {
			g.mu.Lock()
			up := g.upstreams[c.ID]
			delete(g.upstreams, c.ID)
			g.mu.Unlock()
			up.Close()
		}
		return err
	}
	c.Status = store.StatusActive
	g.store.AddConnector(c)
	return nil
}

// SuspendOAuth preserves grants while preventing calls and dropping the session.
func (g *Gateway) SuspendOAuth(c store.Connector) {
	defer g.lockConnector(c.ID)()
	current, ok := g.store.GetConnector(c.ID)
	if !ok || current.OAuthCredentialID == "" {
		return
	}
	current.Status = store.StatusAuthRequired
	g.store.AddConnector(current)
	g.mu.Lock()
	up := g.upstreams[c.ID]
	delete(g.upstreams, c.ID)
	g.mu.Unlock()
	if up != nil {
		up.Close()
	}
}

// ReplaceConnectorCredentials atomically swaps a tested new upstream session.
// The previous session remains in service if authentication or discovery fails.
func (g *Gateway) ReplaceConnectorCredentials(ctx context.Context, c store.Connector, bearer string) error {
	defer g.lockConnector(c.ID)()
	current, ok := g.store.GetConnector(c.ID)
	if !ok || current.WorkspaceID != c.WorkspaceID || current.Status != store.StatusActive {
		return fmt.Errorf("connector is not active")
	}
	if current.OAuthServer != "" {
		return fmt.Errorf("OAuth credentials are managed by the shared product sign-in flow")
	}
	opts := g.upstreamOptions
	opts.BearerToken = bearer
	newUp, err := upstream.DialWithOptions(ctx, current.UpstreamURL, opts)
	if err != nil {
		return err
	}
	if _, err := newUp.Discover(ctx); err != nil {
		newUp.Close()
		return err
	}
	g.mu.Lock()
	old := g.upstreams[c.ID]
	g.upstreams[c.ID] = newUp
	g.mu.Unlock()
	if err := g.syncConnector(ctx, current, false); err != nil {
		g.mu.Lock()
		g.upstreams[c.ID] = old
		g.mu.Unlock()
		newUp.Close()
		return err
	}
	g.store.SetConnectorBearer(c.ID, bearer)
	if old != nil {
		old.Close()
	}
	return nil
}

// RemoveConnector disconnects an instance and drops its snapshots. The MCP
// server keeps the stale handler registrations, but they deny (unknown tool)
// and hide (list filter), so removal is effective immediately.
func (g *Gateway) RemoveConnector(id string) {
	defer g.lockConnector(id)()
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
func (g *Gateway) handleCall(ctx context.Context, snap store.ToolSnapshot, req mcp.CallToolRequest) (result *mcp.CallToolResult, callErr error) {
	var auditErr error
	appendAudit := func(e store.AuditEvent) {
		if !g.store.AuditInfo().Enabled {
			return
		}
		input := req.Params.Arguments
		if input == nil {
			input = map[string]any{}
		}
		e.Input, e.InputTruncated = store.CaptureAuditPayload(input)
		if result != nil {
			// Record tool content, structured output and the error indicator. Protocol
			// metadata and HTTP headers are not tool output and are not captured.
			output := map[string]any{"content": result.Content, "isError": result.IsError}
			if result.StructuredContent != nil {
				output["structuredContent"] = result.StructuredContent
			}
			e.Output, e.OutputTruncated = store.CaptureAuditPayload(output)
		}
		if err := g.store.AppendAudit(e); err != nil {
			auditErr = err
		}
	}
	defer func() {
		if auditErr != nil {
			result = nil
			callErr = fmt.Errorf("audit persistence failed; the tool may already have executed; do not retry automatically")
		}
	}()
	id, _ := auth.FromContext(ctx)
	callID := newCallID()
	start := time.Now()
	groups := g.store.GroupsOf(id.UserID)
	if id.ViaGroup != "" {
		groups = []string{id.ViaGroup}
	}
	deny := func(err error) (*mcp.CallToolResult, error) {
		appendAudit(store.AuditEvent{
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

	if connector, scoped := ctx.Value(connectorKey{}).(string); scoped && snap.ConnectorID != connector {
		return deny(policy.ErrNoGrant)
	}
	authorize := policy.Authorize
	builder := ctx.Value(builderKey{}) == true
	if builder {
		authorize = policy.AuthorizeSetup
	}
	if _, err := authorize(g.store, id, snap.PublicName); err != nil {
		return deny(err)
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
	if current.Fingerprint != snap.Fingerprint {
		return deny(policy.ErrToolNotActive)
	}
	if !builder {
		if err := policy.AuthorizeArguments(g.store, id, current, args); err != nil {
			return deny(err)
		}
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	up, err := g.ensureConnected(callCtx, id.WorkspaceID, snap.ConnectorID)
	if err != nil {
		appendAudit(store.AuditEvent{
			ID: callID, CallID: callID, Timestamp: start.UTC(),
			WorkspaceID: id.WorkspaceID, UserID: id.UserID, ClientID: id.ClientID,
			GroupIDs: groups, ConnectorID: snap.ConnectorID,
			PublicName: snap.PublicName, UpstreamName: snap.UpstreamName,
			Decision: store.DecisionAllow, Outcome: store.OutcomeUpstreamError,
			DurationMs: time.Since(start).Milliseconds(), ErrorText: "upstream connection unavailable",
		})
		return nil, fmt.Errorf("upstream connection unavailable; reconnect the MCP server (call %s)", callID)
	}
	// Discovery may quarantine a changed definition, or access may be revoked
	// while initialization waits on OAuth. Recheck before any upstream invocation.
	if _, err := authorize(g.store, id, snap.PublicName); err != nil {
		return deny(err)
	}
	current, ok = g.store.GetTool(snap.PublicName)
	if !ok || current.Fingerprint != snap.Fingerprint {
		return deny(policy.ErrToolNotActive)
	}
	if !builder {
		if err := policy.AuthorizeArguments(g.store, id, current, args); err != nil {
			return deny(err)
		}
	}
	res, err := up.Call(callCtx, snap.UpstreamName, args)
	if err != nil {
		appendAudit(store.AuditEvent{
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
	result = res // Snapshot only the returned MCP result, never transport headers or credentials.
	outcome, auditError := store.OutcomeOK, ""
	if res != nil && res.IsError {
		outcome, auditError = store.OutcomeUpstreamError, "upstream tool reported error"
	}
	appendAudit(store.AuditEvent{
		ID: callID, CallID: callID, Timestamp: start.UTC(),
		WorkspaceID: id.WorkspaceID, UserID: id.UserID, ClientID: id.ClientID,
		GroupIDs:    groups,
		ConnectorID: snap.ConnectorID,
		PublicName:  snap.PublicName, UpstreamName: snap.UpstreamName,
		Decision: store.DecisionAllow, Outcome: outcome, ErrorText: auditError,
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

// ProductHandler is mounted only behind the host service credential. Identity
// and connector scope come from the authenticated host, never browser headers.
// Use a separate stateless transport so a retained MCP session cannot switch users.
type connectorKey struct{}
type builderKey struct{}

// BuilderProductHandler is service-only. Its callback must validate the host's
// live administrator assertion. No normal or external transport sets this key.
func (g *Gateway) BuilderProductHandler(identity func(*http.Request) (auth.Identity, string, bool)) http.Handler {
	return g.productHandler("/api/admin/runtime/builder/mcp", identity)
}

func (g *Gateway) ProductHandler(identity func(*http.Request) (auth.Identity, string, bool)) http.Handler {
	return g.productHandler("/api/admin/runtime/mcp", identity)
}

// ExternalProductHandler exposes all permitted connectors for a verified platform OAuth user.
func (g *Gateway) ExternalProductHandler(identity func(*http.Request) (auth.Identity, string, bool)) http.Handler {
	return g.productHandler("/api/admin/runtime/external-mcp", identity)
}

func (g *Gateway) productHandler(path string, identity func(*http.Request) (auth.Identity, string, bool)) http.Handler {
	transport := server.NewStreamableHTTPServer(g.mcp, server.WithEndpointPath(path), server.WithStateLess(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context { return r.Context() }))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, connector, ok := identity(r)
		if !ok {
			http.Error(w, "service authentication required", http.StatusUnauthorized)
			return
		}
		ctx := auth.WithIdentity(r.Context(), id)
		if path == "/api/admin/runtime/builder/mcp" {
			ctx = context.WithValue(ctx, builderKey{}, true)
		}
		if connector != "" {
			ctx = context.WithValue(ctx, connectorKey{}, connector)
		}
		transport.ServeHTTP(w, r.WithContext(ctx))
	})
}
