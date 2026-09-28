package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// Gmail is an outbound-only notification channel. The UI manages it entirely
// through these endpoints — enable/disable, set the default recipient, and see
// live auth status — so the user never edits config/gmail-config.json by hand.

// GmailConfigRequest is the body the UI posts to enable/disable Gmail.
type GmailConfigRequest struct {
	Enabled           bool     `json:"enabled"`
	DefaultTo         string   `json:"default_to"`
	BlockedRecipients []string `json:"blocked_recipients,omitempty"`
	GwsPath           string   `json:"gws_path,omitempty"`
	ConfigHome        string   `json:"config_home,omitempty"`
	CredentialsFile   string   `json:"credentials_file,omitempty"`
	Token             string   `json:"token,omitempty"`
}

// GmailConfigResponse is the saved config plus live, auto-detected auth state,
// so the UI can render the toggle and a "Connected / Connect Gmail" badge
// without the user inspecting anything on the host.
type GmailConfigResponse struct {
	Enabled           bool                     `json:"enabled"`
	DefaultTo         string                   `json:"default_to,omitempty"`
	BlockedRecipients []string                 `json:"blocked_recipients,omitempty"`
	Auth              services.GmailAuthStatus `json:"auth"`
	// Ready is the bottom-line "this channel will actually send" signal:
	// enabled + a default recipient + gws installed/authenticated with a Gmail scope.
	Ready bool `json:"ready"`
}

// GmailTestResponse is the result of the "send test email" button.
type GmailTestResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// GmailFeedbackRoutes wires up the Gmail config/status/test API.
func GmailFeedbackRoutes(router *mux.Router, api *StreamingAPI) {
	r := router.PathPrefix("/api/human-feedback/gmail").Subrouter()
	r.HandleFunc("/config", getGmailConfigHandler(api)).Methods("GET")
	r.HandleFunc("/config", requireAdminWrite(updateGmailConfigHandler(api))).Methods("POST", "OPTIONS")
	r.HandleFunc("/status", getGmailStatusHandler(api)).Methods("GET")
	r.HandleFunc("/test", requireAdminWrite(testGmailConnectionHandler(api))).Methods("POST", "OPTIONS")
	r.HandleFunc("/service-catalog", getGoogleServiceCatalogHandler(api)).Methods("GET")
}

// getGoogleServiceCatalogHandler lists the additional Google Workspace
// services (beyond Gmail) a connection may request, so the UI's checkbox list
// stays in sync with the backend's scope catalog rather than hardcoding a
// second copy of it.
func getGoogleServiceCatalogHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(services.GoogleServiceCatalog())
	}
}

// ensureGmailService returns the global Gmail service, initializing it lazily.
func ensureGmailService() (*services.GmailService, error) {
	if svc := services.GetGmailService(); svc != nil {
		return svc, nil
	}
	return services.InitGmailService()
}

func buildGmailConfigResponse(svc *services.GmailService, cfg *services.GmailConfig, auth services.GmailAuthStatus) GmailConfigResponse {
	return GmailConfigResponse{
		Enabled:           cfg.Enabled,
		DefaultTo:         cfg.DefaultTo,
		BlockedRecipients: cfg.BlockedRecipients,
		Auth:              auth,
		Ready:             cfg.Enabled && cfg.DefaultTo != "" && auth.Authenticated && auth.HasGmailScope,
	}
}

func getGmailConfigHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, err := ensureGmailService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Gmail service: %v", err), http.StatusInternalServerError)
			return
		}
		autoConfigureGmailIfAuthenticated(r.Context(), svc)
		cfg := svc.GetConfig()
		auth := svc.EffectiveAuthStatus(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildGmailConfigResponse(svc, cfg, auth))
	}
}

// autoConfigureGmailIfAuthenticated flips the channel on, and (the first time
// only, when no recipient has ever been set) defaults the recipient to the
// account gws just authenticated as, whenever gws is authenticated with the
// Gmail scope. A gws login already names one Google account; a human should
// not also have to retype that same address into a second field before
// Gmail can deliver anything. A failure to persist either is logged, never
// surfaced, because the read the caller is serving still has an answer.
func autoConfigureGmailIfAuthenticated(ctx context.Context, svc *services.GmailService) {
	if enabled, err := svc.EnableIfAuthenticated(ctx); err != nil {
		log.Printf("[GMAIL] auto-enable failed: %v", err)
	} else if enabled {
		log.Printf("[GMAIL] channel enabled automatically: gws is authenticated with the Gmail scope")
	}
	if defaulted, err := svc.AutoDefaultRecipientIfMissing(ctx); err != nil {
		log.Printf("[GMAIL] auto-default recipient failed: %v", err)
	} else if defaulted {
		log.Printf("[GMAIL] default recipient set automatically to the authenticated gws account")
	}
}

// mergeGmailConfigRequest applies the settings form onto the stored config.
// Only the fields the form owns change; registry state (connections, default
// connection, adoption/disable flags) survives a save. Switching the channel
// off through the form is the one deliberate signal that must stop
// auto-enable, so it sets ManuallyDisabled; switching it on clears it.
func mergeGmailConfigRequest(current *services.GmailConfig, req GmailConfigRequest) *services.GmailConfig {
	cfg := &services.GmailConfig{}
	if current != nil {
		copied := *current
		cfg = &copied
	}
	cfg.Enabled = req.Enabled
	cfg.DefaultTo = req.DefaultTo
	cfg.BlockedRecipients = req.BlockedRecipients
	cfg.GwsPath = req.GwsPath
	cfg.ConfigHome = req.ConfigHome
	cfg.CredentialsFile = req.CredentialsFile
	cfg.Token = req.Token
	cfg.ManuallyDisabled = !req.Enabled
	return cfg
}

// getGmailStatusHandler returns just the auto-detected auth state — useful for
// the UI to poll/refresh the connection badge without re-fetching config.
func getGmailStatusHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc, err := ensureGmailService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Gmail service: %v", err), http.StatusInternalServerError)
			return
		}
		autoConfigureGmailIfAuthenticated(r.Context(), svc)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(svc.EffectiveAuthStatus(r.Context()))
	}
}

func updateGmailConfigHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req GmailConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}

		svc, err := ensureGmailService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Gmail service: %v", err), http.StatusInternalServerError)
			return
		}

		// Merge into the stored config rather than replacing it: the form
		// carries only the channel fields, and writing it verbatim wiped the
		// connections registry (accounts, default, adoption/disable flags)
		// every time Default recipients was saved.
		cfg := mergeGmailConfigRequest(svc.GetConfig(), req)
		if err := svc.SaveConfig(r.Context(), cfg); err != nil {
			http.Error(w, fmt.Sprintf("failed to save config: %v", err), http.StatusInternalServerError)
			return
		}

		// Reflect the toggle on the live NotificationManager so it takes effect
		// without a restart: register when enabled, unregister when disabled.
		nm := services.GetNotificationManager()
		if nm != nil {
			if svc.IsEnabled() {
				nm.RegisterConnector(svc)
			} else {
				nm.UnregisterConnector("gmail")
			}
		}

		saved := svc.GetConfig()
		auth := svc.EffectiveAuthStatus(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(buildGmailConfigResponse(svc, saved, auth))
	}
}

func testGmailConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		svc, err := ensureGmailService()
		if err != nil {
			writeGmailTest(w, false, fmt.Sprintf("failed to initialize Gmail service: %v", err))
			return
		}

		// Optional override recipient (test before saving). Falls back to default_to.
		to := ""
		var blockedRecipients []string
		hasDraftConfig := false
		if r.ContentLength > 0 {
			var req GmailConfigRequest
			if json.NewDecoder(r.Body).Decode(&req) == nil {
				to = req.DefaultTo
				blockedRecipients = req.BlockedRecipients
				hasDraftConfig = true
			}
		}

		var msgID string
		if hasDraftConfig {
			msgID, err = svc.SendTestWithBlockedRecipients(r.Context(), to, blockedRecipients)
		} else {
			msgID, err = svc.SendTest(r.Context(), to)
		}
		if err != nil {
			log.Printf("[GMAIL] test send failed: %v", err)
			writeGmailTest(w, false, fmt.Sprintf("Test failed: %v", err))
			return
		}
		writeGmailTest(w, true, fmt.Sprintf("Test email sent (id: %s). Check the recipient inbox.", msgID))
	}
}

func writeGmailTest(w http.ResponseWriter, success bool, message string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GmailTestResponse{Success: success, Message: message})
}
