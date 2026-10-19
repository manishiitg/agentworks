package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

var errInvalidMainChat = errors.New("choose an ordinary chat in this project")
var errMainChatChanged = errors.New("these chats changed; reload the project before changing the main chat")
var errMainChatBusy = errors.New("wait for both chats to finish and clear their queued messages, then try again")

func swappableMainChatKey(projectID, key string) bool {
	id := strings.TrimPrefix(key, projectID+codeChatSideMarker)
	return key != id && id != "" && !strings.Contains(id, ":") && !strings.HasPrefix(id, "fn.") && !strings.HasPrefix(id, "agent-")
}

func validProjectMainRecord(userID, profileID, projectID, workspace string, record ProductConversationRecord) bool {
	return record.SessionID != "" && record.ProfileID == profileID && (record.ConversationKey == projectID || swappableMainChatKey(projectID, record.ConversationKey)) && workspacePathsMatchForUser(userID, record.WorkspacePath, workspace)
}

func originalMainChatKey(projectID, conversationID string) string {
	sum := sha256.Sum256([]byte(conversationID))
	return projectID + codeChatSideMarker + "root-" + hex.EncodeToString(sum[:6])
}

// main is an address, never a physical chat ID. Transitional lazy migration:
// move a legacy root slot once, with a plain registry backup; thereafter the
// root address resolves only through main_chat_key. Remove this migration
// once the owner-authorized server rollouts have moved their registries.
func (store productConversationRegistryStore) resolveProjectMainAddress(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding) (productConversationBinding, error) {
	if !isProjectProfileID(profile.ID) || !binding.ProjectMainAddress || binding.ManifestPath == "" {
		return binding, nil
	}
	path := productConversationRegistryPath(userID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	raw, found, err := store.read(ctx, binding.ManifestPath)
	if err != nil {
		return binding, err
	}
	if !found {
		return binding, errors.New("project manifest was not found")
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return binding, err
	}
	if manifest.ID != binding.ResourceID || manifest.Product != profile.ID {
		return binding, errors.New("project main address does not match its manifest")
	}
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return binding, err
	}
	mainKey := strings.TrimSpace(manifest.MainChatKey)
	if mainKey != "" {
		record, exists := doc.Entries[productConversationRegistryEntryKey(profile.ID, mainKey)]
		if !exists || !validProjectMainRecord(userID, profile.ID, manifest.ID, binding.WorkspacePath, record) {
			// Fix forward using only this caller's ordinary chats in the same
			// project. Prefer the original root, otherwise the newest chat.
			var replacement ProductConversationRecord
			for _, candidate := range doc.Entries {
				if !validProjectMainRecord(userID, profile.ID, manifest.ID, binding.WorkspacePath, candidate) {
					continue
				}
				if replacement.SessionID == "" || candidate.ConversationKey == manifest.ID || (replacement.ConversationKey != manifest.ID && (candidate.UpdatedAt > replacement.UpdatedAt || (candidate.UpdatedAt == replacement.UpdatedAt && candidate.ConversationKey < replacement.ConversationKey))) {
					replacement = candidate
				}
			}
			if replacement.SessionID != "" {
				log.Printf("product main chat: repairing missing pointer project=%s previous=%s replacement=%s", manifest.ID, mainKey, replacement.ConversationKey)
				if replacement.ConversationKey == manifest.ID {
					mainKey, manifest.SessionID = "", replacement.SessionID
				} else {
					mainKey = replacement.ConversationKey
					if err := store.writeMainChatPointer(ctx, binding.ManifestPath, mainKey, replacement.SessionID); err != nil {
						return binding, err
					}
				}
			} else if manifest.SessionID != "" {
				// No surviving slot: reopen the manifest's existing session,
				// keeping its transcript rather than creating an empty session.
				log.Printf("product main chat: restoring missing pointer project=%s previous=%s from manifest session", manifest.ID, mainKey)
				mainKey = ""
			}
		}
	}
	if mainKey == "" {
		// Backup is per registry, before its first legacy move; never overwrite.
		backup := path + ".before-chat-ids.json"
		if _, exists, e := store.read(ctx, backup); e != nil {
			return binding, e
		} else if !exists {
			if registryRaw, exists, e := store.read(ctx, path); e != nil {
				return binding, e
			} else if exists {
				if e := store.write(ctx, backup, registryRaw); e != nil {
					return binding, e
				}
			}
		}
		legacyKey := productConversationRegistryEntryKey(profile.ID, manifest.ID)
		record, legacy := doc.Entries[legacyKey]
		if !legacy {
			// A failed manifest write may follow the completed one-time move.
			for _, candidate := range doc.Entries {
				if candidate.ProfileID == profile.ID && candidate.SessionID == manifest.SessionID && workspacePathsMatchForUser(userID, candidate.WorkspacePath, binding.WorkspacePath) {
					record = candidate
					break
				}
			}
		}
		if record.SessionID == "" {
			now := store.now().UTC().Format(time.RFC3339Nano)
			record = ProductConversationRecord{ConversationID: "conversation-" + store.newID(), SessionID: manifest.SessionID, ProfileID: profile.ID, ProfileVersion: profile.Version, WorkspacePath: binding.WorkspacePath, ResourceID: manifest.ID, Title: binding.Title, Description: binding.Description, CreatedAt: now, UpdatedAt: now}
		}
		if record.ConversationID == "" || record.SessionID == "" || record.ProfileID != profile.ID || !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			return binding, errors.New("the original main chat has an invalid identity")
		}
		mainKey = record.ConversationKey
		if legacy || mainKey == "" || mainKey == manifest.ID {
			mainKey = originalMainChatKey(manifest.ID, record.ConversationID)
		}
		physical := productConversationRegistryEntryKey(profile.ID, mainKey)
		if other, exists := doc.Entries[physical]; exists && (other.ConversationID != record.ConversationID || other.SessionID != record.SessionID) {
			return binding, errors.New("the original main chat ID is already in use")
		}
		record.ConversationKey = mainKey
		doc.Entries[physical] = record
		if legacy {
			delete(doc.Entries, legacyKey)
			if identity, exists := doc.Agents[legacyKey]; exists {
				if doc.Agents == nil {
					doc.Agents = map[string]ProductAgentIdentity{}
				}
				doc.Agents[physical] = identity
				delete(doc.Agents, legacyKey)
			}
			if prior, exists := doc.Previous[legacyKey]; exists {
				for index := range prior {
					prior[index].ConversationKey = mainKey
				}
				doc.Previous[physical] = prior
				delete(doc.Previous, legacyKey)
			}
		}
		if err := store.writeDocument(ctx, path, doc); err != nil {
			return binding, err
		}
		if err := store.writeMainChatPointer(ctx, binding.ManifestPath, mainKey, record.SessionID); err != nil {
			return binding, err
		}
	}
	if !swappableMainChatKey(manifest.ID, mainKey) {
		return binding, errInvalidMainChat
	}
	record, exists := doc.Entries[productConversationRegistryEntryKey(profile.ID, mainKey)]
	if !exists || record.SessionID == "" || !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
		return binding, errors.New("the project's main chat was not found")
	}
	binding.ConversationKey, binding.ProjectMainChatKey = mainKey, mainKey
	// A physical key never adopts a different session from a role pointer.
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

type mainChatChange struct {
	Main         ProductConversationRecord `json:"main"`
	PreviousMain ProductConversationRecord `json:"previous_main"`
}

// Changing main moves only the role pointer. Every chat binding, agent name,
// model choice, previous conversation and key-addressed caller stays intact.
func (store productConversationRegistryStore) setMainChat(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding, targetKey, expectedMain, expectedTarget string, guard func(string, string) error) (mainChatChange, error) {
	projectID := binding.ResourceID
	if !isProjectProfileID(profile.ID) || projectID == "" || !swappableMainChatKey(projectID, targetKey) || binding.ManifestPath == "" {
		return mainChatChange{}, errInvalidMainChat
	}
	path := productConversationRegistryPath(userID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return mainChatChange{}, err
	}
	raw, found, err := store.read(ctx, binding.ManifestPath)
	if err != nil {
		return mainChatChange{}, err
	}
	if !found {
		return mainChatChange{}, errMainChatChanged
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return mainChatChange{}, err
	}
	previous, previousOK := doc.Entries[productConversationRegistryEntryKey(profile.ID, manifest.MainChatKey)]
	target, targetOK := doc.Entries[productConversationRegistryEntryKey(profile.ID, targetKey)]
	if !previousOK || !targetOK || previous.SessionID != expectedMain || target.SessionID != expectedTarget || previous.SessionID == "" || target.SessionID == "" {
		return mainChatChange{}, errMainChatChanged
	}
	for _, record := range []ProductConversationRecord{previous, target} {
		if record.ProfileID != profile.ID || !workspacePathsMatchForUser(userID, record.WorkspacePath, binding.WorkspacePath) {
			return mainChatChange{}, fmt.Errorf("%w: both chats must belong to this project", errInvalidMainChat)
		}
	}
	if err := guard(previous.SessionID, target.SessionID); err != nil {
		return mainChatChange{}, err
	}
	if err := store.writeMainChatPointer(ctx, binding.ManifestPath, targetKey, target.SessionID); err != nil {
		return mainChatChange{}, err
	}
	target.IsMainChat = true
	return mainChatChange{Main: target, PreviousMain: previous}, nil
}
