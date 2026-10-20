package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

const externalSignInAppsTool = "sign_in_apps"

func externalSignInAppsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add(externalSignInAppsTool, "Set up this server's sign-in apps as an administrator, using the same rules as Settings. list returns providers, setup status and callback URL; set saves the provider's client_id and client_secret; remove clears its app. Use the provider key from list. Existing users:manage consent is required. Secret values are stored encrypted and never returned; after saving, refer only to the provider name. Each person still connects their own account in their browser. This does not approve sender access or complete anyone's sign-in.", true, false, map[string]any{
		"action":        map[string]any{"type": "string", "enum": []any{"list", "set", "remove"}},
		"provider":      externalString("Provider key from list; required for set/remove."),
		"project_id":    externalString("Google OAuth project ID from its JSON, when available."),
		"client_id":     externalString("set: provider app's client ID."),
		"client_secret": externalString("set: provider app's secret; never repeat it after saving."),
	}, "action")
}

func (api *StreamingAPI) externalSignInAppsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	if !externalUsersAllowed(GetUserFromContext(r.Context())) {
		externalError(w, http.StatusForbidden, "forbidden", "An administrator must set up sign-in apps in Settings or through an administrator connection.")
		return
	}
	request := r.Clone(r.Context())
	switch externalArg(args, "action") {
	case "list":
		request.Method = http.MethodGet
		requireAdmin(api.handleListMCPApps)(w, request)
	case "set", "remove":
		provider := externalArg(args, "provider")
		if !mcpAppKeyPattern.MatchString(provider) {
			externalError(w, http.StatusBadRequest, "invalid_arguments", "Use a provider key from sign_in_apps action=list.")
			return
		}
		request = mux.SetURLVars(request, map[string]string{"key": provider})
		request.Method = http.MethodDelete
		if externalArg(args, "action") == "set" {
			request.Method = http.MethodPut
			body, _ := json.Marshal(map[string]any{"client_id": args["client_id"], "client_secret": args["client_secret"], "project_id": args["project_id"]})
			request.Body = io.NopCloser(bytes.NewReader(body))
			request.ContentLength = int64(len(body))
		}
		requireAdmin(api.handlePutMCPApp)(w, request)
	default:
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Use list, set or remove.")
	}
}
