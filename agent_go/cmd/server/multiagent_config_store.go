package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"time"
)

// MultiAgentChatConfig is the top-level structure for
// _users/{userID}/multiagent-config.json. It persists the user's chosen
// multi-agent CHAT capabilities (skills, MCP servers, secrets, browser mode,
// code-execution) so headless bot-channel sessions (WhatsApp/Slack) start with
// the same setup as the interactive UI, instead of auto-discovering everything.
//
// This is the chat-mode parallel to a workflow's workflow.json capabilities.
// It is intentionally scoped to generic AgentWorks chat configuration.
// (whose Capabilities block governs scheduled runs).
type MultiAgentChatConfig struct {
	Capabilities WorkflowCapabilities `json:"capabilities"`
	UpdatedAt    string               `json:"updated_at,omitempty"`
}

func multiAgentConfigPath(userID string) string {
	return workspaceref.PhysicalPathOf(userID, "multiagent-config.json")
}

// ReadMultiAgentChatConfig reads the chat-capabilities file for a user.
// Returns (cfg, true, nil) if found, (empty-cfg, false, nil) if not found,
// (nil, false, err) on a read/parse error.
func ReadMultiAgentChatConfig(ctx context.Context, userID string) (*MultiAgentChatConfig, bool, error) {
	content, exists, err := readFileFromWorkspace(ctx, multiAgentConfigPath(userID))
	if err != nil {
		return nil, false, fmt.Errorf("failed to read multiagent-config.json for user %s: %w", userID, err)
	}
	if !exists {
		return &MultiAgentChatConfig{Capabilities: WorkflowCapabilities{}}, false, nil
	}

	var c MultiAgentChatConfig
	if err := json.Unmarshal([]byte(content), &c); err != nil {
		return nil, false, fmt.Errorf("failed to parse multiagent-config.json for user %s: %w", userID, err)
	}
	return &c, true, nil
}

// WriteMultiAgentChatConfig persists the user's chat capabilities, stamping
// UpdatedAt at write time.
func WriteMultiAgentChatConfig(ctx context.Context, userID string, caps WorkflowCapabilities) error {
	c := MultiAgentChatConfig{
		Capabilities: caps,
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(&c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal multiagent-config.json: %w", err)
	}
	return writeFileToWorkspace(ctx, multiAgentConfigPath(userID), string(data))
}
