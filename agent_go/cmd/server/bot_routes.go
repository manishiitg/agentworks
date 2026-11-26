package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/gorilla/mux"
)

// BotRoutes sets up the bot connector API routes.
// Bot sessions are regular chat sessions with BotMetadata attached to their
// on-disk manifest; the handlers here translate between the unified model and
// the legacy BotSession shape the frontend still expects.
func BotRoutes(router *mux.Router, api *StreamingAPI) {
	botRouter := router.PathPrefix("/bot").Subrouter()

	// Connector config routes
	botRouter.HandleFunc("/connectors", listBotConnectorsHandler(api)).Methods("GET")
	botRouter.HandleFunc("/connectors/{platform}", getBotConnectorHandler(api)).Methods("GET")
	botRouter.HandleFunc("/connectors/{platform}", saveBotConnectorHandler(api)).Methods("POST", "OPTIONS")
	botRouter.HandleFunc("/connectors/{platform}/test", testBotConnectorHandler(api)).Methods("POST", "OPTIONS")

	// Shared "_global" config (allowed_emails and friends) -- see bot_config_routes.go
	botRouter.HandleFunc("/config", botConfigGetHandler(api.chatStore)).Methods("GET")
	botRouter.HandleFunc("/config", botConfigSaveHandler(api.chatStore)).Methods("POST", "OPTIONS")
}

// --- Connector config handlers ---

func listBotConnectorsHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configs, err := api.chatStore.ListBotConnectorConfigs(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Add live status from registered connectors
		type connectorStatus struct {
			chathistory.BotConnectorConfig
			Connected bool `json:"connected"`
		}

		var result []connectorStatus
		for _, cfg := range configs {
			cs := connectorStatus{BotConnectorConfig: cfg}
			if api.botManager != nil {
				connector := api.botManager.GetConnector(cfg.ID)
				if connector != nil {
					cs.Connected = connector.IsEnabled()
				}
			}
			result = append(result, cs)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func getBotConnectorHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		platform := mux.Vars(r)["platform"]

		cfg, err := api.chatStore.GetBotConnectorConfig(r.Context(), platform)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	}
}

func saveBotConnectorHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		platform := mux.Vars(r)["platform"]

		var req chathistory.CreateBotConnectorConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		req.ID = platform
		if platform == "slack" && strings.TrimSpace(req.AllowedChannels) != "" && req.AllowedChannels != "[]" && req.AllowedChannels != "{}" {
			var routes map[string]ChannelRoute
			if err := json.Unmarshal([]byte(req.AllowedChannels), &routes); err != nil {
				http.Error(w, "Invalid Slack channel routes", http.StatusBadRequest)
				return
			}
			if err := validateSlackRoutingScopes(routes); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}

		cfg, err := api.chatStore.UpsertBotConnectorConfig(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Printf("[BOT_ROUTES] Saved connector config for %s: enabled=%v bot_mode=%v", platform, cfg.Enabled, cfg.BotMode)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	}
}

func testBotConnectorHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		platform := mux.Vars(r)["platform"]

		if api.botManager == nil {
			http.Error(w, "Bot manager not initialized", http.StatusServiceUnavailable)
			return
		}

		connector := api.botManager.GetConnector(platform)
		if connector == nil {
			http.Error(w, "Connector not found: "+platform, http.StatusNotFound)
			return
		}

		if !connector.IsEnabled() {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Connector is not enabled or not connected",
			})
			return
		}

		// Test by sending a message to the configured channel
		slackSvc := services.GetSlackService()
		if slackSvc != nil && platform == "slack" {
			err := slackSvc.TestConnection(r.Context())
			if err != nil {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Connection test successful",
		})
	}
}
