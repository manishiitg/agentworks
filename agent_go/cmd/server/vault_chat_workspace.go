package server

import (
	"context"
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// Vault has a fixed per-user workspace, unlike Crew's existing project root.
// Ensure it through the authenticated workspace API before linking a native CLI
// to it. Repeat calls also repair chats opened before this initialization existed.
func ensureVaultChatWorkspace(ctx context.Context, userID string) error {
	client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(userID))
	if err := client.CreateFolder(ctx, caplayerproduct.WorkspaceRoot); err != nil {
		return fmt.Errorf("initialize Vault chat workspace: %w", err)
	}
	return nil
}
