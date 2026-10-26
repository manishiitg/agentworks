package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// MultiAgentConfigRoutes mounts the per-user multi-agent chat capabilities API.
// The frontend POSTs the user's selected skills/servers/etc. here ("save as
// chat defaults"); bot-channel sessions read the same file so they start with
// the user's chosen setup. GET returns the current saved config.
func MultiAgentConfigRoutes(router *mux.Router) {
	sub := router.PathPrefix("/multiagent/chat-capabilities").Subrouter()
	sub.HandleFunc("", getMultiAgentChatCapabilitiesHandler()).Methods("GET", "OPTIONS")
	sub.HandleFunc("", saveMultiAgentChatCapabilitiesHandler()).Methods("POST", "OPTIONS")
}

// resolveChatConfigUserID prefers an explicit ?user_id, then the authenticated
// user from context, then "default" — mirroring the schedule routes.
func resolveChatConfigUserID(r *http.Request) string {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		userID = GetUserIDFromContext(r.Context())
	}
	if userID == "" {
		userID = "default"
	}
	return userID
}

func getMultiAgentChatCapabilitiesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == "OPTIONS" {
			return
		}
		userID := resolveChatConfigUserID(r)
		cfg, _, err := ReadMultiAgentChatConfig(r.Context(), userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	}
}

func saveMultiAgentChatCapabilitiesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORS(w)
		if r.Method == "OPTIONS" {
			return
		}
		userID := resolveChatConfigUserID(r)

		var caps MultiAgentChatCapabilities
		if err := json.NewDecoder(r.Body).Decode(&caps); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := enforceDeploymentBrowserCapability(&caps.WorkflowCapabilities); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The general chat capability editor predates notifications and
		// replaces the entire capabilities object. Preserve the dedicated Notify
		// setting when an older/current editor omits that field; disabling Notify
		// remains an explicit agent-tool action.
		if caps.Notifications == nil {
			if existing, found, readErr := ReadMultiAgentChatConfig(r.Context(), userID); readErr != nil {
				http.Error(w, readErr.Error(), http.StatusInternalServerError)
				return
			} else if found && existing != nil {
				caps.Notifications = existing.Capabilities.Notifications
				if caps.Notifications != nil {
					caps.SelectedSecrets = removeString(caps.SelectedSecrets, caps.Notifications.SlackWebhookSecretName)
					if caps.SelectedGlobalSecretNames != nil {
						filtered := removeString(*caps.SelectedGlobalSecretNames, caps.Notifications.SlackWebhookSecretName)
						caps.SelectedGlobalSecretNames = &filtered
					}
				}
			}
		} else if strings.TrimSpace(caps.Notifications.SlackWebhookSecretName) == "" {
			caps.Notifications = nil
		} else {
			caps.SelectedSecrets = removeString(caps.SelectedSecrets, caps.Notifications.SlackWebhookSecretName)
			if caps.SelectedGlobalSecretNames != nil {
				filtered := removeString(*caps.SelectedGlobalSecretNames, caps.Notifications.SlackWebhookSecretName)
				caps.SelectedGlobalSecretNames = &filtered
			}
		}
		if err := WriteMultiAgentChatConfig(r.Context(), userID, caps); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "user_id": userID})
	}
}
