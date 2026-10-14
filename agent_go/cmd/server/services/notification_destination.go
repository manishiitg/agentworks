package services

// NotificationDestination tells a connector where to send a feedback request.
// Each connector field is optional — when set, the connector uses it; when nil,
// the connector falls back to a per-user preference (looked up via UserID),
// then to its workspace-wide default.
//
// A nil *NotificationDestination is equivalent to "no hints, do whatever your
// configured default is" and preserves the pre-routing behavior.
type NotificationDestination struct {
	Slack           *SlackDest           // Slack bot channel/thread hint
	SlackWebhook    *SlackWebhookDest    // workflow-scoped one-way Incoming Webhook
	WhatsApp        *WhatsAppDest        // WhatsApp recipient hint
	Gmail           *GmailDest           // Gmail recipient hint
	UserID          string               // workspace user ID, used to look up per-user preferences
	WorkflowName    string               // workflow identity added to workflow-scoped rich notifications
	WorkspacePath   string               // trusted workflow workspace used by internal durable connectors
	RouteSelections map[string]string    // trusted routing-step ID -> selected top-level route ID
	Content         *NotificationContent // optional typed per-channel content (nil = plain message only)

	// ExcludeChannels lists account-level connector names ("gmail", "slack",
	// "whatsapp") to skip for this notification. Sourced from the workflow's
	// workflow.json notifications.exclude_channels so a per-workflow preference
	// suppresses an inherited account-level channel without touching the
	// account-wide configuration. Matched case-insensitively.
	ExcludeChannels []string

	// Summary-specific allowlists. Empty means inherit the existing workflow
	// routing. workflow summary delivery selects one by notification_kind and enforces it in
	// the backend, including the workflow Slack webhook.
	RunSummaryChannels   []string
	PulseSummaryChannels []string

	// Summary-specific Slack Incoming Webhooks, selected by the same
	// notification_kind. Each webhook is a distinct channel. Empty means fall
	// back to the single SlackWebhook above.
	RunSummaryWebhooks   []SlackWebhookDest
	PulseSummaryWebhooks []SlackWebhookDest

	// Summary-specific Gmail To lists, selected by the same notification_kind.
	// Empty means fall through to the per-user preference and then the
	// account-wide default recipient. Sourced from workflow.json
	// notifications.run_summary_recipients / pulse_summary_recipients.
	// Per-summary senders — the FROM counterpart to the recipient lists below.
	// Several entries fan the summary out, one delivery per account.
	RunSummaryGmailConnectionIDs   []string
	PulseSummaryGmailConnectionIDs []string

	RunSummaryRecipients   []string
	PulseSummaryRecipients []string
}

// SlackWebhookDest is a workflow-scoped, one-way Slack Incoming Webhook.
// SecretName is safe to report in configuration. URL is resolved from the
// encrypted secret store only at run time and must never be logged or persisted.
type SlackWebhookDest struct {
	SecretName string
	URL        string
}

// GmailDest is the Gmail-specific destination hint. Email is the recipient
// address; the sending account is the single shared identity gws is
// authenticated as.
type GmailDest struct {
	Email string

	// ConnectionIDs selects which configured Gmail account(s) send this message.
	// Empty means the workspace default connection. Several entries deliver the
	// same message once per account — the recipient receives one copy from each
	// sender, which is the point of naming more than one.
	//
	// An unknown or disabled ID fails that delivery rather than falling back to
	// another account: sending from an unintended identity is worse than not
	// sending.
	ConnectionIDs []string

	// BlockedRecipients is a per-notification denylist unioned with the
	// account-wide GmailConfig.BlockedRecipients at send time. It lets a
	// per-workflow notification preference (workflow.json capabilities.notifications,
	// applied by the backend) exclude additional recipients for this workflow
	// without editing the account-wide config. Never widens the allow-set — it
	// can only block more, never unblock a globally-blocked address. Blocked
	// addresses are dropped from the recipient list, not grounds for canceling
	// delivery to the recipients that are allowed.
	BlockedRecipients []string
}

// SlackDest is the Slack-specific destination hint. ThreadTS is optional —
// when set, the message is posted as a reply in that thread instead of a
// top-level post in the channel. ConnectionID selects which configured Slack
// app identity sends; empty means the default connection. An unknown or
// disabled ID fails the send rather than falling back to another identity.
type SlackDest struct {
	ChannelID    string
	ThreadTS     string
	ConnectionID string
}

// WhatsAppDest is the WhatsApp-specific destination hint. ChannelID is a
// WhatsApp JID (e.g. "919000000000@s.whatsapp.net" or a group JID) used when
// replying to an active bot chat. PhoneE164 is the recipient phone number in
// E.164 format (e.g. "+919000000000").
type WhatsAppDest struct {
	ChannelID string
	PhoneE164 string
}
