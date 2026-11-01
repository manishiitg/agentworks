package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const crewHandoffHistoryLimit = 32 << 20

// Only the main owner's current conversation travels with this handoff.
// Runtime handles, account IDs, terminal snapshots and registry paths are not
// portable authority. UI events can contain terminal panes and source-machine
// handles too; the destination renders the canonical message history instead.
type crewHandoffConversation struct {
	ConversationID string                    `json:"conversation_id"`
	History        []llmtypes.MessageContent `json:"conversation_history"`
}

func (api *StreamingAPI) crewHandoffMain(ctx context.Context, claims *UserClaims, project string) (ProductConversationRecord, error) {
	profile, err := api.agentProfiles.Resolve("work", 0, claims.UserID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	binding, err := resolveProductProjectBinding(ctx, claims.UserID, profile, project)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	return defaultProductConversationRegistryStore().resolveOrCreate(ctx, claims.UserID, profile, binding, "")
}

func (api *StreamingAPI) captureCrewHandoffConversation(ctx context.Context, claims *UserClaims, roots crewroot.Roots, conversation ProductConversationRecord) (*crewHandoffConversation, error) {
	data, err := ReadChatHistoryConversation(claims.UserID, conversation.SessionID, conversation.WorkspacePath)
	if errors.Is(err, errChatHistoryConversationNotFound) {
		return &crewHandoffConversation{ConversationID: conversation.ConversationID}, nil
	}
	if err != nil {
		return nil, err
	}
	// Check the persisted author as well as the registry/pair owner. A history
	// lookup must never turn another person's private conversation into payload.
	var doc struct {
		UserID  string                    `json:"user_id"`
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if len(data) > crewHandoffHistoryLimit {
		return nil, fmt.Errorf("this conversation is too large to move; keep using its agent on this computer")
	}
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the saved main conversation could not be read")
	}
	if doc.UserID != claims.UserID {
		return nil, fmt.Errorf("only your own main conversation can move with this Crew")
	}
	out := &crewHandoffConversation{ConversationID: conversation.ConversationID, History: doc.History}
	if err = validateCrewHandoffConversation(out); err != nil {
		return nil, err
	}
	log.Printf("[CREW_HANDOFF] project=%s phase=capture mode=archive conversation=%s messages=%d", roots.ID, out.ConversationID, len(out.History))
	return out, nil
}

func validateCrewHandoffConversation(in *crewHandoffConversation) error {
	if in == nil || !strings.HasPrefix(in.ConversationID, "conversation-") || !productConversationKeyPattern.MatchString(in.ConversationID) {
		return fmt.Errorf("the main conversation is missing from this handoff; retry from an updated Mac app")
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > crewHandoffHistoryLimit {
		return fmt.Errorf("this conversation is too large to move; keep using its agent on this computer")
	}
	return nil
}

// A new destination runtime session avoids reusing a stale in-memory/native
// session on the return trip. The logical conversation ID stays unchanged.
// Its ID is derived from the admitted pair and transfer, never a remote path.
func (api *StreamingAPI) importCrewHandoffConversation(ctx context.Context, claims *UserClaims, roots crewroot.Roots, pair, transfer string, in *crewHandoffConversation, current ProductConversationRecord) error {
	if err := validateCrewHandoffConversation(in); err != nil {
		return err
	}
	session := "product-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(claims.UserID+"/"+roots.ID+"/"+pair+"/"+transfer)).String()
	store := defaultProductConversationRegistryStore()
	registryPath := productConversationRegistryPath(claims.UserID)
	lock := productConversationRegistryMutex(registryPath)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, registryPath)
	if err != nil {
		return err
	}
	key := productConversationRegistryEntryKey("work", current.ConversationKey)
	current, ok := doc.Entries[key]
	if !ok {
		return fmt.Errorf("the destination main conversation is unavailable")
	}
	// A retried file receipt must not replace turns already made after import.
	if current.SessionID == session && current.ConversationID == in.ConversationID {
		return nil
	}
	now := time.Now().UTC()
	path, ok := workProjectChatHistoryConversationPath(claims.UserID, current.WorkspacePath, session, now)
	if !ok {
		return fmt.Errorf("the destination's private conversation storage is unavailable")
	}
	data := map[string]interface{}{
		"session_id": session, "user_id": claims.UserID,
		"username": chatHistoryUsername(claims.UserID, ""), "agent_mode": "multi-agent",
		"conversation_history": in.History, "updated_at": now.Format(time.RFC3339),
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	// The caller holds the Crew file lock. Going through workspace HTTP would
	// acquire that same lock again, including for private builder records.
	write := func(virtual, content string) error {
		physical, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), virtual)
		if err != nil {
			return err
		}
		if !bound || !crewroot.Within(roots.Private, physical) {
			return fmt.Errorf("the destination's private conversation storage is unavailable")
		}
		return fsutil.WriteJSONAtomic(physical, json.RawMessage(content), 0600)
	}
	if err = write(path, string(raw)); err != nil {
		return err
	}
	if err = write(chatHistoryResumeSnapshotPath(path), string(boundChatHistoryResumeSnapshot(projectChatHistoryConversationForResumePage(raw, chatHistoryResumeSnapshotTurns, 0)))); err != nil {
		return err
	}
	if err = updatePersistedChatHistoryIndexWithWriter(claims.UserID, session, "multi-agent", in.History, len(in.History), nil, path, int64(len(raw)), now, nil, write); err != nil {
		return err
	}
	rememberPreviousConversation(&doc, key, current)
	current.SessionID, current.ConversationID = session, in.ConversationID
	current.FreshNative = true
	current.UpdatedAt = now.Format(time.RFC3339Nano)
	doc.Entries[key] = current
	if err = store.writeDocument(ctx, registryPath, doc); err != nil {
		return err
	}
	log.Printf("[CREW_HANDOFF] project=%s pair=%s transfer=%s phase=import mode=archive conversation=%s session=%s messages=%d bytes=%d", roots.ID, pair, transfer, in.ConversationID, session, len(in.History), len(raw))
	return nil
}
