package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Tool contracts are shared verbatim by the builder and platform MCP catalog.
func vaultManagementDefinitions() []agentprofiles.ToolSpec {
	object := func(props map[string]any, required ...string) map[string]any {
		if required == nil {
			required = []string{}
		}
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
	}
	specs := []agentprofiles.ToolSpec{
		{Name: "manage_vault_access", Category: "vault", Description: caplayerproduct.AccessToolDescription, Parameters: caplayerproduct.AccessToolParameters()},
		{Name: "manage_vault_groups", Category: "vault", Description: "List/create/edit Vault groups and list/add/remove active platform members. attach_server gives the group a whole connection (connector_id), read_only=true for only its read tools (see manage_vault_tools labels); detach_server removes it. Resolve IDs using manage_vault_access list_users and inspect_environment. Changes apply immediately. Does not create accounts or provision product slots.", Parameters: object(map[string]any{
			"operation":    map[string]any{"type": "string", "enum": []string{"list", "create", "update", "list_members", "add_member", "remove_member", "attach_server", "detach_server"}},
			"connector_id": externalString("For attach_server/detach_server: the connection ID from inspect_environment."),
			"read_only":    map[string]any{"type": "boolean", "description": "For attach_server: only the connection's read tools. Omit to keep the current level (new grants are full)."},
			"group_id":     externalString("Existing group ID, or a new unique ID for create."),
			"name":         map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"description":  map[string]any{"type": "string", "maxLength": 2000, "description": "Shown to every member of the group. Say what the group is for; never name people, emails or who is excluded (membership is the member list)."},
			"user_id":      externalString("Exact active platform user ID returned by list_users."),
		}, "operation")},
		{Name: "manage_vault_secret_access", Category: "vault", Description: "Vault secrets. operation=list: secret names (optionally those assigned to group_id). operation=set with group_id, name and allowed: group access. operation=share with name, source_workflow_id or source_crew_id, group_ids and optional vault_name: copies that project's secret into Vault on the server and grants the groups (the project copy stays; copies rotate independently). operation=delete with name and confirm repeating it. No operation accepts or returns a typed value. To add a secret over MCP: set it on the project first (the Crew settings tool, secrets.set, owner only), then call share; the copy is made on the server. Rotate a value in Vault's Secrets panel or by re-sharing after changing the project copy. share, set and delete need a Vault administrator (vault:manage); group_ids limits who can select the copy (Platform = everyone).", Parameters: object(map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"list", "set", "share", "delete"}},
			"group_id":  externalString("Existing Vault group ID."), "name": externalString("Exact existing secret name (for share: the project's secret name)."), "allowed": map[string]any{"type": "boolean"},
			"source_workflow_id": externalString("For share: the source workflow or Relay ID."), "source_crew_id": externalString("For share: the source Crew ID."),
			"vault_name": externalString("For share: name in Vault; defaults to name. Existing names are never overwritten."),
			"group_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 100, "description": "For share: Vault group IDs from manage_vault_groups list."},
			"confirm":    externalString("For delete: repeat the secret name."),
		}, "operation")},
	}
	for _, definition := range virtualtools.WorkflowDBSQLToolDefinitions() {
		data, err := json.Marshal(definition.Function.Parameters)
		if err != nil {
			panic(err)
		}
		var params map[string]any
		data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "query_workflow_db", "query_vault_db"), "mutate_workflow_db", "mutate_vault_db"))
		if err := json.Unmarshal(data, &params); err != nil {
			panic(err)
		}
		params["additionalProperties"] = false
		params["required"] = []string{}
		name := strings.Replace(definition.Function.Name, "workflow", "vault", 1)
		description := "Query the Vault governance database; use action=describe first. Only read-only SQL is accepted."
		if name == "mutate_vault_db" {
			description = "Apply an atomic parameterized SQL mutation to the Vault governance database. Mutable tables: groups, group_members, group_tool_grants, and user_tool_grants for deleting old direct grants only (Vault access is group-only). Prefer manage_vault_groups for active directory-bound membership changes and manage_vault_access for MCP removals and validated regex rules. Users, credentials, connectors, approvals and policy history are read-only."
		}
		specs = append(specs, agentprofiles.ToolSpec{Name: name, Category: "vault", Description: description + " The gateway owns SQLite and enforces table/row restrictions. Never supply a database path or edit physical files. Secret values are excluded.", Parameters: params})
	}
	specs = append(specs,
		agentprofiles.ToolSpec{Name: "list_vault_mcp_servers", Category: "vault", Description: "List all active Vault connections, approved tool schemas, groups and secret names for administrator setup. This setup inventory is independent of group grants. Private connections belonging to other people are excluded; secret values are never exposed.", Parameters: object(map[string]any{})},
		agentprofiles.ToolSpec{Name: "call_vault_mcp_tool", Category: "vault", Description: "Execute an active approved Vault MCP tool as the administrator for setup, independently of group/regex grants. Discover its exact server, tool and input schema using list_vault_mcp_servers first. Use searches/fetches to resolve canonical IDs before configuring permissions. Upstream writes require the user's explicit request. Every call rechecks administrator access and is audited as that user. Ordinary product and Vault runtime calls remain group scoped.", Parameters: object(map[string]any{
			"server": externalString("Exact vault_<connection ID> from list_vault_mcp_servers."), "tool": externalString("Exact discovered public tool name."), "arguments": map[string]any{"type": "object"},
		}, "server", "tool", "arguments")},
		agentprofiles.ToolSpec{Name: "manage_vault_tools", Category: "vault", Description: "Review a Vault connection's tools after a sync, as the Vault Servers page does. operation=list: every tool with its Status (quarantined = new or changed, not callable until approved). operation=versions with public_name: what changed. operation=approve with public_name, fingerprint and version from versions: approves exactly that reviewed definition (a later change quarantines it again). operation=set_access with public_name and access=read|write labels the tool for read-only server grants (empty returns to the server's readOnlyHint; unmarked tools are write); list shows each tool's access.", Parameters: object(map[string]any{
			"operation":   map[string]any{"type": "string", "enum": []string{"list", "versions", "approve", "set_access"}},
			"access":      map[string]any{"type": "string", "enum": []string{"read", "write", ""}},
			"public_name": externalString("Exact public tool name from list."),
			"fingerprint": externalString("Fingerprint of the reviewed version, from versions."),
			"version":     map[string]any{"type": "integer", "minimum": 1},
		}, "operation")},
		agentprofiles.ToolSpec{Name: "read_vault_audit", Category: "vault", Description: "Read the Vault audit log (operation=events: who called which tool, when, the decision and outcome) or a usage summary (operation=usage). Optional filters: user, group, client, connector, tool, decision, outcome, after, before (RFC 3339). Read-only. For who changed access (groups, members, grants, rules, SQL edits), query_vault_db the policy_history table.", Parameters: object(map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"events", "usage"}},
			"user":      externalString("Platform user ID."), "group": externalString("Vault group ID."), "client": externalString("Client name."),
			"connector": externalString("Connection ID."), "tool": externalString("Public tool name."), "decision": externalString("allow or deny."),
			"outcome": externalString("Call outcome."), "after": externalString("RFC 3339 start."), "before": externalString("RFC 3339 end."),
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
		}, "operation")},
	)
	return specs
}

// Register the same executors used by platform MCP, under manifest-owned bindings.
func registerVaultManagementTools(registry *agentprofiles.Registry, apis ...*StreamingAPI) error {
	api := &StreamingAPI{}
	if len(apis) > 0 {
		api = apis[0]
	}
	profile := caplayerproduct.Manifest().Profile
	definitions := vaultManagementDefinitions()
	names := caplayerproduct.ExternalTools()
	if len(names) != len(definitions) {
		return fmt.Errorf("Vault product.yaml and implementations differ")
	}
	for i, definition := range definitions {
		if definition.Name != names[i] {
			return fmt.Errorf("Vault product.yaml tool %q has no matching implementation", names[i])
		}
	}
	for i, binding := range profile.Tools {
		spec := definitions[i]
		if binding.ID == "caplayer.access" {
			continue
		} // Registered by the shared product runtime.
		if err := registry.RegisterToolFactory(binding.ID, func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
			out := spec
			out.Execute = func(ctx context.Context, args map[string]any) (string, error) {
				if canonicalChatHistoryWorkspacePath(runtime.UserID, runtime.WorkspacePath) != caplayerproduct.WorkspaceRoot {
					return "", fmt.Errorf("Vault management requires its own chat project")
				}
				// The same compiled schema guards direct builder calls and external calls.
				catalog, err := externalTools()
				if err != nil {
					return "", err
				}
				var tool *externalTool
				for i := range catalog {
					if catalog[i].Name == out.Name {
						tool = &catalog[i]
						break
					}
				}
				if tool == nil {
					return "", fmt.Errorf("Vault tool missing from catalog: %s", out.Name)
				}
				if err := tool.validator.Validate(args); err != nil {
					return "", fmt.Errorf("invalid Vault arguments: %w", err)
				}
				if claims := GetUserFromContext(ctx); claims != nil {
					if claims.UserID != runtime.UserID {
						return "", fmt.Errorf("Vault tool identity changed")
					}
				} else {
					ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: runtime.UserID})
				}
				if out.Name == "list_vault_mcp_servers" || out.Name == "call_vault_mcp_tool" {
					if _, present, err := api.vaultBuilderAuthority(ctx, runtime.UserID); err != nil || !present {
						return "", fmt.Errorf("Vault setup requires an administrator-owned builder session")
					}
				}
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/internal/vault/tool", nil)
				if err != nil {
					return "", err
				}
				response := &accessToolResponse{header: make(http.Header)}
				api.vaultManagementCall(response, request, out.Name, args)
				if response.status >= 300 {
					return "", fmt.Errorf("Vault operation failed: %s", response.String())
				}
				return response.String(), nil
			}
			return out, nil
		}); err != nil {
			return err
		}
	}
	return nil
}
