// Package chathistory persists the non-chat operator state that outlives a
// server restart: bot connector configs and per-user encrypted secrets. Chat
// sessions, events, and bot conversations are all in-memory and die with the
// process — none of that lives here anymore.
//
// Layout on disk (under the workspace-docs root):
//
//	_users/<userID>/secrets.json                           per-user encrypted secret blobs
//	_users/<userID>/workflow_secrets/<workflow-hash>.json   per-workflow encrypted secret blobs
//	config/bot-connectors.json                             operator-level bot connector config
//
// The package name is historical; what remains is an operator-config store
// rather than chat history.
package chathistory

import (
	"context"
	"errors"
	"time"
)

// Bot session status constants. Bot conversations run in-memory only, but
// their status strings are shared across the bot connector manager and the
// routes that expose them.
const (
	BotSessionStatusAwaitingPlanApproval = "awaiting_plan_approval"
	BotSessionStatusRunning              = "running"
	BotSessionStatusCompleted            = "completed"
	BotSessionStatusFailed               = "failed"
)

// BotConnectorConfig represents configuration for a bot connector platform.
// ErrBotConnectorConfigNotFound means no connector config is saved under the
// requested ID (e.g. no shared Slack bot, only workflow-owned Slack apps).
var ErrBotConnectorConfigNotFound = errors.New("bot connector config not found")

type BotConnectorConfig struct {
	ID              string    `json:"id"`
	Enabled         bool      `json:"enabled"`
	BotMode         bool      `json:"bot_mode"`
	ConfigJSON      string    `json:"config_json"`
	DefaultPresetID string    `json:"default_preset_id"`
	AutoConfirm     bool      `json:"auto_confirm"`
	AllowedChannels string    `json:"allowed_channels"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateBotConnectorConfigRequest for creating/updating a bot connector config.
type CreateBotConnectorConfigRequest struct {
	ID              string `json:"id"`
	Enabled         bool   `json:"enabled"`
	BotMode         bool   `json:"bot_mode"`
	ConfigJSON      string `json:"config_json,omitempty"`
	DefaultPresetID string `json:"default_preset_id,omitempty"`
	AutoConfirm     bool   `json:"auto_confirm"`
	AllowedChannels string `json:"allowed_channels,omitempty"`
}

// UserSecret represents a user-owned secret blob. The EncryptedValue is
// opaque ciphertext produced by the server layer (AES-GCM + AUTH_SECRET +
// userID AAD); the store just reads and writes it.
type UserSecret struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	WorkflowPath   string    `json:"workflow_path,omitempty"`
	EncryptedValue string    `json:"encrypted_value"`
	VaultName      string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// WorkflowProviderCredential is an encrypted provider-runtime credential
// scoped to one user and workflow. Unlike UserSecret, it is never exposed to
// workflow tools or injected into the step environment.
type WorkflowProviderCredential struct {
	UserID         string    `json:"user_id"`
	WorkflowPath   string    `json:"workflow_path"`
	Provider       string    `json:"provider"`
	EncryptedValue string    `json:"encrypted_value"`
	VaultName      string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Store is the persistence interface for bot connector configs and per-user
// secrets. Everything else (chat sessions, events, bot conversations) is
// in-memory on StreamingAPI / BotConversationManager and has no durable form.
type Store interface {
	// Bot connector configs (operator-level, shared across users)
	UpsertBotConnectorConfig(ctx context.Context, req *CreateBotConnectorConfigRequest) (*BotConnectorConfig, error)
	GetBotConnectorConfig(ctx context.Context, id string) (*BotConnectorConfig, error)
	ListBotConnectorConfigs(ctx context.Context) ([]BotConnectorConfig, error)

	// User secrets (encrypted per-user blobs — ciphertext in, ciphertext out).
	UpsertUserSecret(ctx context.Context, userID, name, encryptedValue string) error
	DeleteUserSecret(ctx context.Context, userID, name string) error
	ListUserSecrets(ctx context.Context, userID string) ([]UserSecret, error)

	// Workflow secrets (encrypted per-user blobs scoped to one workflow path).
	UpsertWorkflowSecret(ctx context.Context, userID, workflowPath, name, encryptedValue string) error
	DeleteWorkflowSecret(ctx context.Context, userID, workflowPath, name string) error
	ListWorkflowSecrets(ctx context.Context, userID, workflowPath string) ([]UserSecret, error)

	// Workflow provider credentials (private runtime auth, never tool secrets).
	UpsertWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider, encryptedValue string) error
	GetWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) (*WorkflowProviderCredential, error)
	DeleteWorkflowProviderCredential(ctx context.Context, userID, workflowPath, provider string) error

	Close() error
}

// BotMetadata carries Slack/bot-specific information for an in-flight bot
// conversation. It's passed around in-memory by BotConversationManager; it's
// never persisted.
type BotMetadata struct {
	Platform    string `json:"platform"`
	ChannelID   string `json:"channel_id"`
	ThreadTS    string `json:"thread_ts"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	UserEmail   string `json:"user_email,omitempty"`
	// DirectMessage: a 1:1 Slack DM whose sender the Slack service mapped
	// to an AgentWorks account; its turns run as that account.
	DirectMessage bool `json:"direct_message,omitempty"`
}
