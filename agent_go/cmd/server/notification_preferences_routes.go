package server

import (
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// NotificationPreferencesRoutes mounts the per-user preference endpoints.
//
//	GET  /api/notification-preferences       → returns the calling user's prefs (or zero-valued struct)
//	POST /api/notification-preferences       → upserts the calling user's prefs (body = NotificationPreference)
//
// The connector resolvers (Slack today, WhatsApp later) consult these prefs
// at notification-send time before falling back to the workspace-wide default.
func NotificationPreferencesRoutes(router *mux.Router) {
	apiRouter := router.PathPrefix("/notification-preferences").Subrouter()
	apiRouter.HandleFunc("", getNotificationPreferencesHandler).Methods("GET")
	apiRouter.HandleFunc("", updateNotificationPreferencesHandler).Methods("POST", "OPTIONS")
}

func getNotificationPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || user.UserID == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	pref := services.GetNotificationPreference(user.UserID)
	if pref == nil {
		pref = &services.NotificationPreference{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pref)
}

func updateNotificationPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || user.UserID == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	var pref services.NotificationPreference
	if err := json.NewDecoder(r.Body).Decode(&pref); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := services.SetNotificationPreference(r.Context(), user.UserID, &pref); err != nil {
		http.Error(w, "failed to save preferences: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
