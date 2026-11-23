package executor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// HandlePerToolMCPRequest is the public entry point for per-tool MCP requests.
// It handles requests to /tools/mcp/{server}/{tool}, extracting args from the body
// and delegating to the standard MCP execution logic.
func (h *ExecutorHandlers) HandlePerToolMCPRequest(w http.ResponseWriter, r *http.Request, server, tool string) {
	h.handlePerToolMCP(w, r, server, tool)
}

// HandlePerToolCustomRequest is the public entry point for per-tool custom requests.
// It handles requests to /tools/custom/{tool}, extracting args from the body
// and delegating to the standard custom tool execution logic.
func (h *ExecutorHandlers) HandlePerToolCustomRequest(w http.ResponseWriter, r *http.Request, tool string) {
	h.handlePerToolCustom(w, r, tool)
}

func perToolSessionIDFromRequest(r *http.Request, args map[string]interface{}) string {
	bodySessionID := ""
	if sid, ok := args["session_id"].(string); ok {
		bodySessionID = strings.TrimSpace(sid)
		delete(args, "session_id")
	}

	// X-Session-ID is injected by the trusted session-scoped bridge route. It
	// must be authoritative; a CLI agent can control JSON args but cannot
	// rewrite this header from the per-session route.
	if hdr := strings.TrimSpace(r.Header.Get("X-Session-ID")); hdr != "" {
		return hdr
	}

	// Legacy direct /tools/... callers may still pass session_id in the body.
	return bodySessionID
}

// handlePerToolMCP handles requests to /tools/mcp/{server}/{tool}.
// It extracts the tool arguments from the request body and delegates to the standard MCP execution logic.
func (h *ExecutorHandlers) handlePerToolMCP(w http.ResponseWriter, r *http.Request, server, tool string) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body as tool arguments
	var args map[string]interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			h.logger.Warn("Failed to decode per-tool MCP request body", loggerv2.Error(err))
			_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	sessionID := perToolSessionIDFromRequest(r, args)

	mcpStartTime := time.Now()
	h.logger.Info("Per-tool MCP request",
		loggerv2.String("server", server),
		loggerv2.String("tool", tool),
		loggerv2.String("session_id", sessionID))
	defer func() {
		h.logger.Info(fmt.Sprintf("[TOOL_LIFECYCLE] MCP tool completed | server=%s tool=%s session=%s duration=%dms", server, tool, sessionID, time.Since(mcpStartTime).Milliseconds()))
	}()

	// Build the standard MCPExecuteRequest and reuse the full handler logic.
	// URL path segments are sanitized (hyphens→underscores via SanitizePathSegment).
	// The original server/tool names may have hyphens, so we try both forms.
	wrappedBody := MCPExecuteRequest{
		Server:    server,
		Tool:      tool,
		Args:      args,
		SessionID: sessionID,
	}

	bodyBytes, err := json.Marshal(wrappedBody)
	if err != nil {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}

	// Create a new request with the wrapped body
	newReq, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(bodyBytes))) // #nosec G704 -- In-process handler adapter only; this request is never sent by an HTTP client.
	if err != nil {
		_ = json.NewEncoder(w).Encode(MCPExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}
	newReq.Header.Set("Content-Type", "application/json")

	// URL path segments are sanitized (hyphens→underscores via SanitizePathSegment),
	// but the session registry stores server configs under the ORIGINAL name (with hyphens).
	// Try the desanitized (hyphenated) name first so we hit the session registry / lazy connect
	// path instead of falling through to mcpcache and spawning a duplicate process.
	desanitizedServer := strings.ReplaceAll(server, "_", "-")
	if desanitizedServer == server || h.mcpServerResolver != nil {
		// A host resolver prefers exact configured names before considering aliases.
		// Do not try a different provider first when both names exist.
		h.HandleMCPExecute(w, newReq)
		return
	}

	// Name has underscores that might be sanitized hyphens — try desanitized name first
	// (matches session registry keys), fall back to sanitized name if that fails.
	h.logger.Info("Trying desanitized server name first (session registry uses original names)",
		loggerv2.String("sanitized", server),
		loggerv2.String("desanitized", desanitizedServer))

	wrappedBody.Server = desanitizedServer
	desanitizedBytes, _ := json.Marshal(wrappedBody)
	desanitizedReq, desanitizedErr := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(desanitizedBytes))) // #nosec G704 -- Passed directly to HandleMCPExecute, not to a network transport.
	if desanitizedErr != nil {
		h.HandleMCPExecute(w, newReq)
		return
	}
	desanitizedReq.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.HandleMCPExecute(rec, desanitizedReq)

	// Check if the desanitized name worked
	var resp MCPExecuteResponse
	desanitizedFailed := false
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err == nil {
		if !resp.Success && (strings.Contains(resp.Error, "Failed to connect to server") ||
			strings.Contains(resp.Error, "is not available in this session's scope")) {
			desanitizedFailed = true
		}
	}

	if desanitizedFailed {
		// Desanitized name failed — fall back to original sanitized name
		h.logger.Info("Desanitized name failed, retrying with sanitized server name",
			loggerv2.String("desanitized", desanitizedServer),
			loggerv2.String("sanitized", server))

		wrappedBody.Server = server
		retryBytes, _ := json.Marshal(wrappedBody)
		retryReq, retryErr := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(retryBytes))) // #nosec G704 -- Passed directly to HandleMCPExecute, not to a network transport.
		if retryErr == nil {
			retryReq.Header.Set("Content-Type", "application/json")
			h.HandleMCPExecute(w, retryReq)
			return
		}
	}

	// Write the desanitized attempt's response
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes()) //nolint:gosec
}

// handlePerToolCustom handles requests to /tools/custom/{tool}.
func (h *ExecutorHandlers) handlePerToolCustom(w http.ResponseWriter, r *http.Request, tool string) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body as tool arguments
	var args map[string]interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			h.logger.Warn("Failed to decode per-tool custom request body", loggerv2.Error(err))
			_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	sessionID := perToolSessionIDFromRequest(r, args)

	h.logger.Info("Per-tool custom request",
		loggerv2.String("tool", tool),
		loggerv2.String("session_id", sessionID))

	// Build the standard CustomExecuteRequest and reuse the full handler logic
	wrappedBody := CustomExecuteRequest{
		Tool:      tool,
		Args:      args,
		SessionID: sessionID,
	}

	bodyBytes, err := json.Marshal(wrappedBody)
	if err != nil {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}

	newReq, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(bodyBytes))) // #nosec G704 -- In-process handler adapter only; this request is never sent by an HTTP client.
	if err != nil {
		_ = json.NewEncoder(w).Encode(CustomExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}
	newReq.Header.Set("Content-Type", "application/json")

	h.HandleCustomExecute(w, newReq)
}

// HandlePerToolVirtualRequest is the public entry point for per-tool virtual requests.
// It handles requests to /tools/virtual/{tool}, extracting args from the body
// and delegating to the standard virtual tool execution logic.
func (h *ExecutorHandlers) HandlePerToolVirtualRequest(w http.ResponseWriter, r *http.Request, tool string) {
	w.Header().Set("Content-Type", "application/json")
	if !applyExecutorCORS(w, r) {
		return
	}

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse request body as tool arguments
	var args map[string]interface{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			h.logger.Warn("Failed to decode per-tool virtual request body", loggerv2.Error(err))
			_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec
				Success: false,
				Error:   fmt.Sprintf("Invalid request body: %v", err),
			})
			return
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	sessionID := perToolSessionIDFromRequest(r, args)

	// Per-agent virtual tool scoping: if X-Virtual-Scope-ID header is present,
	// use it instead of session_id for virtual tool lookup. This prevents parent/child
	// agents sharing the same MCP session from overwriting each other's get_api_spec handler.
	virtualScopeID := sessionID
	if scopeHdr := strings.TrimSpace(r.Header.Get("X-Virtual-Scope-ID")); scopeHdr != "" {
		virtualScopeID = scopeHdr
	}

	startTime := time.Now()
	h.logger.Info("Per-tool virtual request",
		loggerv2.String("tool", tool),
		loggerv2.String("session_id", sessionID),
		loggerv2.String("virtual_scope_id", virtualScopeID))

	// Log completion when response is sent
	defer func() {
		h.logger.Info(fmt.Sprintf("[TOOL_LIFECYCLE] Virtual tool completed | tool=%s session=%s duration=%dms", tool, sessionID, time.Since(startTime).Milliseconds()))
	}()

	// Build the standard VirtualExecuteRequest and reuse the full handler logic.
	// Use virtualScopeID for virtual tool lookup (per-agent scoping).
	wrappedBody := VirtualExecuteRequest{
		Tool:      tool,
		Args:      args,
		SessionID: virtualScopeID,
	}

	bodyBytes, err := json.Marshal(wrappedBody)
	if err != nil {
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}

	newReq, err := http.NewRequestWithContext(r.Context(), "POST", r.URL.String(), strings.NewReader(string(bodyBytes))) // #nosec G704 -- In-process handler adapter only; this request is never sent by an HTTP client.
	if err != nil {
		_ = json.NewEncoder(w).Encode(VirtualExecuteResponse{ //nolint:gosec
			Success: false,
			Error:   fmt.Sprintf("Internal error: %v", err),
		})
		return
	}
	newReq.Header.Set("Content-Type", "application/json")

	h.HandleVirtualExecute(w, newReq)
}
