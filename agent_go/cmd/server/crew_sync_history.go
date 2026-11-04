package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

const crewHandoffHistoryLimit = 32 << 20

func crewArchiveOnly(roots crewroot.Roots) bool {
	if cliHostOS == "windows" {
		return true
	}
	binding, err := crewroot.ReadSync(roots.ID)
	return err == nil && binding != nil && binding.ArchiveOnly
}

// Only the main owner's current conversation travels with this handoff.
// Runtime handles, account IDs, terminal snapshots and registry paths are not
// portable authority. UI events can contain terminal panes and source-machine
// handles too; the destination renders the canonical message history instead.
//
// The chat travels as its turn lines (PLAT-856-521): each keeps its id and
// time, and the destination appends only the lines it does not have yet, so
// a retried transfer never duplicates a turn.
type crewHandoffConversation struct {
	ConversationID string                  `json:"conversation_id"`
	ModelID        string                  `json:"model_id,omitempty"`
	System         json.RawMessage         `json:"system,omitempty"`
	Turns          []chatlog.Turn          `json:"turns"`
	Native         *sessiontransfer.Bundle `json:"native,omitempty"`
	Notice         string                  `json:"-"`
}

// history is the flat message list of the carried turns.
func (in *crewHandoffConversation) history() []llmtypes.MessageContent {
	var out []llmtypes.MessageContent
	for _, turn := range in.Turns {
		for _, raw := range turn.Messages {
			var message llmtypes.MessageContent
			if json.Unmarshal(raw, &message) == nil {
				out = append(out, message)
			}
		}
	}
	return out
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
	record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, claims.UserID, profile, binding, "")
	if err != nil {
		return record, err
	}
	applyCrewNativeToolPolicy(&profile, true, binding.ProjectNativeAgentTools)
	record.handoffAgentToolsMode = normalizeAgentToolsMode(profile.Runtime.AgentTools.Mode)
	return record, nil
}

func (api *StreamingAPI) captureCrewHandoffConversation(ctx context.Context, claims *UserClaims, roots crewroot.Roots, conversation ProductConversationRecord, nativeOnly ...bool) (*crewHandoffConversation, error) {
	strict := len(nativeOnly) > 0 && nativeOnly[0]
	noSession := func() (*crewHandoffConversation, error) {
		if strict {
			return nil, fmt.Errorf("this agent has no saved native session yet; send it a message before publishing")
		}
		return &crewHandoffConversation{ConversationID: conversation.ConversationID}, nil
	}
	path, found, err := FindChatHistoryConversationPathForSession(claims.UserID, conversation.SessionID, conversation.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return noSession()
	}
	if !isChatHeaderPath(path) {
		return nil, fmt.Errorf("the saved main conversation could not be read")
	}
	physical, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), path)
	if err != nil {
		return nil, err
	}
	if !bound {
		physical = filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(chatHistoryStoragePath(path)))
	}
	chat, err := chatlog.ReadChat(chatlog.ChatDir(physical), 0)
	if errors.Is(err, os.ErrNotExist) {
		return noSession()
	}
	if err != nil {
		return nil, err
	}
	// Check the persisted author as well as the registry/pair owner. A history
	// lookup must never turn another person's private conversation into payload.
	var doc struct {
		UserID  string                   `json:"user_id"`
		Runtime *ChatHistoryAgentRuntime `json:"runtime"`
	}
	header, _ := json.Marshal(chat.Header)
	if err = json.Unmarshal(header, &doc); err != nil {
		return nil, fmt.Errorf("the saved main conversation could not be read")
	}
	if doc.UserID != claims.UserID {
		return nil, fmt.Errorf("only your own main conversation can move with this Crew")
	}
	// UI events can hold terminal panes and source-machine handles: only the
	// dialogue travels.
	turns := make([]chatlog.Turn, 0, len(chat.Turns))
	for _, turn := range chat.Turns {
		turn.UIEvents = nil
		turns = append(turns, turn)
	}
	out := &crewHandoffConversation{ConversationID: conversation.ConversationID, ModelID: conversation.ModelID, System: chat.Header["system"], Turns: turns}
	if err = validateCrewHandoffConversation(out); err != nil {
		return nil, err
	}
	if candidate, captureErr := api.captureCrewNative(ctx, claims, roots, conversation, doc.Runtime); captureErr != nil {
		if strict {
			return nil, fmt.Errorf("cannot publish this native session: %w", captureErr)
		}
		log.Printf("[CREW_HANDOFF] project=%s phase=capture mode=archive native_unavailable=%q", roots.ID, captureErr)
	} else {
		out.Native = candidate
		if validateCrewHandoffConversation(out) != nil {
			out.Native = nil
		}
	}
	if strict && out.Native == nil {
		return nil, fmt.Errorf("this agent has no portable native session; keep using it on this Mac")
	}
	if err = validateCrewHandoffConversation(out); err != nil {
		return nil, err
	}
	mode := "archive"
	if out.Native != nil {
		mode = "native_candidate"
	}
	log.Printf("[CREW_HANDOFF] project=%s phase=capture mode=%s conversation=%s turns=%d", roots.ID, mode, out.ConversationID, len(out.Turns))
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
func (api *StreamingAPI) importCrewHandoffConversation(ctx context.Context, claims *UserClaims, roots crewroot.Roots, pair, transfer string, in *crewHandoffConversation, current ProductConversationRecord, nativeOnly ...bool) error {
	strict := len(nativeOnly) > 0 && nativeOnly[0]
	if strict && (in == nil || in.Native == nil || crewArchiveOnly(roots)) {
		return fmt.Errorf("publishing requires a supported native session on this server")
	}
	if err := validateCrewHandoffConversation(in); err != nil {
		return err
	}
	if (!strict && api.crewNativeTurnRunning(roots)) || (strict && api.crewAgentTurnRunning(current.SessionID)) {
		return errCrewNativeDestinationBusy
	}
	in.Notice = "The next reply will continue from saved conversation history."
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
	destinationToolsMode := current.handoffAgentToolsMode
	current, ok := doc.Entries[key]
	if !ok {
		return fmt.Errorf("the destination main conversation is unavailable")
	}
	current.handoffAgentToolsMode = destinationToolsMode
	if crewArchiveOnly(roots) {
		in.Native = nil
	}
	nativeCandidate := in.Native
	// Use the destination's own admitted account for the source AI program.
	// A new paired Crew may otherwise inherit an unrelated company default.
	if in.Native != nil {
		identity := doc.Agents[key]
		if chosen, reason := api.crewNativeDestination(ctx, claims, roots, current, identity, in.ModelID, in.Native.Provider); chosen != nil {
			current.Provider, current.ModelID, current.ConnectionID = in.Native.Provider, chosen.ModelID, chosen.ConnectionID
			if doc.Agents == nil {
				doc.Agents = map[string]ProductAgentIdentity{}
			}
			doc.Agents[key] = *chosen
		} else {
			if strict {
				return fmt.Errorf("cannot publish this native session: %s", reason)
			}
			nativeCandidate = nil
			in.Notice = reason
		}
	}
	// A retried file receipt must not replace turns already made after import.
	if current.SessionID == session && current.ConversationID == in.ConversationID {
		in.Notice = "Your main conversation is available on the destination computer."
		return nil
	}
	now := time.Now().UTC()
	path, ok := workProjectChatHistoryConversationPath(claims.UserID, current.WorkspacePath, session)
	if !ok {
		return fmt.Errorf("the destination's private conversation storage is unavailable")
	}
	data := map[string]interface{}{
		"session_id": session, "user_id": claims.UserID,
		"username": chatHistoryUsername(claims.UserID, ""), "agent_mode": "multi-agent",
		"updated_at": now.Format(time.RFC3339),
	}
	if len(in.System) > 0 {
		data["system"] = in.System
	}
	if nativeCandidate != nil {
		nativeLock := crewNativeLock(roots, nativeCandidate.Provider+"-"+nativeCandidate.SessionID)
		nativeLock.Lock()
		defer nativeLock.Unlock()
		if err := bindCrewNativeStage(roots, nativeCandidate); err != nil {
			return err
		}
	}
	if runtime, nativeErr := api.importCrewNative(ctx, claims, roots, current, session, nativeCandidate, strict); nativeErr != nil {
		// Busy means retry this handoff, not replace the running conversation
		// with an archive that starts a fresh native session.
		if strict || errors.Is(nativeErr, errCrewNativeDestinationBusy) {
			return nativeErr
		}
		log.Printf("[CREW_HANDOFF] project=%s phase=import mode=archive native_unavailable=%q", roots.ID, nativeErr)
		in.Notice = "The native session could not resume on this computer. The next reply will continue from saved conversation history."
		if errors.Is(nativeErr, sessiontransfer.ErrDiverged) {
			in.Notice = "The native history changed on both computers. Both copies were kept; the next reply will continue from saved conversation history."
		}
	} else if runtime != nil {
		data["runtime"] = runtime
		in.Notice = "The saved native session is ready to resume on the destination computer."
	}
	// The caller holds the Crew file lock. Going through workspace HTTP would
	// acquire that same lock again, so the chat folder is written here, under
	// the chat's own writer lock (chatlog takes an OS file lock).
	physical, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), path)
	if err != nil {
		return err
	}
	if !bound || !crewroot.Within(roots.Private, physical) {
		return fmt.Errorf("the destination's private conversation storage is unavailable")
	}
	stats, err := chatlog.AppendMissingTurns(chatlog.ChatDir(physical), in.Turns, chatHeaderPatch(data))
	if err != nil {
		return err
	}
	history := in.history()
	indexWrite := func(virtual, content string) error {
		indexPhysical, indexBound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), virtual)
		if err != nil {
			return err
		}
		if !indexBound || !crewroot.Within(roots.Private, indexPhysical) {
			return fmt.Errorf("the destination's private conversation storage is unavailable")
		}
		return fsutil.WriteJSONAtomic(indexPhysical, json.RawMessage(content), 0600)
	}
	if err = updatePersistedChatHistoryIndexWithWriter(claims.UserID, session, "multi-agent", history, stats.MessageCount, nil, path, int64(stats.HeaderBytes), now, nil, indexWrite); err != nil {
		return err
	}
	// The chat screen reads the durable journal, not the resume archive. Register
	// the destination owner through the normal path and project only dialogue;
	// source terminal events, handles and execution identities never travel.
	if api.eventStore == nil {
		return fmt.Errorf("the destination conversation journal is unavailable")
	}
	api.eventStore.SetSessionOwner(session, claims.UserID)
	if err = api.eventStore.SetSessionPersistenceClass(session, events.SessionPersistenceInteractiveChat); err != nil {
		return err
	}
	raw, err := chatlog.Record(chatlog.ChatDir(physical), 0, false)
	if err != nil {
		return err
	}
	var conversation builderConversationLog
	if err = json.Unmarshal(raw, &conversation); err != nil {
		return err
	}
	var dialogue []events.Event
	for _, row := range builderConversationToRawEventsLimit(conversation, 0) {
		var event events.Event
		if err = json.Unmarshal(row, &event); err != nil {
			return err
		}
		dialogue = append(dialogue, event)
	}
	if err = api.eventStore.ImportDurableChatEvents(session, dialogue); err != nil {
		return err
	}
	rememberPreviousConversation(&doc, key, current)
	current.SessionID, current.ConversationID = session, in.ConversationID
	current.FreshNative = data["runtime"] == nil
	current.UpdatedAt = now.Format(time.RFC3339Nano)
	doc.Entries[key] = current
	if err = store.writeDocument(ctx, registryPath, doc); err != nil {
		return err
	}
	mode := "archive"
	if !current.FreshNative {
		mode = "native_candidate"
	}
	log.Printf("[CREW_HANDOFF] project=%s pair=%s transfer=%s phase=import mode=%s conversation=%s session=%s turns_appended=%d messages=%d journal_rows=%d bytes=%d", roots.ID, pair, transfer, mode, in.ConversationID, session, stats.Appended, len(history), len(dialogue), stats.Bytes)
	return nil
}
