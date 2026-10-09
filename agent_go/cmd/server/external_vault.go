package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

func isExternalVaultTool(name string) bool {
	for _, admitted := range caplayerproduct.ExternalTools() {
		if admitted == name {
			return true
		}
	}
	return false
}

func externalVaultDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	for _, spec := range vaultManagementDefinitions() {
		// Normalize schema arrays for the external JSON Schema validator.
		data, _ := json.Marshal(spec.Parameters)
		var schema map[string]any
		_ = json.Unmarshal(data, &schema)
		required := []string{}
		for _, name := range schema["required"].([]any) {
			required = append(required, name.(string))
		}
		write := spec.Name != "query_vault_db" && spec.Name != "list_vault_mcp_servers" && spec.Name != "read_vault_audit"
		add(spec.Name, spec.Description, write, false, schema["properties"].(map[string]any), required...)
	}
}

var externalVaultID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)

func (api *StreamingAPI) externalVaultCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	// Recheck discovery's authorization: cached tools cannot retain a revoked role.
	if !externalTokenAllows(claims, externalTool{Name: name}) {
		externalError(w, 403, "forbidden", "Vault management requires vault:manage and an active Vault administrator.")
		return
	}
	api.vaultManagementCall(w, r, name, args)
}

// Both transports supply an authenticated identity; arguments never select it.
func (api *StreamingAPI) vaultManagementCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || !vaultBuilderAdministrator(claims.UserID) {
		externalError(w, 403, "forbidden", "Vault administrator required.")
		return
	}
	if name == "query_vault_db" || name == "mutate_vault_db" {
		operation := "query"
		if name == "mutate_vault_db" {
			operation = "mutate"
		}
		payload, err := json.Marshal(args)
		if err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		result, err := capLayerAgentRequest(r.Context(), claims.UserID, "/api/admin/database/"+operation, payload)
		if err != nil {
			externalError(w, 502, "vault_operation_failed", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(result))
		return
	}
	if name == "list_vault_mcp_servers" || name == "call_vault_mcp_tool" {
		result, err := vaultManagementMCP(r.Context(), claims.UserID, name, args)
		if err != nil {
			externalError(w, 502, "vault_operation_failed", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(result))
		return
	}
	fail := func(message string) { externalError(w, 400, "invalid_arguments", message) }
	if name == "manage_vault_access" {
		payload, err := json.Marshal(args["arguments"])
		if err != nil {
			fail("Invalid Vault arguments.")
			return
		}
		result, err := api.capLayerConnectionAccess(r.Context(), claims.UserID, externalArg(args, "operation"), payload)
		if err != nil {
			externalError(w, 502, "vault_operation_failed", err.Error())
			return
		}
		externalJSON(w, json.RawMessage(result))
		return
	}
	op, group := externalArg(args, "operation"), externalArg(args, "group_id")
	if group != "" && !externalVaultID.MatchString(group) {
		fail("Invalid group_id.")
		return
	}
	path, method := "/api/admin/groups", http.MethodGet
	var payload any
	query := url.Values{}
	if name == "manage_vault_tools" {
		publicName := externalArg(args, "public_name")
		if op != "list" && (publicName == "" || strings.ContainsAny(publicName, "/?#") || strings.Contains(publicName, "..")) {
			fail("An exact public_name from operation=list is required.")
			return
		}
		switch op {
		case "list":
			path = "/api/admin/tools"
		case "versions":
			path = "/api/admin/tools/" + url.PathEscape(publicName) + "/versions"
		case "approve":
			version, ok := args["version"].(float64)
			if externalArg(args, "fingerprint") == "" || !ok {
				fail("approve requires the fingerprint and version of the reviewed definition, from operation=versions.")
				return
			}
			path, method = "/api/admin/tools/"+url.PathEscape(publicName)+"/approve", http.MethodPost
			payload = map[string]any{"fingerprint": externalArg(args, "fingerprint"), "version": int(version)}
		default:
			fail("Unknown tool review operation.")
			return
		}
	} else if name == "read_vault_audit" {
		path = "/api/admin/audit"
		if op == "usage" {
			path = "/api/admin/usage"
		} else if op != "events" {
			fail("operation must be events or usage.")
			return
		}
		for _, key := range []string{"user", "group", "client", "connector", "tool", "decision", "outcome", "after", "before"} {
			if value := externalArg(args, key); value != "" {
				query.Set(key, value)
			}
		}
		if op == "events" {
			query.Set("limit", fmt.Sprint(externalInt(args, "limit", 100)))
		}
	} else if name == "manage_vault_groups" {
		switch op {
		case "list":
		case "create":
			if group == "" || externalArg(args, "name") == "" {
				fail("create requires group_id and name.")
				return
			}
			method = http.MethodPost
			payload = map[string]any{"id": group, "name": args["name"], "description": externalArg(args, "description")}
		case "update":
			if group == "" {
				fail("update requires group_id.")
				return
			}
			fields := map[string]any{}
			for _, key := range []string{"name", "description"} {
				if value, ok := args[key]; ok {
					fields[key] = value
				}
			}
			if len(fields) == 0 {
				fail("update requires name or description.")
				return
			}
			path, method, payload = path+"/"+url.PathEscape(group), http.MethodPost, fields
		case "list_members", "add_member", "remove_member":
			if group == "" {
				fail("group_id is required.")
				return
			}
			path += "/" + url.PathEscape(group) + "/members"
			if op != "list_members" {
				user := externalArg(args, "user_id")
				if !externalVaultID.MatchString(user) {
					fail("An exact valid platform user_id is required.")
					return
				}
				if op == "add_member" {
					method, payload = http.MethodPost, map[string]any{"user_id": user}
				} else {
					method, path = http.MethodDelete, path+"/"+url.PathEscape(user)
				}
			}
		default:
			fail("Unknown group operation.")
			return
		}
	} else if name == "manage_vault_secret_access" {
		switch op {
		case "list":
			if group == "" {
				// Explicit projection excludes sealed values and host credentials.
				rows := []map[string]any{}
				for _, entry := range getGlobalSecrets() {
					rows = append(rows, map[string]any{"name": entry.Name, "managed": entry.Managed})
				}
				externalJSON(w, map[string]any{"secrets": rows})
				return
			}
			path = "/api/admin/groups/" + url.PathEscape(group) + "/secrets"
		case "set":
			secret := externalArg(args, "name")
			allow, ok := args["allowed"].(bool)
			if group == "" || !globalSecretNamePattern.MatchString(secret) || len(secret) > 128 || !ok {
				fail("set requires group_id, an exact secret name and boolean allowed.")
				return
			}
			if err := syncVaultSecretMetadata(r.Context(), claims.UserID); err != nil {
				externalError(w, 502, "vault_operation_failed", "Could not register secret names.")
				return
			}
			path, method, payload = "/api/admin/groups/"+url.PathEscape(group)+"/secrets", http.MethodPost, map[string]any{"name": secret, "allowed": allow}
		default:
			fail("Unknown secret access operation.")
			return
		}
	} else {
		fail("Unknown Vault tool.")
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fail("Invalid Vault arguments.")
		return
	}
	// Reuse the UI's active-directory binding and service transport. The client
	// cannot choose an arbitrary administration path or receive gateway credentials.
	req := r.Clone(r.Context())
	req.URL = &url.URL{Path: "/api/caplayer" + path, RawQuery: query.Encode()}
	req.Method, req.Body, req.ContentLength = method, http.NoBody, 0
	if payload != nil {
		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(data)), int64(len(data))
	}
	api.handleCapLayerAdmin(w, req)
}
