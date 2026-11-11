package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

var errInvalidMainChat = errors.New("the project main chat has an invalid ID")

func ordinaryProjectChatKey(projectID, key string) bool {
	id := strings.TrimPrefix(key, projectID+codeChatSideMarker)
	return key != id && id != "" && !strings.Contains(id, ":") && !strings.HasPrefix(id, "fn.") && !strings.HasPrefix(id, "agent-")
}

func validProjectMainRecord(userID, profileID, projectID, workspace string, record ProductConversationRecord) bool {
	return record.SessionID != "" && record.ProfileID == profileID && (record.ConversationKey == projectID || ordinaryProjectChatKey(projectID, record.ConversationKey)) && workspacePathsMatchForUser(userID, record.WorkspacePath, workspace)
}

func originalMainChatKey(projectID, conversationID string) string {
	sum := sha256.Sum256([]byte(conversationID))
	return projectID + codeChatSideMarker + "root-" + hex.EncodeToString(sum[:6])
}

// Project admission is independent of a chat. Only an actual default-chat open
// resolves this optional role, and it never repairs, creates or adopts an agent.
var errChooseProjectAgent = errors.New("Choose an agent in this Crew, or create an agent first.")

func (store productConversationRegistryStore) resolveProjectMainAddress(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding) (productConversationBinding, error) {
	if !isProjectProfileID(profile.ID) || !binding.ProjectMainAddress {
		return binding, nil
	}
	if binding.ManifestPath == "" {
		return binding, errChooseProjectAgent
	}
	raw, found, err := store.read(ctx, binding.ManifestPath)
	if err != nil {
		return binding, err
	}
	if !found {
		return binding, errChooseProjectAgent
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return binding, err
	}
	if manifest.ID != binding.ResourceID || manifest.Product != profile.ID {
		return binding, errInvalidMainChat
	}
	key := strings.TrimSpace(manifest.MainChatKey)
	if !ordinaryProjectChatKey(manifest.ID, key) {
		return binding, errChooseProjectAgent
	}
	doc, err := store.loadDocument(ctx, productConversationRegistryPath(userID))
	if err != nil {
		return binding, err
	}
	record, exists := doc.Entries[productConversationRegistryEntryKey(profile.ID, key)]
	if !exists || record.ConversationKey != key || !validProjectMainRecord(userID, profile.ID, manifest.ID, binding.WorkspacePath, record) {
		return binding, errChooseProjectAgent
	}
	binding.ConversationKey, binding.ProjectMainChatKey = key, key
	binding.ProjectMainAddress = false
	binding.AuthoritativeSessionID = ""
	return binding, nil
}

func (store productConversationRegistryStore) writeMainChatPointer(ctx context.Context, path, key, session string) error {
	raw, found, err := store.read(ctx, path)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("project manifest was not found")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	manifest["main_chat_key"], _ = json.Marshal(key)
	manifest["session_id"], _ = json.Marshal(session)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return store.write(ctx, path, string(encoded)+"\n")
}
