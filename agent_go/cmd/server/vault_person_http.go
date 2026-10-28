package server

import (
	"encoding/json"
	"net/http"
)

// Project panel and personal Vault endpoints reuse native operations. The
// caller's current project/Vault authority is rechecked; bodies can carry secret
// values and must never be logged.

var myVaultScreenOperations = map[string]bool{
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
