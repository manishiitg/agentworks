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
	mcp       *server.MCPServer
}

// New builds the gateway MCP server. Call SyncTools before serving.
func New(st *store.MemoryStore, a auth.Authenticator, ups map[string]*upstream.Client) *Gateway {
	g := &Gateway{store: st, auth: a, upstreams: ups}
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
				ConnectorID:   c.ID,
				WorkspaceID:   c.WorkspaceID,
				UpstreamName:  t.Name,
				PublicName:    PublicName(c.Provider, c.InstanceSlug, t.Name),
				Description:   t.Description,
				Fingerprint:   store.Fingerprint(t.Name, t.Description, rawSchema(t)),
				DiscoveredAt:  time.Now().UTC(),
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

// Handler returns the HTTP handler: bearer auth → Streamable HTTP at /mcp.
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	return mux
}

type identityKey struct{}

// requireAuth validates the bearer token on every request. M0 also accepts
// X-Gateway-Token for test clients that cannot set Authorization; M1 removes it.
func (g *Gateway) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		} else {
			token = r.Header.Get("X-Gateway-Token")
		}
		id, err := g.auth.Authenticate(r.Context(), token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}

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
