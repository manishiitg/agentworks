package server

import (
	"net/url"
	"os"
	"strings"
)

// Safe, deployment-specific instructions shared by the pane and Builder.
// OAuth credentials and service account keys never belong in this response.
func gmailInboundAdminSetup(config gmailInboundConfig) map[string]interface{} {
	endpoint := ""
	if u, err := url.Parse(config.Audience); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == gmailInboundEventPath {
		endpoint = config.Audience
	}
	if endpoint == "" {
		base := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/")
		u, err := url.Parse(base)
		if err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
			endpoint = base + gmailInboundEventPath
		}
	}
	return map[string]interface{}{
		"required_access":       "Google Cloud project setup permissions and access to the deployment's server environment; app administrator access alone is insufficient.",
		"explanation":           "Google sign-in connects an account. Automatic incoming email also needs Google Pub/Sub, which delivers mailbox change events to this server. Saved filters do not enable delivery.",
		"push_endpoint":         endpoint,
		"environment_variables": []string{"GMAIL_INBOUND_TOPICS", "GMAIL_INBOUND_AUDIENCE", "GMAIL_INBOUND_PUSH_EMAIL"},
		"steps": []string{
			"Use the Google Cloud project that owns the existing OAuth client; enable the Gmail and Pub/Sub APIs.",
			"Create a Pub/Sub topic and grant gmail-api-push@system.gserviceaccount.com the Pub/Sub Publisher role on that topic.",
			"Create an authenticated push subscription to push_endpoint, with the same URL as its audience. Select a push service account and grant the Pub/Sub service agent permission to create tokens for it. Keep the default wrapped JSON payload.",
			"Set GMAIL_INBOUND_TOPICS to a JSON map of this app's exact OAuth client name to the full projects/PROJECT_ID/topics/TOPIC name. Set GMAIL_INBOUND_AUDIENCE to push_endpoint and GMAIL_INBOUND_PUSH_EMAIL to the push service account email. Restart the backend service.",
			"Verify configured=true and a nonempty setup.oauth_clients list, then ask Builder to connect the mailbox with Gmail read consent and configure its trigger. Verify watch_ready=true before sending a test email.",
		},
		"empty_client_list": "An empty oauth_clients list means no registered OAuth client is mapped to an inbound topic; it does not by itself mean Google sign-in is missing.",
		"local_setup":       "Local development also needs a public HTTPS tunnel to this backend. Use the tunnel's Gmail event URL for both push endpoint and audience; localhost cannot receive Google's push requests.",
		"documentation_url": "https://developers.google.com/workspace/gmail/api/guides/push",
	}
}
