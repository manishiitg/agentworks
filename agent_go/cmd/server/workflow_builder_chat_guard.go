package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

var workflowBuilderChatGuardSessions = struct {
	sync.Mutex
	byWorkflow map[string]map[string]string
}{byWorkflow: make(map[string]map[string]string)}

// protectOtherWorkflowBuilderChats adds hard read/write denies for every
// private Builder transcript location except the current account's directory.
// Builder still receives workflow-wide access for normal workflow authoring;
// these explicit denies are the privacy boundary inside that shared tree.
func protectOtherWorkflowBuilderChats(sessionID, workspacePath, userID string) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if sessionID == "" || workspacePath == "" {
		return
	}

	conversationRoot, resolveErr := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), workspacePath+"/builder/conversation")
	if resolveErr != nil {
		return
	}
	privateUsersRoot := filepath.Join(conversationRoot, "users")
	currentUserSegment := sanitizeUserIDForPath(userID)
	blocked := []string{filepath.ToSlash(filepath.Join(workspacePath, "builder", "conversation", "system"))}
	privatePathFor := func(owner string) string {
		return filepath.ToSlash(filepath.Join(workspacePath, "builder", "conversation", "users", sanitizeUserIDForPath(owner)))
	}

	if entries, err := os.ReadDir(privateUsersRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != currentUserSegment {
				blocked = append(blocked, filepath.ToSlash(filepath.Join(workspacePath, "builder", "conversation", "users", entry.Name())))
			}
		}
	}

	// Until the migration script has moved old date-bucket files, protect each
	// legacy transcript whose embedded owner is not the current account. File
	// granularity lets the current owner continue a legacy chat on the same date.
	if entries, err := os.ReadDir(conversationRoot); err == nil {
		for _, dateEntry := range entries {
			if !dateEntry.IsDir() || dateEntry.Name() == "users" || dateEntry.Name() == "system" {
				continue
			}
			dateDir := filepath.Join(conversationRoot, dateEntry.Name())
			files, readErr := os.ReadDir(dateDir)
			if readErr != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() || !strings.HasPrefix(file.Name(), "session-") || !strings.HasSuffix(file.Name(), "-conversation.json") {
					continue
				}
				data, readErr := os.ReadFile(filepath.Join(dateDir, file.Name()))
				if readErr != nil {
					continue
				}
				var owner struct {
					UserID string `json:"user_id"`
				}
				if json.Unmarshal(data, &owner) != nil || strings.TrimSpace(owner.UserID) != strings.TrimSpace(userID) {
					blocked = append(blocked, filepath.ToSlash(filepath.Join(workspacePath, "builder", "conversation", dateEntry.Name(), file.Name())))
				}
			}
		}
	}

	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil {
		blocked = append(blocked, cfg.BlockedPaths...)
	}

	// Keep already-running Builder sessions current when a new workflow member
	// starts a chat after their guards were created. This closes the time-of-check
	// gap left by scanning only the directories that existed at turn start.
	workflowBuilderChatGuardSessions.Lock()
	sessions := workflowBuilderChatGuardSessions.byWorkflow[workspacePath]
	if sessions == nil {
		sessions = make(map[string]string)
		workflowBuilderChatGuardSessions.byWorkflow[workspacePath] = sessions
	}
	for otherSessionID, otherOwner := range sessions {
		if otherOwner != currentUserSegment {
			blocked = append(blocked, privatePathFor(otherOwner))
		}
		if otherSessionID == sessionID || otherOwner == currentUserSegment {
			continue
		}
		if otherConfig := common.GetSessionShellConfig(otherSessionID); otherConfig != nil {
			otherBlocked := append(otherConfig.BlockedPaths, privatePathFor(currentUserSegment))
			common.SetSessionFolderGuardBlockedPaths(otherSessionID, common.DeduplicateStrings(otherBlocked))
		}
	}
	sessions[sessionID] = currentUserSegment
	workflowBuilderChatGuardSessions.Unlock()

	common.SetSessionFolderGuardBlockedPaths(sessionID, common.DeduplicateStrings(blocked))
}
