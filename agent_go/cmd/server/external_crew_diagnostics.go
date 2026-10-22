package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func crewDiagnosticPage[T any](items []T, limit, offset int) map[string]any {
	start := min(max(offset, 0), len(items))
	end := min(start+min(max(limit, 1), 100), len(items))
	return map[string]any{"items": items[start:end], "total": len(items), "has_more": end < len(items), "next_offset": end}
}

func externalCrewDiagnosticsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("crew_diagnostics", "Read this Crew's browser status and scoped browser sessions (action=browser), or its attached MCP connection status (action=connections). Pass an exact connection name for cached tool metadata; include_tools adds schemas for the Crew owner only. limit/offset page browser sessions, connections, or the named connection's tools. Metadata is cached, not a live reachability check; refresh it with crew_mcp action=discover. Chrome extension status follows account ownership. No browser starts, commands, credentials or server-wide logs. Requires crews:read and current Crew access.", false, false, map[string]any{
		"crew_id":       externalString("Crew ID from crew action=list."),
		"action":        map[string]any{"type": "string", "enum": []any{"browser", "connections"}},
		"name":          externalString("Exact attached connection name; connections only. Omit to list connection summaries."),
		"include_tools": map[string]any{"type": "boolean", "description": "Crew owner only: include cached parameter schemas for a named connection. Default false."},
		"limit":         externalInteger(1, 100), "offset": externalInteger(0, 100000),
	}, "crew_id", "action")
}

func (api *StreamingAPI) externalCrewDiagnosticsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	id, action, name := externalArg(args, "crew_id"), externalArg(args, "action"), externalArg(args, "name")
	if claims.AccessToken != nil && (!claims.AccessToken.Allows("crews:read") || !claims.AccessToken.AllowsCrew(id)) {
		externalError(w, 403, "insufficient_scope", "This connection does not allow reading this Crew.")
		return
	}
	crew, _, _, ok := api.externalCrewResolve(r.Context(), claims, id)
	if !ok {
		externalError(w, 404, "not_found", "Crew not found or not allowed.")
		return
	}
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	limit, offset := externalInt(args, "limit", 50), externalInt(args, "offset", 0)
	if action == "browser" {
		if name != "" || args["include_tools"] != nil {
			externalError(w, 400, "invalid_arguments", "name and include_tools apply only to connections.")
			return
		}
		if profile.Runtime.Capabilities.Browser == agentprofiles.CapabilityDisabled || !containsExactString(profile.ToolPolicy.Enabled, "agent_browser") {
			externalError(w, 404, "tool_unavailable", "This Crew profile does not enable browser tools.")
			return
		}
		settings, err := readWorkspaceBrowserSettings(r.Context(), root)
		if err != nil {
			externalError(w, 502, "browser_settings_unavailable", "Cannot read this Crew's browser settings.")
			return
		}
		// Query the shared capability handler without adopting the owner's chat
		// or a retained caller session. Status never launches a browser.
		ctx := context.WithValue(r.Context(), common.ChatSessionIDKey, "")
		ctx = context.WithValue(ctx, common.WorkflowSessionIDKey, "")
		probe := browser.NewExecutor(browser.NewClient(getWorkspaceAPIURL()), browser.WithBrowserRuntimeConfig(browser.NewBrowserRuntimeConfig(settings.Mode, []int{settings.Port})))
		raw, err := probe.HandleAgentBrowser(ctx, map[string]any{"command": "status"})
		if err != nil {
			externalError(w, 502, "browser_status_unavailable", "Cannot inspect this Crew's browser status.")
			return
		}
		var status map[string]any
		if err := json.Unmarshal([]byte(raw), &status); err != nil {
			externalFailure(w, err)
			return
		}
		request := r.Clone(r.Context())
		request.URL = &url.URL{RawQuery: url.Values{"workspace_path": {root}}.Encode()}
		// The app intentionally shares Crew browsers and Playwright recordings.
		// Export a small inventory, not owner chat IDs, recorded commands or
		// recording errors (which may contain host paths).
		sessions := []map[string]string{}
		shared := browserSessionForWorkspace(claims.UserID, root)
		for _, item := range api.liveBrowserSessions(request) {
			row := map[string]string{}
			for _, key := range []string{"browser_session", "label", "kind", "state", "read_only", "recording_state", "age", "idle"} {
				if value, found := item[key]; found {
					row[key] = value
				}
			}
			row["scope"] = "caller"
			if item["browser_session"] == shared || item["kind"] == "playwright" {
				row["scope"] = "shared_crew"
			}
			if !crew.OwnedByCaller {
				row["read_only"] = "true"
			}
			sessions = append(sessions, row)
		}
		// Do not forward raw status: its CDP endpoints, ports and probe errors
		// describe the host. Keep only the declared capability summary.
		capabilities := map[string]any{}
		for _, key := range []string{"configured_mode", "effective_mode", "cdp_supported", "headless_available"} {
			if value, found := status[key]; found {
				capabilities[key] = value
			}
		}
		extension := map[string]any{"available_to_caller": crew.OwnedByCaller}
		if crew.OwnedByCaller {
			connection := browserrelay.Default.Status(claims.UserID, browserSessionForWorkspace(claims.UserID, root))
			extension["selected"], extension["connected"], extension["tabs"] = connection.Selected, connection.Connected, connection.Tabs
			extension["extension_version"], extension["latest_extension_version"] = connection.ExtensionVersion, connection.LatestVersion
		}
		externalJSON(w, map[string]any{"crew_id": id, "action": action, "settings": map[string]any{"mode": settings.Mode}, "capabilities": capabilities, "extension": extension, "sessions": crewDiagnosticPage(sessions, limit, offset)})
		return
	}
	if action != "connections" {
		externalError(w, 400, "invalid_arguments", "Unknown diagnostics action.")
		return
	}
	if !containsExactString(profile.ToolPolicy.Enabled, "list_mcp_servers") {
		externalError(w, 404, "tool_unavailable", "This Crew profile does not enable MCP tools.")
		return
	}
	if include, _ := args["include_tools"].(bool); include && !crew.OwnedByCaller {
		externalError(w, 403, "forbidden", "Only the Crew owner can read full connection parameter schemas. Omit include_tools for tool names and descriptions.")
		return
	}
	inventory, err := vaultServersFor(r.Context(), api.projectSecretsOwner(claims.UserID, root))
	if err != nil {
		externalError(w, 502, "connections_unavailable", "Vault is unavailable.")
		return
	}
	selected, _ := projectSelectedServers(r.Context(), root)
	hasPicks := false
	none := false
	for _, pick := range selected {
		if pick == "NO_SERVERS" {
			none = true
		}
		if strings.HasPrefix(pick, "vault_") {
			hasPicks = true
		}
	}
	rows := []map[string]any{}
	for _, server := range inventory {
		exact := vaultServerName(server.ID)
		if none || hasPicks && !containsExactString(selected, exact) || name != "" && name != exact {
			continue
		}
		row := map[string]any{"name": exact, "label": server.Label, "catalog": server.Provider, "status": "connected", "active": true, "connected": true, "cached_tool_count": len(server.Tools)}
		if name != "" {
			tools := []map[string]any{}
			for _, tool := range server.Tools {
				item := map[string]any{"name": tool.Name, "description": tool.Description}
				if include, _ := args["include_tools"].(bool); include {
					item["parameters"] = tool.InputSchema
				}
				tools = append(tools, item)
			}
			row["tools"] = crewDiagnosticPage(tools, limit, offset)
		}
		rows = append(rows, row)
	}

	if name != "" {
		if len(rows) != 1 {
			externalError(w, 403, "forbidden", "Use an exact connection name attached to this Crew.")
			return
		}
		externalJSON(w, map[string]any{"crew_id": id, "action": action, "connection": rows[0]})
		return
	}
	if include, _ := args["include_tools"].(bool); include {
		externalError(w, 400, "invalid_arguments", "include_tools requires an exact attached connection name.")
		return
	}
	externalJSON(w, map[string]any{"crew_id": id, "action": action, "connections": crewDiagnosticPage(rows, limit, offset), "metadata_source": "Vault"})
}
