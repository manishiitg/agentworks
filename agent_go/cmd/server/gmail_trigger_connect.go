package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

func gmailTriggerOAuthClients(config gmailInboundConfig) []string {
	names := []string{}
	for name := range config.Topics {
		available := services.OAuthClientExists(name)
		if name == services.PlatformGoogleClientName {
			_, _, available = platformGoogleApp()
		}
		if available {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// This prepares Google's consent link through the existing account handlers.
// No token, client secret, credential path or sender identity is agent-selected.
func (api *StreamingAPI) connectGmailTriggerAccount(ctx context.Context, workspace string, config gmailInboundConfig, args map[string]interface{}) (string, error) {
	for key := range args {
		if key != "action" && key != "connection_id" && key != "client_name" && key != "name" {
			return "", fmt.Errorf("%s is not a connect setting; configure routing and filters after Google consent", key)
		}
		if _, ok := args[key].(string); !ok {
			return "", fmt.Errorf("%s must be a string", key)
		}
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("an administrator must set PUBLIC_URL to generate the Google consent link")
	}
	requestURL := base + "/api/human-feedback/gmail/connections?workspace_path=" + url.QueryEscape(workspace)
	req := httptest.NewRequest(http.MethodPost, requestURL, nil).WithContext(ctx)
	scope, err := gmailRequestScope(req, workspace)
	if err != nil {
		return "", err
	}
	if scope.CodeWorkspace == "" && !currentUserIsAdmin(req) {
		return "", fmt.Errorf("an administrator must connect or reconnect shared Gmail accounts; this Code's owner can connect accounts")
	}
	gmailTriggerConfigMu.Lock()
	defer gmailTriggerConfigMu.Unlock()
	svc, err := ensureGmailService()
	if err != nil {
		return "", err
	}
	id, _ := args["connection_id"].(string)
	id = strings.TrimSpace(id)
	clientName, _ := args["client_name"].(string)
	clientName = strings.TrimSpace(clientName)
	if id == "" {
		clients := gmailTriggerOAuthClients(config)
		if clientName == "" {
			if len(clients) == 0 {
				return "", fmt.Errorf("No registered OAuth client is mapped to an inbound topic. Google sign-in may already work; read get_gmail_trigger.setup.provisioning and use setup_gmail_inbound as an interactive administrator to prepare the receiving setup.")
			}
			if len(clients) != 1 {
				return "", fmt.Errorf("choose an exact deployed OAuth client from get_gmail_trigger.setup.oauth_clients: %v", clients)
			}
			clientName = clients[0]
		}
		if config.Topics[clientName] == "" {
			return "", fmt.Errorf("Pub/Sub is not configured for OAuth client %q", clientName)
		}
		name, _ := args["name"].(string)
		name = googleAppDisplayName(name)
		read := true
		var body []byte
		var handler http.HandlerFunc
		if clientName == services.PlatformGoogleClientName {
			body, err = json.Marshal(GoogleAppConnectRequest{WorkspacePath: workspace, DisplayName: name, AllowReadAccess: &read})
			handler = googleAppConnectHandler(api)
		} else {
			body, err = json.Marshal(GmailConnectionRequest{WorkspacePath: workspace, DisplayName: name, ClientName: clientName, AllowReadAccess: &read})
			handler = createGmailConnectionHandler(api)
		}
		if err != nil {
			return "", err
		}
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, requestURL, bytes.NewReader(body)).WithContext(ctx))
		if rec.Code != http.StatusCreated {
			return "", fmt.Errorf("%s", strings.TrimSpace(rec.Body.String()))
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			return "", err
		}
		id = created.ID
	}
	conn, err := svc.ConnectionForScope(id, scope)
	if err != nil {
		return "", fmt.Errorf("Gmail connection unavailable in this target")
	}
	if config.Topics[conn.ClientName] == "" || clientName != "" && clientName != conn.ClientName {
		return "", fmt.Errorf("this account's OAuth client does not match a configured Gmail topic")
	}
	read := true
	if _, err := svc.UpdateConnection(ctx, id, services.GmailConnectionInput{AllowReadAccess: &read}); err != nil {
		return "", err
	}
	rec := httptest.NewRecorder()
	req = mux.SetURLVars(req, map[string]string{"id": id})
	requireGmailConnectionManager(startGmailOAuthHandler(api))(rec, req)
	if rec.Code != http.StatusOK {
		return "", fmt.Errorf("account %s needs consent; could not prepare its link: %s", id, strings.TrimSpace(rec.Body.String()))
	}
	var link GmailOAuthStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		return "", err
	}
	result, err := json.Marshal(map[string]interface{}{
		"connection_id": id, "reconnect_url": link.AuthURL, "redirect_uri": link.RedirectURI,
		"ready": false, "next_step": "Open reconnect_url and complete Google consent. Then ask Builder to inspect list_gmail_connections and configure this target's Gmail trigger. No trigger is enabled by connecting an account.",
	})
	return string(result), err
}
