package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type SlackConnectionCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type SlackConnectionTestResult struct {
	Success bool                   `json:"success"`
	Message string                 `json:"message"`
	Checks  []SlackConnectionCheck `json:"checks,omitempty"`
}

// DiagnoseConnection is shared by the settings UI and Builder tool. No test
// message is posted, and no credentials or WebSocket URLs are returned.
// It diagnoses this instance's effective connection (the default on root).
func (s *SlackService) DiagnoseConnection(ctx context.Context) SlackConnectionTestResult {
	if err := s.ReloadConfig(ctx); err != nil {
		return SlackConnectionTestResult{Message: "Failed to load Slack configuration"}
	}
	return s.DiagnoseConnectionWithConfig(ctx, s.probeConfig())
}

// DiagnoseConnectionFor diagnoses one named registry entry. Root only.
func (s *SlackService) DiagnoseConnectionFor(ctx context.Context, connID string) SlackConnectionTestResult {
	curCfg := s.slackConfig()
	if s.isChildService() {
		return SlackConnectionTestResult{Message: "Diagnose through the root Slack service"}
	}
	if err := s.ReloadConfig(ctx); err != nil {
		return SlackConnectionTestResult{Message: "Failed to load Slack configuration"}
	}
	conn, ok := effectiveSlackConnection(curCfg, connID)
	if !ok {
		return SlackConnectionTestResult{Message: "Slack connection is unknown or has no tokens"}
	}
	return s.DiagnoseConnectionWithConfig(ctx, &SlackConfig{Enabled: conn.Enabled, BotToken: conn.BotToken, AppToken: conn.AppToken})
}

func (s *SlackService) DiagnoseConnectionWithConfig(ctx context.Context, config *SlackConfig) SlackConnectionTestResult {
	result := SlackConnectionTestResult{Success: true}
	add := func(name, status, message string) {
		result.Checks = append(result.Checks, SlackConnectionCheck{Name: name, Status: status, Message: message})
	}
	if config == nil || !config.Enabled {
		return SlackConnectionTestResult{Message: "Enable the Slack bot before testing"}
	}
	if config.BotToken == "" || config.AppToken == "" {
		return SlackConnectionTestResult{Message: "Both bot and app tokens are required"}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	scopes, err := testSlackBotScopes(ctx, config.BotToken)
	if err != nil {
		result.Success = false
		add("Bot token", "failed", err.Error())
	} else {
		add("Bot token", "passed", "Authenticated with Slack")
		if scopes == nil {
			add("Bot permissions", "manual", "Slack did not return granted scopes. Verify app_mentions:read, chat:write, reactions:write, channels:history, groups:history, channels:read, groups:read, users:read, and users:read.email under OAuth & Permissions.")
		} else {
			for _, scope := range []string{"app_mentions:read", "chat:write", "reactions:write", "channels:history", "groups:history", "channels:read", "groups:read", "users:read", "users:read.email"} {
				if scopes[scope] {
					add(scope, "passed", "Permission granted to the installed bot token")
				} else {
					result.Success = false
					add(scope, "missing", "Add this bot scope under OAuth & Permissions, then reinstall the Slack app.")
				}
			}
			// Channel bots work without these; a 1:1 DM with the bot needs them.
			for _, scope := range []string{"im:history", "im:read"} {
				if scopes[scope] {
					add(scope, "passed", "Permission granted for direct messages with the bot")
				} else {
					add(scope, "manual", "Needed only for 1:1 direct messages with the bot: add this scope, subscribe to message.im, turn on App Home → Messages Tab, then reinstall the app.")
				}
			}
			for _, optional := range []struct{ scope, feature string }{
				{"files:read", "reading incoming attachments"},
				{"chat:write.public", "posting to public channels without joining them"},
			} {
				if scopes[optional.scope] {
					add(optional.scope, "passed", "Optional permission granted for "+optional.feature)
				} else {
					add(optional.scope, "manual", "Optional: add this scope and reinstall only if you need "+optional.feature+".")
				}
			}
		}
	}
	if err := testAppToken(ctx, config.AppToken); err != nil {
		result.Success = false
		add("Socket Mode token", "failed", err.Error()+" In the same Slack app, open Socket Mode and turn on Enable Socket Mode. Then refresh Event Subscriptions; leave Request URL empty.")
	} else {
		add("Socket Mode token", "passed", "App token can open a Socket Mode connection")
	}
	add("Event subscriptions", "manual", "Required for full delivery: in the same Slack app, first turn on Enable Socket Mode, then refresh Event Subscriptions, enable events, add app_mention, message.channels (public channel replies), and message.groups (private channel replies) under Subscribe to bot events, and click Save Changes. For 1:1 direct messages with the bot, also add message.im and turn on App Home → Messages Tab (tick 'Allow users to send Slash commands and messages from the messages tab'). Leave Request URL empty: Socket Mode does not need one. If Save Changes is disabled and a Request URL is shown, check that Socket Mode is enabled in this app. Bot/app tokens cannot read these settings.")
	add("Mention delivery", "manual", "Required delivery verification: invite the bot to the channel, send an @mention, then reply in its thread without mentioning the bot. Verify both messages receive replies; app_mention alone does not deliver ordinary thread replies.")
	if result.Success {
		result.Message = "Token checks passed. Setup is incomplete until required event subscriptions, an @mention, and a thread reply are verified."
		if scopes == nil {
			result.Message = "Tokens verified. Bot permissions and event subscriptions need manual verification."
		}
	} else {
		var missing []string
		for _, check := range result.Checks {
			if check.Status == "missing" {
				missing = append(missing, check.Name)
			}
		}
		if len(missing) > 0 {
			result.Message = "Missing bot permissions: " + strings.Join(missing, ", ") + ". Add these scopes under OAuth & Permissions and reinstall the Slack app."
		} else {
			for _, check := range result.Checks {
				if check.Status == "failed" {
					result.Message = check.Name + ": " + check.Message
					break
				}
			}
		}
	}
	if !result.Success {
		// Server-side record of why a connection test failed. The messages
		// carry Slack's own error codes (invalid_auth, missing scopes) and
		// never credentials, so the next "why is this failing" starts here
		// instead of in the browser.
		for _, check := range result.Checks {
			if check.Status == "failed" || check.Status == "missing" {
				log.Printf("[SLACK] connection test %s: %s", check.Name, check.Message)
			}
		}
	}
	return result
}

// x-oauth-scopes describes the scopes actually granted to this installed token,
// rather than the scopes requested in an app manifest before reinstallation.
func testSlackBotScopes(ctx context.Context, token string) (map[string]bool, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "xoxb-") {
		return nil, fmt.Errorf("Bot token must start with xoxb-")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/auth.test", nil)
	if err != nil {
		return nil, fmt.Errorf("Could not build bot token check")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Could not reach Slack to validate the bot token")
	}
	defer response.Body.Close()
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("Unexpected Slack authentication response")
	}
	if !payload.OK {
		return nil, fmt.Errorf("Bot token validation failed: %s", payload.Error)
	}
	header, found := response.Header[http.CanonicalHeaderKey("X-OAuth-Scopes")]
	if !found {
		return nil, nil
	}
	scopes := map[string]bool{}
	for _, part := range strings.Split(strings.Join(header, ","), ",") {
		if scope := strings.TrimSpace(part); scope != "" {
			scopes[scope] = true
		}
	}
	return scopes, nil
}
