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
		"push_endpoint":     endpoint,
		"explanation":       "Agent inboxes use one company receiving mailbox. Personal Gmail connections use gog only.",
		"automatic_setup":   "An administrator uses My Vault → Company agent inboxes, or manage_agent_email: connect the company mailbox, prepare and review Google receiving setup, then enable all inboxes.",
		"required_access":   "App administrator and Google Cloud project setup permissions. OAuth JSON identifies the app; connect the company mailbox once with read access.",
		"documentation_url": "https://developers.google.com/workspace/gmail/api/guides/push",
	}
}
