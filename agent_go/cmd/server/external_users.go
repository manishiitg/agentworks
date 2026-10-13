package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const externalUsersTool = "users"

func externalUsersAllowed(c *UserClaims) bool {
	return c != nil && c.Scope == "" && claimsIsAdmin(c) &&
		(c.AccessToken == nil || c.AccessToken.BoundAgentID == "" && c.AccessToken.Allows("users:manage"))
}

func externalUsersDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	products := map[string]any{"type": "array", "maxItems": 100, "items": externalString("Existing product ID, as on the Users page.")}
	add(externalUsersTool, "Administrator account access management, using the Users page's current rules and existing users:manage consent. list/get return account identity, sign-in method, role, products, create permissions, status and review flags; never credentials. update changes only existing account access through the app's handler. Omitted fields stay unchanged; clear_create_products restores role defaults. Changing administrator access requires confirm=user_id. Self-demotion, self-disable and removing the last active administrator are refused. update requires a stable request_id: for 7 days an identical retry returns the recorded result and different arguments with the same ID are refused. After 7 days the ID is a new update; inspect current access before resending. Every new change is audited with its caller. Builder/Run product access continues to follow existing roles. Accounts are provisioned separately.", false, false, map[string]any{
		"action":   map[string]any{"type": "string", "enum": []any{"list", "get", "update"}},
		"user_id":  externalString("Existing account ID; required for get/update."),
		"role":     map[string]any{"type": "string", "enum": []any{"admin", "creator", "editor", "viewer"}, "description": "Existing account role ID."},
		"products": products, "create_products": products,
		"clear_create_products": map[string]any{"type": "boolean"},
		"disabled":              map[string]any{"type": "boolean"},
		"code_reviewer":         map[string]any{"type": "boolean", "description": "May inspect Code through the existing audited review access."},
		"vault_reader":          map[string]any{"type": "boolean", "description": "May inspect Vault through its existing access rules."},
		"confirm":               externalString("Repeat user_id to confirm a change to administrator access."),
		"request_id":            map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable update retry ID; reuse only with identical arguments within 7 days. After expiration it is a new update."},
	}, "action")
}

// Explicit projection: additions to the app account response never expand MCP.
type externalUserView struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Email          string    `json:"email,omitempty"`
	SignInMethod   string    `json:"sign_in_method"`
	Role           string    `json:"role"`
	Products       []string  `json:"products"`
	CreateProducts *[]string `json:"create_products"`
	Disabled       bool      `json:"disabled"`
	CodeReviewer   bool      `json:"code_reviewer"`
	VaultReader    bool      `json:"vault_reader"`
}

// Sign-in words match the Users page: the identity provider's name is not
// something administrators act on, so every SSO account reads "sso" and only
// a plain password account reads "password" (856-81).
func externalSignInMethod(v userAdminView) string {
	if v.Invited || (v.Provider != "" && v.Provider != "simple") {
		return "sso"
	}
	return "password"
}

func externalUserOf(v userAdminView) externalUserView {
	return externalUserView{v.ID, v.Username, v.Email, externalSignInMethod(v), v.Role, v.Products, v.CreateProducts, v.Disabled, v.CodeReviewer, v.VaultReader}
}

func userAccessHash(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (api *StreamingAPI) externalUsersCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !externalUsersAllowed(claims) || !currentUserIsAdmin(r) {
		externalError(w, 403, "insufficient_scope", "Administrator access and users:manage consent are required.")
		return
	}
	action, id := externalArg(args, "action"), externalArg(args, "user_id")
	if action == "list" || action == "get" {
		if action == "get" && id == "" {
			externalError(w, 400, "invalid_arguments", "get requires user_id.")
			return
		}
		for key := range args {
			if key != "action" && key != "user_id" {
				externalError(w, 400, "invalid_arguments", "Account changes are accepted only with action=update.")
				return
			}
		}
		status, body := api.externalForward(r, http.MethodGet, "/api/admin/users", nil, nil, requireAdmin(api.handleAdminListUsers))
		if status != http.StatusOK {
			externalError(w, status, "users_failed", body)
			return
		}
		var response struct {
			Users    []userAdminView `json:"users"`
			Products []string        `json:"products"`
		}
		if json.Unmarshal([]byte(body), &response) != nil {
			externalError(w, 502, "users_failed", "Invalid account response.")
			return
		}
		views := []externalUserView{}
		for _, user := range response.Users {
			if action == "list" || user.ID == id {
				views = append(views, externalUserOf(user))
			}
		}
		if action == "get" && len(views) == 0 {
			externalError(w, 404, "not_found", "Account not found.")
			return
		}
		if err := recordCodeAdminView(r.Context(), claims, "user_directory_read", id, "", action); err != nil {
			externalError(w, 503, "audit_unavailable", "The account read could not be audited.")
			return
		}
		if action == "get" {
			externalJSON(w, map[string]any{"user": views[0]})
		} else {
			externalJSON(w, map[string]any{"users": views, "products": response.Products})
		}
		return
	}
	if action != "update" {
		externalError(w, 400, "invalid_arguments", "action must be list, get or update.")
		return
	}
	requestID := externalArg(args, "request_id")
	if id == "" || requestID == "" || len(requestID) > 128 || strings.TrimSpace(requestID) != requestID {
		externalError(w, 400, "invalid_arguments", "update requires user_id and a stable request_id (at most 128 characters).")
		return
	}
	payload := map[string]any{}
	fields := []string{}
	for _, field := range []string{"role", "products", "create_products", "clear_create_products", "disabled", "code_reviewer", "vault_reader"} {
		if value, exists := args[field]; exists {
			payload[field] = value
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 || args["create_products"] != nil && args["clear_create_products"] == true {
		externalError(w, 400, "invalid_arguments", "Supply access fields; create_products and clear_create_products cannot both be set.")
		return
	}
	sort.Strings(fields)
	mutation := &userAccessMutation{Key: userAccessHash([]string{claims.UserID, requestID}), Hash: userAccessHash(args), Confirm: externalArg(args, "confirm"), RequestID: requestID, Fields: strings.Join(fields, ",")}
	r = r.WithContext(context.WithValue(r.Context(), userAccessMutationKey{}, mutation))
	status, body := api.externalForward(r, http.MethodPut, "/api/admin/users/"+url.PathEscape(id), map[string]string{"id": id}, payload, requireAdmin(api.handleAdminUpdateUser))
	if status != http.StatusOK {
		externalError(w, status, "users_failed", body)
		return
	}
	var user userAdminView
	if json.Unmarshal([]byte(body), &user) != nil {
		externalError(w, 502, "users_failed", "Invalid account response.")
		return
	}
	externalJSON(w, map[string]any{"user": externalUserOf(user), "request_id": requestID})
}
