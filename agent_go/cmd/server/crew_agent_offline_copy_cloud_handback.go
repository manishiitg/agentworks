package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Cloud sources use the same private return cache and quoted human receipt as
// Mac sources. The current exact owned row gates this, not upload selection or
// general home membership. No transcript is read from the copy.
func crewOfflineCloudHandback(ctx context.Context, user string, record ProductConversationRecord) (string, error) {
	claims := GetUserFromContext(ctx)
	if IsLocalMode() || claims == nil || claims.UserID != user || claims.AccessToken != nil || claims.Scope != "" || record.ProfileID != crewProfileID {
		return "", fmt.Errorf("cloud source owner unavailable")
	}
	project := crewResourceProjectID(record.ConversationKey)
	teamDir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	mu := crewTeamRecordMutex(teamDir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil {
		return "", err
	}
	a, err := crewOfflineServerOwner(ctx, user, record, team)
	if err != nil {
		return "", err
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || roots.ID != project || roots.Owner != team.OwnerID {
		return "", fmt.Errorf("source roots changed")
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	if !b.valid() {
		return "", fmt.Errorf("source binding changed")
	}
	path, err := crewOfflineAppReturnsPathIn(roots.Private, record)
	if err != nil {
		return "", err
	}
	cached, err := readCrewOfflineAppReturns(path, b)
	if err != nil {
		return "", err
	}
	raw, err := ReadChatHistoryConversation(user, record.SessionID, record.WorkspacePath)
	if err != nil {
		return "", err
	}
	var saved struct {
		UserID  string                    `json:"user_id"`
		Session string                    `json:"session_id"`
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.UserID != user || saved.Session != record.SessionID {
		return "", fmt.Errorf("source saved history changed")
	}
	dir, err := crewOfflineDirectory(project, a.ID)
	if err != nil {
		return "", err
	}
	if crewOfflineAppReturnsAccepted(saved.History, crewOfflineAppReturnsText(cached.Records)) {
		ids := make([]string, 0, len(cached.Records))
		for _, r := range cached.Records {
			ids = append(ids, r.ID)
		}
		if err = acknowledgeCrewOfflineReturns(project, a.ID, dir, b, ids); err != nil {
			return "", err
		}
		cached.Records = nil
	}
	// Preserve an unreceipted batch: newer replies must not change its receipt.
	if len(cached.Records) == 0 {
		state, err := readCrewOfflineReturns(dir, b)
		if err != nil {
			return "", err
		}
		pending := state.Pending
		if len(pending) > 200 {
			pending = pending[:200]
		}
		cached = crewOfflineAppReturns{Binding: b, Records: pending}
		if err = fsutil.WriteJSONAtomic(path, cached, 0600); err != nil {
			return "", err
		}
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=handback_cached count=%d place=server", project, a.ID, record.SessionID, len(cached.Records))
	return path, nil
}
