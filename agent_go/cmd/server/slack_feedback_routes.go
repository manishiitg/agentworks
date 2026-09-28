package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"

	"github.com/gorilla/mux"
)

// ChannelRoute maps a Slack channel ID to a specific workflow.
type ChannelRoute = services.ChannelRoute

// SlackConfigRequest represents a request to update Slack config (Socket Mode only)
type SlackConfigRequest struct {
	Enabled        bool                    `json:"enabled"`
	BotToken       string                  `json:"bot_token"` // Bot User OAuth Token (xoxb-...)
	AppToken       string                  `json:"app_token"` // App-level token (xapp-...) for Socket Mode
	BotMode        bool                    `json:"bot_mode"`  // Enable @mention bot mode (starts agent sessions from Slack)
	ChannelRouting map[string]ChannelRoute `json:"channel_routing,omitempty"`
}

// SlackConfigResponse represents the Slack configuration response.
// The top-level credential fields mirror the default connection; the full
// masked registry is in Connections.
type SlackConfigResponse struct {
	Enabled              bool                      `json:"enabled"`
	BotToken             string                    `json:"bot_token,omitempty"` // Masked in GET
	AppToken             string                    `json:"app_token,omitempty"` // Masked in GET
	BotMode              bool                      `json:"bot_mode"`
	ChannelRouting       map[string]ChannelRoute   `json:"channel_routing,omitempty"`
	Connections          []SlackConnectionResponse `json:"connections,omitempty"`
	DefaultConnectionID  string                    `json:"default_connection_id,omitempty"`
	ManageDefaultAllowed bool                      `json:"manage_default_allowed"`
}

// SlackTestResponse represents test connection response
type SlackTestResponse struct {
	Success bool                            `json:"success"`
	Message string                          `json:"message"`
	Checks  []services.SlackConnectionCheck `json:"checks,omitempty"`
	TestID  string                          `json:"test_id,omitempty"` // Unique ID for polling test replies
}

// Serializes route read/validate/write transactions shared by the UI and tools.
var slackRouteMutationMu sync.Mutex

var slackChannelIDPattern = regexp.MustCompile(`^[CDG][A-Z0-9]{2,}$`)

func normalizeSlackChannelRouting(routes map[string]ChannelRoute) (map[string]ChannelRoute, error) {
	if routes == nil {
		return nil, nil
	}
	out := make(map[string]ChannelRoute, len(routes))
	for rawChannelID, route := range routes {
		channelID := strings.ToUpper(strings.TrimSpace(rawChannelID))
		route.WorkflowID = strings.TrimSpace(route.WorkflowID)
		route.WorkspacePath = strings.TrimSpace(route.WorkspacePath)
		route.ProfileID = strings.TrimSpace(route.ProfileID)
		route.ConversationKey = strings.TrimSpace(route.ConversationKey)
		route.ProfileLabel = strings.TrimSpace(route.ProfileLabel)
		route.WorkspaceUserID = strings.TrimSpace(route.WorkspaceUserID)
		hasWorkflowRoute := route.WorkflowID != "" && route.WorkspacePath != ""
		hasProfileRoute := route.ProfileID != "" && route.ConversationKey != "" && route.WorkspacePath != ""
		if channelID == "" || hasWorkflowRoute == hasProfileRoute || (route.WorkflowID != "" && route.ProfileID != "") {
			return nil, fmt.Errorf("Slack route %q requires exactly one complete workflow or profile destination", rawChannelID)
		}
		if strings.TrimSpace(route.WorkshopMode) != "" && services.NormalizeBotWorkshopMode(route.WorkshopMode) == "" {
			return nil, fmt.Errorf("Slack route %q has an unknown workshop_mode; use run or workshop", rawChannelID)
		}
		seenEmails := map[string]bool{}
		emails := []string{}
		for _, email := range route.BlockedEmails {
			email = strings.ToLower(strings.TrimSpace(email))
			parsed, err := mail.ParseAddress(email)
			if err != nil || parsed.Address != email {
				return nil, fmt.Errorf("invalid blocked email on channel %s", channelID)
			}
			if !seenEmails[email] {
				emails = append(emails, email)
				seenEmails[email] = true
			}
		}
		if route.BlockedEmails != nil {
			route.BlockedEmails = emails
		}
		if route.BotGrant != "" && route.BotGrant != "run" && route.BotGrant != "owner" {
			return nil, fmt.Errorf("Slack route %q requires a run or owner bot_grant", rawChannelID)
		}
		if !slackChannelIDPattern.MatchString(channelID) {
			return nil, fmt.Errorf("Slack route key %q is not a channel ID; use the channel ID from Slack, for example C1234567890", rawChannelID)
		}
		if existing, ok := out[channelID]; ok && !sameSlackRouteDestination(existing, route) {
			return nil, fmt.Errorf("Slack channel %s is already routed", channelID)
		}
		route.BotGrant = "run"
		route.WorkshopMode = "run"
		route.SendFullDetails = true
		out[channelID] = route
	}
	return out, nil
}

func sameSlackRouteDestination(a, b ChannelRoute) bool {
	return strings.EqualFold(strings.TrimSpace(a.WorkflowID), strings.TrimSpace(b.WorkflowID)) &&
		services.SameSlackScopePath(a.WorkspacePath, b.WorkspacePath) &&
		strings.EqualFold(strings.TrimSpace(a.ProfileID), strings.TrimSpace(b.ProfileID)) &&
		strings.EqualFold(strings.TrimSpace(a.ConversationKey), strings.TrimSpace(b.ConversationKey))
}

func validateSlackRouteMutationPermissions(ctx context.Context, api *StreamingAPI, next, current map[string]ChannelRoute) error {
	checked := map[string]bool{}
	for channelID, route := range next {
		if !slackRouteHasDestination(route) {
			continue
		}
		old, existed := current[channelID]
		newGrant := services.NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode)
		oldGrant := services.NormalizeBotRouteGrant(old.BotGrant, old.WorkshopMode)
		needsOwner := !existed || !sameSlackRouteDestination(old, route) || newGrant != oldGrant || !reflect.DeepEqual(old.Trigger, route.Trigger) || !reflect.DeepEqual(old.BlockedEmails, route.BlockedEmails)
		// Resource ownership is server authored, never editable through a route payload.
		route.WorkspaceUserID = old.WorkspaceUserID
		next[channelID] = route
		if existed && !sameSlackRouteDestination(old, route) {
			if _, err := requireSlackRouteDestinationOwner(ctx, api, old); err != nil {
				return err
			}
		}
		if !needsOwner && strings.TrimSpace(route.WorkspaceUserID) == "" {
			route.WorkspaceUserID = strings.TrimSpace(old.WorkspaceUserID)
			next[channelID] = route
		}
		if !needsOwner && (strings.TrimSpace(route.ProfileID) == "" || strings.TrimSpace(route.WorkspaceUserID) != "") {
			continue
		}
		if err := validateSlackTrigger(ctx, route); err != nil {
			return err
		}
		ownerID, err := requireSlackRouteDestinationOwner(ctx, api, route)
		if err != nil {
			return err
		}
		if strings.TrimSpace(route.ProfileID) != "" {
			route.WorkspaceUserID = ownerID
			// Store the crew's physical folder: the crew UI sends the logical
			// path, while its turns run in the physical one and revalidation
			// compares the two.
			route.WorkspacePath = productConversationRuntimeWorkspace(ownerID, route.WorkspacePath)
			next[channelID] = route
		}
		checked[channelID] = true
	}
	for channelID, route := range current {
		if checked[channelID] || !slackRouteHasDestination(route) {
			continue
		}
		if _, stillPresent := next[channelID]; stillPresent {
			continue
		}
		if _, err := requireSlackRouteDestinationOwner(ctx, api, route); err != nil {
			return err
		}
	}
	// Store every crew route in its physical folder (the bot destination
	// invariant, docs/design/bot_destination_scope.md), then refuse anything
	// that still is not.
	for channelID, route := range next {
		if strings.TrimSpace(route.ProfileID) != "" && strings.TrimSpace(route.WorkspaceUserID) != "" {
			route.WorkspacePath = productConversationRuntimeWorkspace(route.WorkspaceUserID, route.WorkspacePath)
			next[channelID] = route
		}
	}
	return validateSlackRoutingScopes(next)
}

// validateSlackRoutingScopes refuses channel routes whose crew destination is
// not physical.
func validateSlackRoutingScopes(routes map[string]ChannelRoute) error {
	for channelID, route := range routes {
		if err := services.ValidateBotScope(route.WorkspacePath, route.ProfileID); err != nil {
			return fmt.Errorf("Slack route for %s: %w", channelID, err)
		}
	}
	return nil
}

func slackRouteHasDestination(route ChannelRoute) bool {
	return strings.TrimSpace(route.WorkflowID) != "" || strings.TrimSpace(route.ProfileID) != ""
}

func requireSlackRouteDestinationOwner(ctx context.Context, api *StreamingAPI, route ChannelRoute) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID == "" || claims.Provider == "bot_route" || !userAccessForClaims(claims).CanEdit {
		return "", fmt.Errorf("an authenticated interactive owner is required to manage bot grants")
	}
	if strings.TrimSpace(route.WorkflowID) != "" {
		return "", requireSlackRouteWorkflowOwner(ctx, route)
	}
	return requireSlackRouteProfileOwner(ctx, api, route)
}

func requireSlackRouteWorkflowOwner(ctx context.Context, route ChannelRoute) error {
	manifest, exists, err := ReadWorkflowManifest(ctx, strings.TrimSpace(route.WorkspacePath))
	if err != nil {
		return err
	}
	if !exists || manifest == nil {
		return fmt.Errorf("workflow route %s has no manifest; cannot manage Slack bot grant", strings.TrimSpace(route.WorkflowID))
	}
	if !strings.EqualFold(strings.TrimSpace(manifest.ID), strings.TrimSpace(route.WorkflowID)) {
		return fmt.Errorf("Slack route workflow %s does not match manifest %s", strings.TrimSpace(route.WorkflowID), strings.TrimSpace(manifest.ID))
	}
	if manifest.Kind == "relay" {
		return fmt.Errorf("Relay %s does not accept Slack bot routes", manifest.ID)
	}
	if workflowAccessForManifest(GetUserFromContext(ctx), manifest) != WorkflowAccessOwner {
		return fmt.Errorf("only a workflow owner may manage Slack bot grants for %s", manifest.ID)
	}
	return nil
}

func requireSlackRouteProfileOwner(ctx context.Context, api *StreamingAPI, route ChannelRoute) (string, error) {
	profileID := strings.TrimSpace(route.ProfileID)
	if profileID == "" {
		return "", fmt.Errorf("Slack route has no workflow or profile destination")
	}
	if api == nil || api.agentProfiles == nil {
		return "", fmt.Errorf("agent profiles are unavailable; cannot manage Slack bot grant for %s", profileID)
	}
	userID := productWorkspaceUserID(ctx)
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return "", fmt.Errorf("profile route %s is unavailable; cannot manage Slack bot grant: %w", profileID, err)
	}
	if !userAllowedProduct(GetUserFromContext(ctx), profile.Product) {
		return "", fmt.Errorf("only a product owner may manage Slack bot grants for %s", profileID)
	}
	binding, err := resolveProductConversationBinding(ctx, userID, profile, strings.TrimSpace(route.ConversationKey))
	if err != nil {
		return "", fmt.Errorf("profile route %s has no authorized conversation: %w", profileID, err)
	}
	// The crew UI sends the logical path, the binding holds the physical one.
	if !workspacePathsMatchForUser(userID, binding.WorkspacePath, route.WorkspacePath) {
		return "", fmt.Errorf("Slack route profile %s does not match the selected product workspace", profileID)
	}
	return userID, nil
}

// Webhook types removed - using Socket Mode for real-time events

// SlackFeedbackRoutes sets up Slack feedback API routes
func SlackFeedbackRoutes(router *mux.Router, api *StreamingAPI) {
	apiRouter := router.PathPrefix("/api/human-feedback/slack").Subrouter()

	// Configuration routes
	apiRouter.HandleFunc("/config", getSlackConfigHandler(api)).Methods("GET")
	apiRouter.HandleFunc("/config", updateSlackConfigHandler(api)).Methods("POST", "OPTIONS")

	// Test connection
	apiRouter.HandleFunc("/test", testSlackConnectionHandler(api)).Methods("POST", "OPTIONS")

	// Get test connection reply (for polling)
	apiRouter.HandleFunc("/test/reply", getTestConnectionReplyHandler(api)).Methods("GET", "OPTIONS")
	// Note: Using Socket Mode for real-time events - no webhook endpoint needed
}

// ensureSlackService returns the global Slack service, initializing it lazily
// on first use. The service reads its config from the filesystem now, so no
// database handle is required.
func ensureSlackService() (*services.SlackService, error) {
	configureSlackCredentialCodec()

	slackService := services.GetSlackService()
	if slackService != nil {
		return slackService, nil
	}
	svc, err := services.InitSlackService()
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// getSlackConfigHandler retrieves current Slack configuration
func getSlackConfigHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slackService, err := ensureSlackService()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
			return
		}

		config := slackService.GetConfig()

		// Check bot_mode from the filesystem-backed bot connector config.
		botMode := false
		var channelRouting map[string]ChannelRoute
		botCfg, _ := api.chatStore.GetBotConnectorConfig(r.Context(), "slack")
		if botCfg != nil {
			botMode = botCfg.BotMode
			if botCfg.AllowedChannels != "" && botCfg.AllowedChannels != "[]" && botCfg.AllowedChannels != "{}" {
				_ = json.Unmarshal([]byte(botCfg.AllowedChannels), &channelRouting)
				var normalizeErr error
				channelRouting, normalizeErr = normalizeSlackChannelRouting(channelRouting)
				if normalizeErr != nil {
					channelRouting = nil
				}
			}
		}

		resp := SlackConfigResponse{
			Enabled:              config.Enabled,
			BotToken:             config.BotToken,
			AppToken:             config.AppToken,
			BotMode:              botMode,
			ChannelRouting:       channelRouting,
			Connections:          projectSlackConnections(slackService),
			DefaultConnectionID:  slackService.DefaultConnectionID(),
			ManageDefaultAllowed: currentUserIsAdmin(r),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// projectSlackConnections renders the masked registry for the config response.
func projectSlackConnections(svc *services.SlackService) []SlackConnectionResponse {
	if svc == nil {
		return nil
	}
	defaultID := svc.DefaultConnectionID()
	conns := svc.ListConnections()
	out := make([]SlackConnectionResponse, 0, len(conns))
	for _, c := range conns {
		out = append(out, projectSlackConnection(c, defaultID))
	}
	return out
}

// updateSlackConfigHandler creates/updates Slack configuration. The legacy
// credential fields map onto the default connection (admin-only); channel
// routing is owner-scoped per destination workflow. Named connections are
// managed through the connections API.
func updateSlackConfigHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		slackRouteMutationMu.Lock()
		defer slackRouteMutationMu.Unlock()

		var req SlackConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[SLACK] Failed to decode request: %v", err)
			http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
			return
		}

		slackService, err := ensureSlackService()
		if err != nil {
			log.Printf("[SLACK] Failed to initialize service: %v", err)
			http.Error(w, fmt.Sprintf("failed to initialize Slack service: %v", err), http.StatusInternalServerError)
			return
		}

		config := &services.SlackConfig{
			Enabled:  req.Enabled,
			BotToken: req.BotToken,
			AppToken: req.AppToken,
		}

		var existingRouting map[string]ChannelRoute
		if existingBotCfg, _ := api.chatStore.GetBotConnectorConfig(r.Context(), "slack"); existingBotCfg != nil {
			if existingBotCfg.AllowedChannels != "" && existingBotCfg.AllowedChannels != "[]" && existingBotCfg.AllowedChannels != "{}" {
				_ = json.Unmarshal([]byte(existingBotCfg.AllowedChannels), &existingRouting)
				existingRouting, _ = normalizeSlackChannelRouting(existingRouting)
			}
		}

		var normalizeErr error
		req.ChannelRouting, normalizeErr = normalizeSlackChannelRouting(req.ChannelRouting)
		if normalizeErr != nil {
			http.Error(w, normalizeErr.Error(), http.StatusBadRequest)
			return
		}
		if req.ChannelRouting != nil {
			if err := validateSlackRouteMutationPermissions(r.Context(), api, req.ChannelRouting, existingRouting); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
		}
		currentConfig := slackService.GetConfig()
		// Absence is a first-time save, not a failure: the upsert below
		// creates the connector config. Every other lookup in this
		// handler already treats a missing config as nil.
		currentBotConfig, _ := api.chatStore.GetBotConnectorConfig(r.Context(), "slack")
		botMode := currentBotConfig != nil && currentBotConfig.BotMode
		connectorChanged := req.Enabled != currentConfig.Enabled || req.BotMode != botMode || req.BotToken != currentConfig.BotToken || req.AppToken != currentConfig.AppToken
		if connectorChanged {
			claims := GetUserFromContext(r.Context())
			if claims == nil || claims.Provider == "bot_route" || !currentUserIsAdmin(r) {
				http.Error(w, "only an operator may configure Slack credentials or enablement", http.StatusForbidden)
				return
			}

			if err := slackService.SaveConfig(r.Context(), config); err != nil {
				log.Printf("[SLACK] SaveConfig failed: %v", err)
				http.Error(w, fmt.Sprintf("failed to save config: %v", err), http.StatusInternalServerError)
				return
			}

		}

		channelRouting := req.ChannelRouting
		allowedChannelsJSON := ""
		if req.ChannelRouting != nil {
			if data, err := json.Marshal(req.ChannelRouting); err == nil {
				allowedChannelsJSON = string(data)
			}
		} else if existingBotCfg, _ := api.chatStore.GetBotConnectorConfig(r.Context(), "slack"); existingBotCfg != nil {
			allowedChannelsJSON = existingBotCfg.AllowedChannels
			if allowedChannelsJSON != "" && allowedChannelsJSON != "[]" && allowedChannelsJSON != "{}" {
				_ = json.Unmarshal([]byte(allowedChannelsJSON), &channelRouting)
				var existingNormalizeErr error
				channelRouting, existingNormalizeErr = normalizeSlackChannelRouting(channelRouting)
				if existingNormalizeErr != nil {
					http.Error(w, "existing Slack routes are invalid; configure channel IDs on routes", http.StatusBadRequest)
					return
				} else if data, err := json.Marshal(channelRouting); err == nil {
					allowedChannelsJSON = string(data)
				}
			}
		}

		if currentBotConfig == nil {
			currentBotConfig = &chathistory.BotConnectorConfig{}
		}
		// Save bot_mode and Slack channel routing to the filesystem-backed bot connector config.
		if _, err := api.chatStore.UpsertBotConnectorConfig(r.Context(), &chathistory.CreateBotConnectorConfigRequest{
			ID:              "slack",
			Enabled:         req.Enabled,
			BotMode:         req.BotMode,
			ConfigJSON:      currentBotConfig.ConfigJSON,
			DefaultPresetID: currentBotConfig.DefaultPresetID,
			AutoConfirm:     currentBotConfig.AutoConfirm,
			AllowedChannels: allowedChannelsJSON,
		}); err != nil {
			log.Printf("[SLACK] Failed to save bot config: %v", err)
			http.Error(w, "failed to save Slack routes", http.StatusInternalServerError)
			return
		} else {
		}

		// Sessions follow their route, not the platform switch: revoke only
		// sessions whose channel lost its route or changed destination.
		// An empty routing map revokes everything, as before.
		api.revokeChangedBotSessions(channelRouting)
		// Dynamically register the Slack bot connector when the platform
		// switch is on or any owner-saved channel route exists. Route
		// removal takes effect immediately through the per-message and
		// per-route checks; the connector object itself is never
		// unregistered at runtime.
		if api.botManager != nil {
			if (req.BotMode && req.Enabled) || len(channelRouting) > 0 {
				// Register if not already registered
				if api.botManager.GetConnector("slack") == nil {
					api.botManager.RegisterConnector(slackService)
					slackService.StartListening(r.Context())
					log.Printf("[SLACK] Bot mode enabled — registered with bot manager")
				} else {
				}
			} else {
			}
		} else {
		}

		response := SlackConfigResponse{
			Enabled:              config.Enabled,
			BotMode:              req.BotMode,
			ChannelRouting:       channelRouting,
			Connections:          projectSlackConnections(slackService),
			DefaultConnectionID:  slackService.DefaultConnectionID(),
			ManageDefaultAllowed: currentUserIsAdmin(r),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// testSlackConnectionHandler tests Slack connection
// Accepts optional config in request body to test without saving
func testSlackConnectionHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		slackService, err := ensureSlackService()
		if err != nil {
			response := SlackTestResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to initialize Slack service: %v", err),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		// Check if config is provided in request body (for testing without saving)
		var testConfig *SlackConfigRequest
		if r.ContentLength > 0 {
			var req SlackConfigRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
				// Config provided - use it for testing without saving
				testConfig = &req
			} else {
			}
		}

		// If config provided, test with it directly; otherwise use saved config
		var result services.SlackConnectionTestResult
		if testConfig != nil {
			result = slackService.DiagnoseConnectionWithConfig(r.Context(), &services.SlackConfig{Enabled: testConfig.Enabled, BotToken: testConfig.BotToken, AppToken: testConfig.AppToken})
		} else {
			result = slackService.DiagnoseConnection(r.Context())
		}
		response := SlackTestResponse{Success: result.Success, Message: result.Message, Checks: result.Checks}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// getTestConnectionReplyHandler checks if a reply was received for a test connection
func getTestConnectionReplyHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		testUniqueID := r.URL.Query().Get("test_id")
		if testUniqueID == "" {
			http.Error(w, "test_id parameter is required", http.StatusBadRequest)
			return
		}

		feedbackStore := virtualtools.GetHumanFeedbackStore()
		if feedbackStore == nil {
			log.Printf("[SLACK_TEST] ❌ Human feedback store not initialized")
			http.Error(w, "human feedback store not initialized", http.StatusInternalServerError)
			return
		}

		// Check if there's a response for this test connection
		response, exists := feedbackStore.GetResponse(testUniqueID)
		if !exists {
			// Return 204 No Content if no reply yet
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Return the reply
		responseData := map[string]interface{}{
			"test_id":  testUniqueID,
			"reply":    response,
			"received": true,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responseData)
	}
}

// Webhook handler removed - using Socket Mode for real-time events
