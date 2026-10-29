package server

import (
	"encoding/json"
	"net/http"
)

// Project panel and personal Vault endpoints reuse native operations. The
// caller's current project/Vault authority is rechecked; bodies can carry secret
// values and must never be logged.

var myVaultScreenOperations = map[string]bool{
	"github_status": true, "github_connect": true, "github_disconnect": true,
	"project": true, "project_select": true, "laptops": true, "apps": true, "connect": true, "sign_in": true, "sync": true, "remove_connection": true, "remove_secret": true,
}

func (api *StreamingAPI) myVaultsPerson(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || !personVaultActive(claims.UserID) {
		writeUsersError(w, http.StatusForbidden, "your account cannot manage vaults")
		return "", false
	}
	return claims.UserID, true
}

// POST /api/my-vaults/op {"operation": "...", ...}
func (api *StreamingAPI) handleMyVaultsOp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	args := map[string]interface{}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&args); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid request")
		return
	}
	operation, _ := args["operation"].(string)
	person := ""
	if operation == "project" || operation == "project_select" {
		caller := GetUserFromContext(r.Context())
		if caller == nil || !activeMCPPerson(caller.UserID) {
			writeUsersError(w, http.StatusForbidden, "Open a project you can build to manage Vault")
			return
		}
		person = caller.UserID
	} else {
		var ok bool
		person, ok = api.myVaultsPerson(w, r)
		if !ok {
			return
		}
	}
	if !myVaultScreenOperations[operation] {
		writeUsersError(w, http.StatusBadRequest, "that operation is not available here")
		return
	}
	result, err := api.myVaultsOperation(r.Context(), person, args)
	if err != nil {
		writeUsersError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeUsersJSON(w, http.StatusOK, map[string]string{"result": result})
}

// externalMyVaultsDefinition offers the person's own Vault items over MCP (PLAT-856-400), the same operations as the
// chat tool, so a person's own agent can do what the project's Vault panel does. The grouped vault tool presents
// them as vault action=mine_*.
func externalMyVaultsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	data, _ := json.Marshal(manageMyVaultsParameters())
	var schema map[string]any
	_ = json.Unmarshal(data, &schema)
	add("manage_my_vaults", manageMyVaultsDescription, true, false, schema["properties"].(map[string]any), "operation")
}

// externalMyVaultsCall runs one operation for the signed-in person, with the website's checks.
func (api *StreamingAPI) externalMyVaultsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	operation, _ := args["operation"].(string)
	if !personalLaptopSettingsClaims(c) || !myVaultScreenOperations[operation] && operation != "set_secret" && operation != "promote_secret" {
		externalError(w, 403, "forbidden", "Use your own sign-in to manage your own Vault items, or open the project's Vault panel.")
		return
	}
	if (operation == "project" || operation == "project_select") && !activeMCPPerson(c.UserID) || operation != "project" && operation != "project_select" && !personVaultActive(c.UserID) {
		externalError(w, 403, "forbidden", "Your account cannot keep its own Vault items. Ask an administrator for Builder access.")
		return
	}
	result, err := api.myVaultsOperation(r.Context(), c.UserID, args)
	if err != nil {
		externalError(w, 400, "vault_operation_failed", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if json.Valid([]byte(result)) {
		externalJSON(w, json.RawMessage(result))
		return
	}
	externalJSON(w, map[string]string{"message": result})
}
