package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/backgroundtranscript"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Cascade the private execution records before removing the parent conversation.
// Refuse symlink redirects, and share the writers' session lock so queued event
// or terminal callbacks cannot recreate the deleted directory afterwards.
func deleteBackgroundTranscriptHistory(userID, workspacePath, sessionID string) error {
	workspacePath, valid := cleanWorkspaceReadPath(workspacePath, true)
	if !valid || !inspectionIdentityPart(sessionID) {
		return fmt.Errorf("invalid background transcript deletion scope")
	}
	canonical := canonicalChatHistoryWorkspacePath(userID, workspacePath)
	if workspaceref.MustParse(canonical).HasOwner() {
		return fmt.Errorf("background transcript deletion refuses another account's workspace")
	}
	storagePath, _ := ownedWorkProjectWorkspacePath(userID, workspacePath)
	aliases := []string{}
	seen := map[string]bool{}
	for _, alias := range []string{workspacePath, canonical, storagePath} {
		if !seen[alias] {
			aliases = append(aliases, alias)
			seen[alias] = true
		}
	}
	sort.Strings(aliases)
	remove := func() error {
		directory, exists := resolveLocalWorkflowDir(storagePath)
		if !exists {
			return nil
		}
		for _, component := range []string{"builder", "conversation", "background", sessionID} {
			directory = filepath.Join(directory, component)
			info, err := os.Lstat(directory)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("background transcript deletion refuses a symlink redirect")
			}
			if !info.IsDir() {
				return fmt.Errorf("background transcript directory is not a directory")
			}
		}
		return os.RemoveAll(directory)
	}
	// A project can use its logical or account-prefixed physical workspace path.
	// Fence both names without allowing another owner's project to be selected.
	var lockedDelete func(int) error
	lockedDelete = func(index int) error {
		if index == len(aliases) {
			return remove()
		}
		return backgroundtranscript.Delete(aliases[index], sessionID, func() error { return lockedDelete(index + 1) })
	}
	return lockedDelete(0)
}

// Central product history retains its trusted workspace in runtime metadata.
// Plain chats without a workspace have no background transcript storage.
func deleteConversationBackgroundTranscripts(userID, sessionID, conversationPath string) error {
	content, err := os.ReadFile(conversationPath)
	if err != nil {
		return err
	}
	var saved struct {
		UserID        string                   `json:"user_id"`
		SessionID     string                   `json:"session_id"`
		WorkspacePath string                   `json:"workspace_path"`
		Runtime       *ChatHistoryAgentRuntime `json:"runtime"`
	}
	if json.Unmarshal(content, &saved) != nil {
		return nil
	}
	if saved.SessionID != "" && saved.SessionID != sessionID {
		return nil
	}
	if saved.UserID != "" && saved.UserID != userID {
		return nil
	}
	workspace := strings.TrimSpace(saved.WorkspacePath)
	if workspace == "" && saved.Runtime != nil {
		workspace = strings.TrimSpace(saved.Runtime.WorkspacePath)
	}
	if workspace == "" {
		return nil
	}
	return deleteBackgroundTranscriptHistory(userID, workspace, sessionID)
}
