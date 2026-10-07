package server

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
)

// Browser endpoints for the "My vaults" screen (PLAT-507). They reuse the tool's operations, so the screen and the agent
// can do exactly the same things and the Vault service checks the same owner rules. The promotes need a Crew, Code or
// workflow chat and stay tool-only. A secret's value is typed here and nowhere else; this handler never logs the body.

var myVaultScreenOperations = map[string]bool{
	"list": true, "apps": true, "create": true, "inspect": true, "add_member": true, "remove_member": true, "add_owner": true,
	"remove_owner": true, "connect": true, "sign_in": true, "sync": true, "remove_connection": true, "remove_secret": true, "delete": true,
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
	person, ok := api.myVaultsPerson(w, r)
	if !ok {
		return
	}
	args := map[string]interface{}{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&args); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid request")
		return
	}
	operation, _ := args["operation"].(string)
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

// POST /api/my-vaults/{id}/secrets {"name": "...", "value": "...", "replace": false}
func (api *StreamingAPI) handleMyVaultSecret(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	person, ok := api.myVaultsPerson(w, r)
	if !ok {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Value   string `json:"value"`
		Replace bool   `json:"replace"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid request")
		return
	}
	vaultID, _ := url.PathUnescape(mux.Vars(r)["id"])
	if err := api.setVaultSecret(r.Context(), person, vaultID, in.Name, in.Value, in.Replace); err != nil {
		writeUsersError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeUsersJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}
