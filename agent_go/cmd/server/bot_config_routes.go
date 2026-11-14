package server

import (
	"encoding/json"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// The "_global" bot connector config is shared by every bot channel: the
// allowed_emails gate that BotConversationManager applies to inbound Slack
// and WhatsApp messages, plus per-provider API keys and default
// servers/skills for bot sessions. These handlers used to live under the
// web-simulator routes; the simulator is gone, the config is not.

func botConfigGetHandler(store chathistory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := store.GetBotConnectorConfig(r.Context(), "_global")
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}

		result := map[string]interface{}{}

		if cfg.ConfigJSON != "" {
			var cfgData map[string]json.RawMessage
			if err := json.Unmarshal([]byte(cfg.ConfigJSON), &cfgData); err == nil {
				if raw, ok := cfgData["default_servers"]; ok {
					var servers []string
					if err := json.Unmarshal(raw, &servers); err == nil {
						result["default_servers"] = servers
					}
				}
				if raw, ok := cfgData["default_skills"]; ok {
					var skills []string
					if err := json.Unmarshal(raw, &skills); err == nil {
						result["default_skills"] = skills
					}
				}
				if raw, ok := cfgData["allowed_emails"]; ok {
					var emails []string
					if err := json.Unmarshal(raw, &emails); err == nil {
						result["allowed_emails"] = emails
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func botConfigSaveHandler(store chathistory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req struct {
			ProviderAPIKeys map[string]string `json:"provider_api_keys,omitempty"`
			DefaultServers  []string          `json:"default_servers,omitempty"`
			DefaultSkills   []string          `json:"default_skills,omitempty"`
			AllowedEmails   []string          `json:"allowed_emails,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Build ConfigJSON: merge all config into one JSON blob
		cfgMap := map[string]interface{}{}

		// Preserve existing config values first
		existingCfg, _ := store.GetBotConnectorConfig(r.Context(), "_global")
		if existingCfg != nil && existingCfg.ConfigJSON != "" {
			json.Unmarshal([]byte(existingCfg.ConfigJSON), &cfgMap)
		}

		// Store per-provider API keys (from frontend) for bot session use
		if len(req.ProviderAPIKeys) > 0 {
			existing, _ := cfgMap["provider_api_keys"].(map[string]interface{})
			if existing == nil {
				existing = map[string]interface{}{}
			}
			for k, v := range req.ProviderAPIKeys {
				existing[k] = v
			}
			cfgMap["provider_api_keys"] = existing
		}
		// Store default servers/skills selections
		if req.DefaultServers != nil {
			cfgMap["default_servers"] = req.DefaultServers
		}
		if req.DefaultSkills != nil {
			cfgMap["default_skills"] = req.DefaultSkills
		}
		// delegation_mode is no longer configurable — multi-agent chat is the only mode
		// for bot-connector sessions. Strip any stale value from previously saved configs.
		delete(cfgMap, "delegation_mode")
		if req.AllowedEmails != nil {
			cfgMap["allowed_emails"] = req.AllowedEmails
		}

		configJSON := ""
		if len(cfgMap) > 0 {
			cfgBytes, _ := json.Marshal(cfgMap)
			configJSON = string(cfgBytes)
		}

		_, err := store.UpsertBotConnectorConfig(r.Context(), &chathistory.CreateBotConnectorConfigRequest{
			ID:         "_global",
			Enabled:    true,
			BotMode:    true,
			ConfigJSON: configJSON,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}
}
