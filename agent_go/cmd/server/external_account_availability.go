package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (api *StreamingAPI) externalGetAccountAvailability(w http.ResponseWriter, r *http.Request, account string) {
	if err := recordCodeAdminView(r.Context(), GetUserFromContext(r.Context()), "get_account_availability", "", "", "account="+account); err != nil {
		externalError(w, http.StatusServiceUnavailable, "audit_unavailable", "The admin audit log is unavailable.")
		return
	}
	current, err := effectiveServerAccountAvailability(r.Context(), account)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "account_access_unavailable", "Could not load this account's access. Open AI models & accounts to check it.")
		return
	}
	externalJSON(w, map[string]any{"account": account, "availability": current})
}

// externalSetAccountAvailability reuses the screen's admin-only write path.
// Personal accounts, credentials and native provider homes are not touched.
func (api *StreamingAPI) externalSetAccountAvailability(w http.ResponseWriter, r *http.Request, account string, args map[string]any) {
	raw, exists := args["available_to"]
	encoded, err := json.Marshal(raw)
	var audience providerAvailability
	if !exists || raw == nil || err != nil || json.Unmarshal(encoded, &audience) != nil {
		externalError(w, http.StatusBadRequest, "invalid_arguments", `Pass available_to: "all", "admins", or {"users":[person IDs]}.`)
		return
	}
	if err := recordCodeAdminView(r.Context(), GetUserFromContext(r.Context()), "set_account_availability", "", "", fmt.Sprintf("account=%s audience=%s people=%d", account, audience.Describe(), len(audience.Users))); err != nil {
		externalError(w, http.StatusServiceUnavailable, "audit_unavailable", "The admin audit log is unavailable.")
		return
	}
	id := serverAccountIDPrefix + account
	status, body := api.externalForward(r, http.MethodPatch, "/api/provider-connections/"+url.PathEscape(id), map[string]string{"connectionID": id}, map[string]any{"available_to": audience}, api.handleProviderConnection)
	if status != http.StatusOK && status != http.StatusNoContent {
		externalError(w, status, "account_access_not_saved", strings.TrimSpace(body))
		return
	}
	current, err := effectiveServerAccountAvailability(r.Context(), account)
	if err != nil {
		externalError(w, http.StatusServiceUnavailable, "account_access_unavailable", "The choice was saved but could not be loaded. Open AI models & accounts to check it.")
		return
	}
	externalJSON(w, map[string]any{"account": account, "availability": current})
}
