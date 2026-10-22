package server

import (
	"fmt"
	"net/http"
)

// The legacy platform namespace is now a Vault credential store only. An
// explicit scope plus central admin permission is required to manage it.
func vaultOAuthScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	if scope == "vault" && currentUserIsAdmin(r) && userAllowedProduct(GetUserFromContext(r.Context()), "mcp-gateway") {
		return true
	}
	writeUsersError(w, 403, fmt.Sprintf("invalid MCP scope %q; Vault sign-in requires an administrator", scope))
	return false
}
