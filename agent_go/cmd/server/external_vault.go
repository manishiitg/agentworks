package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

func isExternalVaultTool(name string) bool {
	switch name {
	case "manage_vault_access", "manage_vault_groups", "manage_vault_secret_access":
		return true
	}
	return false
}

// Reuse the Vault chat's connection/policy schema. Management does not acquire
// the Vault builder's authority to execute upstream tools.
func externalVaultDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	params := caplayerproduct.AccessToolParameters()
	properties := params["properties"].(map[string]interface{})
	operation := properties["operation"].(map[string]interface{})
	enum := []any{}
	for _, name := range operation["enum"].([]string) {
		enum = append(enum, name)
	}
	operation["enum"] = enum
	add("manage_vault_access", strings.TrimSuffix(caplayerproduct.AccessToolDescription, "Read vault-access first.")+" Inspect the environment and exact tool schemas before changing permissions. Requires vault:manage and a current Vault administrator. No workflow_id.", true, false, params["properties"].(map[string]interface{}), "operation", "arguments")
	add("manage_vault_groups", "List/create/edit Vault groups and list/add/remove platform members. Resolve IDs using manage_vault_access list_users. Changes apply immediately. Does not create accounts or provision product slots. Requires vault:manage and a current Vault administrator; no workflow_id.", true, false, map[string]any{
		"operation":   map[string]any{"type": "string", "enum": []any{"list", "create", "update", "list_members", "add_member", "remove_member"}},
		"group_id":    externalString("Existing group ID, or a new unique ID for create."),
		"name":        map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		"description": map[string]any{"type": "string", "maxLength": 2000},
		"user_id":     externalString("Exact platform user ID returned by list_users."),
	}, "operation")
	add("manage_vault_secret_access", "List Vault secret names (optionally assigned to group_id), or set group access with operation=set, group_id, name and allowed. Values are never returned or accepted. Add/rotate values in Vault's secure Secrets panel. Requires vault:manage and a current Vault administrator; no workflow_id.", true, false, map[string]any{
		"operation": map[string]any{"type": "string", "enum": []any{"list", "set"}},
		"group_id":  externalString("Existing Vault group ID."),
		"name":      externalString("Exact existing secret name."),
		"allowed":   map[string]any{"type": "boolean"},
	}, "operation")
}

var externalVaultID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@-]{0,127}$`)

func (api *StreamingAPI) externalVaultCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	// Recheck discovery's authorization: cached tools cannot retain a revoked role.
	if !externalTokenAllows(claims, externalTool{Name: name}) {
		externalError(w, 403, "forbidden", "Vault management requires vault:manage and an active Vault administrator.")
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
	if name == "manage_vault_groups" {
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
	req.URL = &url.URL{Path: "/api/caplayer" + path}
	req.Method, req.Body, req.ContentLength = method, http.NoBody, 0
	if payload != nil {
		req.Body, req.ContentLength = io.NopCloser(bytes.NewReader(data)), int64(len(data))
	}
	api.handleCapLayerAdmin(w, req)
}
