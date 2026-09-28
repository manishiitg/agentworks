package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

const (
	workflowNotificationStateNotConfigured = "not_configured"
	workflowNotificationStateMissingSecret = "missing_secret"
	workflowNotificationStateInvalidSecret = "invalid_secret"
	workflowNotificationStateReady         = "ready"
)

type WorkflowNotificationDestinationInfo struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Label      string `json:"label"`
	State      string `json:"state"`
	SecretName string `json:"secret_name,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type WorkflowNotificationAccountChannelInfo struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	State            string `json:"state"`
	DefaultRecipient string `json:"default_recipient,omitempty"`
	Summary          string `json:"summary,omitempty"`
	// BlockedRecipients is the account-wide email denylist. Shown alongside the
	// default recipient so the settings UI can state where mail will and will
	// not go without the reader opening a config file.
	BlockedRecipients []string `json:"blocked_recipients,omitempty"`
	// Checking reports that Gmail authorization is still being resolved in the
	// background; the row renders immediately and settles when it lands.
	Checking bool `json:"checking,omitempty"`

	// DefaultSender is the account-level fallback FROM address. A workflow may
	// select a different Notify sender. Empty when no default connection exists
	// or while legacy authentication is still resolving.
	DefaultSender string `json:"default_sender,omitempty"`
	// DefaultSenderConnectionID names the connection DefaultSender came from,
	// so the UI can say which configured account is inherited by default.
	DefaultSenderConnectionID string `json:"default_sender_connection_id,omitempty"`
	// SenderChoices lists the connections available to the Notify sender picker.
	SenderChoices []WorkflowNotificationSenderChoice `json:"sender_choices,omitempty"`
}

// WorkflowNotificationSenderChoice is one selectable sending account.
// Identifiers and labels only — never credential material or file paths.
type WorkflowNotificationSenderChoice struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Ready       bool   `json:"ready"`
	IsDefault   bool   `json:"is_default,omitempty"`
}

type WorkflowNotificationInfoResponse struct {
	Success         bool                                     `json:"success"`
	Agentic         bool                                     `json:"agentic"`
	ScopeLabel      string                                   `json:"scope_label"`
	WorkflowLabel   string                                   `json:"workflow_label,omitempty"`
	EffectiveState  string                                   `json:"effective_state"`
	Destinations    []WorkflowNotificationDestinationInfo    `json:"destinations"`
	AccountChannels []WorkflowNotificationAccountChannelInfo `json:"account_channels"`
	// Per-workflow preferences from workflow.json notifications. ExcludeChannels
	// lists inherited account-level channels this workflow opts out of;
	// BlockRecipients is the workflow's own email denylist (added to the
	// account-wide one), and both are display-only here — edits go through
	// /notify. The Run/PulseSummaryRecipients lists say where mail is sent.
	RunSummaryInstructions   string   `json:"run_summary_instructions,omitempty"`
	PulseSummaryInstructions string   `json:"pulse_summary_instructions,omitempty"`
	RunSummaryChannels       []string `json:"run_summary_channels,omitempty"`
	PulseSummaryChannels     []string `json:"pulse_summary_channels,omitempty"`
	RunSummaryRecipients     []string `json:"run_summary_recipients,omitempty"`
	PulseSummaryRecipients   []string `json:"pulse_summary_recipients,omitempty"`
	// Slack channels per summary, as webhook secret names (one webhook = one
	// channel). Display-only — edits go through /notify.
	RunSummarySlackWebhooks   []string `json:"run_summary_slack_webhooks,omitempty"`
	PulseSummarySlackWebhooks []string `json:"pulse_summary_slack_webhooks,omitempty"`
	ExcludeChannels           []string `json:"exclude_channels,omitempty"`
	BlockRecipients           []string `json:"block_recipients,omitempty"`
	// GmailConnectionID is which Gmail account sends this workflow's mail.
	// Empty means the account-level default connection is inherited.
	GmailConnectionID string `json:"gmail_connection_id,omitempty"`
	// Per-summary senders. Empty means the summary falls back to
	// GmailConnectionID, then to the account default connection.
	RunSummaryGmailConnectionIDs   []string `json:"run_summary_gmail_connection_ids,omitempty"`
	PulseSummaryGmailConnectionIDs []string `json:"pulse_summary_gmail_connection_ids,omitempty"`
}

func resolveSlackNotificationState(id, label string, capabilities WorkflowCapabilities, secretValue string, secretResolved bool) WorkflowNotificationDestinationInfo {
	destination := WorkflowNotificationDestinationInfo{
		ID:    id,
		Type:  "slack_webhook",
		Label: label,
		State: workflowNotificationStateNotConfigured,
	}
	if capabilities.Notifications == nil {
		destination.Summary = "No Slack webhook selected for this scope."
		return destination
	}

	secretName := strings.TrimSpace(capabilities.Notifications.SlackWebhookSecretName)
	destination.SecretName = secretName
	if secretName == "" {
		destination.Summary = "No Slack webhook selected for this scope."
		return destination
	}

	if !secretResolved || strings.TrimSpace(secretValue) == "" {
		destination.State = workflowNotificationStateMissingSecret
		destination.Summary = "The encrypted notification secret is missing."
		return destination
	}
	if err := services.ValidateSlackIncomingWebhookURL(secretValue); err != nil {
		destination.State = workflowNotificationStateInvalidSecret
		destination.Summary = "The encrypted secret is not a valid official Slack Incoming Webhook URL."
		return destination
	}

	destination.State = workflowNotificationStateReady
	destination.Summary = "notify_user calls are delivered here automatically by the backend."
	return destination
}

func resolveWorkflowSlackNotificationState(manifest *WorkflowManifest, secretValue string, secretResolved bool) WorkflowNotificationDestinationInfo {
	if manifest == nil {
		return resolveSlackNotificationState("workflow-slack-webhook", "Workflow Slack webhook", WorkflowCapabilities{}, secretValue, secretResolved)
	}
	return resolveSlackNotificationState("workflow-slack-webhook", "Workflow Slack webhook", manifest.Capabilities, secretValue, secretResolved)
}

func notificationAccountChannels(ctx context.Context, selectedSenderID string) []WorkflowNotificationAccountChannelInfo {
	accountChannels := []WorkflowNotificationAccountChannelInfo{}
	if gmail, gmailErr := ensureGmailService(); gmailErr == nil {
		config := gmail.GetConfig()
		// Never block this handler on `gws auth status` (~5.5s, a Node CLI). The
		// popup needs the recipients and channel config, all already in memory;
		// only the auth badge depends on gws, so only it waits.
		auth := gmail.EffectiveAuthStatusCached()
		gmailState := "not_ready"
		gmailSummary := gmailNotReadyReason(config, auth)
		switch {
		case auth.Checking:
			gmailState = "checking"
			gmailSummary = "Checking Gmail authorization…"
		// Same definition as the Bots panel (user decision 2026-09-03): a
		// signed-in account with the Gmail send scope is Connected. Whether a
		// default recipient exists is a recipients question, shown beside the
		// "to" address, not a reason to call the channel not ready.
		case config.Enabled && auth.Authenticated && auth.HasGmailScope:
			gmailState = "ready"
			gmailSummary = "Connected with the Gmail send scope."
		}
		// Which account sends. With no connections configured this is the
		// legacy singleton, whose address comes from the same cached auth the
		// badge above already used — so naming the sender costs nothing extra.
		defaultSender := ""
		if len(config.Connections) == 0 {
			defaultSender = auth.Email
		}
		defaultSenderID := config.DefaultConnectionID
		choices := []WorkflowNotificationSenderChoice{}
		selectedFound := false
		selectedReady := false
		selectedChecking := false
		for _, conn := range gmail.ConnectionsUsableFrom(services.GmailUseScope{}) {
			connAuth, _ := gmail.AuthStatusForConnection(conn.ID)
			email := conn.Email
			if connAuth.Email != "" {
				email = connAuth.Email
			}
			isDefault := conn.ID == config.DefaultConnectionID
			if isDefault && email != "" {
				defaultSender = email
			}
			if conn.ID == selectedSenderID {
				selectedFound = true
				selectedChecking = connAuth.Checking
				selectedReady = conn.Enabled && connAuth.Authenticated && connAuth.HasGmailScope
			}
			choices = append(choices, WorkflowNotificationSenderChoice{
				ID:          conn.ID,
				DisplayName: conn.DisplayName,
				Email:       email,
				Ready:       conn.Enabled && connAuth.Authenticated && connAuth.HasGmailScope,
				IsDefault:   isDefault,
			})
		}
		if selectedSenderID != "" {
			switch {
			case !selectedFound:
				gmailState, gmailSummary = "not_ready", "Selected Notify sender is missing."
			case selectedChecking:
				gmailState, gmailSummary = "checking", "Checking the selected Notify sender…"
			case config.Enabled && selectedReady:
				gmailState, gmailSummary = "ready", "Selected Notify sender is connected."
			default:
				gmailState, gmailSummary = "not_ready", "Selected Notify sender is unavailable."
			}
		}

		accountChannels = append(accountChannels, WorkflowNotificationAccountChannelInfo{
			ID:                        "gmail",
			Label:                     "Gmail account channel",
			State:                     gmailState,
			DefaultRecipient:          config.DefaultTo,
			BlockedRecipients:         config.BlockedRecipients,
			Summary:                   gmailSummary,
			Checking:                  auth.Checking,
			DefaultSender:             defaultSender,
			DefaultSenderConnectionID: defaultSenderID,
			SenderChoices:             choices,
		})
	}
	return accountChannels
}

// effectiveNotificationState reports whether notify_user has at least one
// usable delivery path for this scope. Account-level channels such as Gmail
// are inherited automatically; a workflow does not need a redundant Gmail
// field in workflow.json. A broken explicitly configured Slack destination
// still wins so the UI does not hide a configuration problem merely because
// an inherited fallback is available.
func effectiveNotificationState(scopeDestination WorkflowNotificationDestinationInfo, accountChannels []WorkflowNotificationAccountChannelInfo) string {
	switch scopeDestination.State {
	case workflowNotificationStateMissingSecret, workflowNotificationStateInvalidSecret:
		return scopeDestination.State
	case workflowNotificationStateReady:
		return workflowNotificationStateReady
	}
	for _, channel := range accountChannels {
		if strings.EqualFold(strings.TrimSpace(channel.State), workflowNotificationStateReady) {
			return workflowNotificationStateReady
		}
	}
	return workflowNotificationStateNotConfigured
}

func (api *StreamingAPI) handleGetWorkflowNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}

	manifest, found, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	secretName := ""
	if manifest.Capabilities.Notifications != nil {
		secretName = strings.TrimSpace(manifest.Capabilities.Notifications.SlackWebhookSecretName)
	}
	secretValue := ""
	secretResolved := false
	if secretName != "" {
		userID := GetUserIDFromContext(r.Context())
		secretValue, secretResolved = api.resolveBackendNotificationSecret(r.Context(), userID, workspacePath, secretName)
	}
	slack := resolveWorkflowSlackNotificationState(manifest, secretValue, secretResolved)
	selectedSenderID := ""
	if manifest.Capabilities.Notifications != nil {
		selectedSenderID = strings.TrimSpace(manifest.Capabilities.Notifications.GmailConnectionID)
	}
	accountChannels := notificationAccountChannels(r.Context(), selectedSenderID)

	response := WorkflowNotificationInfoResponse{
		Success:                  true,
		Agentic:                  true,
		ScopeLabel:               manifest.Label,
		WorkflowLabel:            manifest.Label,
		EffectiveState:           effectiveNotificationState(slack, accountChannels),
		Destinations:             []WorkflowNotificationDestinationInfo{slack},
		AccountChannels:          accountChannels,
		RunSummaryInstructions:   runSummaryInstructionsOrDefault(manifest.Capabilities.Notifications),
		PulseSummaryInstructions: pulseSummaryInstructionsOrDefault(manifest.Capabilities.Notifications),
	}
	if manifest.Capabilities.Notifications != nil {
		response.RunSummaryChannels = manifest.Capabilities.Notifications.RunSummaryChannels
		response.PulseSummaryChannels = manifest.Capabilities.Notifications.PulseSummaryChannels
		response.RunSummaryRecipients = manifest.Capabilities.Notifications.RunSummaryRecipients
		response.PulseSummaryRecipients = manifest.Capabilities.Notifications.PulseSummaryRecipients
		response.RunSummarySlackWebhooks = manifest.Capabilities.Notifications.RunSummarySlackWebhookSecretNames
		response.PulseSummarySlackWebhooks = manifest.Capabilities.Notifications.PulseSummarySlackWebhookSecretNames
		response.ExcludeChannels = manifest.Capabilities.Notifications.ExcludeChannels
		response.BlockRecipients = manifest.Capabilities.Notifications.BlockRecipients
		response.GmailConnectionID = manifest.Capabilities.Notifications.GmailConnectionID
		response.RunSummaryGmailConnectionIDs = manifest.Capabilities.Notifications.RunSummaryGmailConnectionIDs
		response.PulseSummaryGmailConnectionIDs = manifest.Capabilities.Notifications.PulseSummaryGmailConnectionIDs
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// gmailNotReadyReason names the first unmet condition for the Gmail channel
// instead of a blanket "not ready": the Notify panel showed a connected,
// authenticated account as "Not ready" with no way to tell it was only
// missing a default recipient (user, 2026-09-03).
func gmailNotReadyReason(config *services.GmailConfig, auth services.GmailAuthStatus) string {
	switch {
	case !auth.GwsInstalled:
		return "gws is not installed on the server."
	case !auth.Authenticated:
		return "No Gmail account is signed in."
	case !auth.HasGmailScope:
		return "The signed-in account has no Gmail send scope."
	case config == nil || !config.Enabled:
		return "The Gmail channel is switched off."
	}
	return "Gmail is not ready at account level."
}
