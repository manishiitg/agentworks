package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// The destination is resolved under the recipient's own login before this
// call. Never import a maker session ID verbatim: fork the native bundle into
// a new writer, or retain just the canonical dialogue for another program.
func (api *StreamingAPI) importCrewAgentForkCopy(r *http.Request, offer crewAgentFork, payload crewAgentForkPayload, destination ProductConversationRecord, roots crewroot.Roots) error {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID != offer.Recipient || destination.ConversationKey != crewAgentForkChatKey(offer) || destination.WorkspacePath != roots.Virtual {
		return fmt.Errorf("import this fork into your own new chat in the same Crew")
	}
	team, err := readCrewTeam(offer.Project)
	if err != nil {
		return err
	}
	if err = offer.admit(team, claims.UserID, true, time.Now()); err != nil {
		return err
	}
	if err := validateCrewAgentForkPayload(payload); err != nil {
		return err
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s event=import_started", offer.Project, offer.ID, offer.CopyAgentID)
	var runtime *ChatHistoryAgentRuntime
	if payload.Conversation.Native != nil && destination.Provider == payload.Conversation.Native.Provider {
		var err error
		// The descriptor's source-machine Root is never authority to read
		// native files on this computer. Only this fork's admitted stage is.
		dir, stageErr := crewAgentForkDirectory(offer.Project, offer.ID)
		if stageErr != nil {
			return stageErr
		}
		native := *payload.Conversation.Native
		native.Root = filepath.Join(dir, "native")
		runtime, err = api.importCrewAgentForkNative(r, roots, destination, &native)
		if err != nil {
			// A different program or unsupported native format is not a failed fork.
			// No source-account details or native error paths go to the logs.
			log.Printf("[AGENT_FORK] project=%s fork=%s event=native_skipped reason=history_continuity", offer.Project, offer.ID)
		}
	} else {
		log.Printf("[AGENT_FORK] project=%s fork=%s event=native_skipped reason=program_differs_or_no_native", offer.Project, offer.ID)
	}
	now := time.Now().UTC()
	path, ok := workProjectChatHistoryConversationPath(claims.UserID, destination.WorkspacePath, destination.SessionID, now)
	if !ok || path == "" {
		return fmt.Errorf("your private conversation storage is unavailable")
	}
	data, err := json.Marshal(map[string]any{
		"session_id": destination.SessionID, "user_id": claims.UserID,
		"workspace_path": destination.WorkspacePath, "agent_mode": "multi-agent",
		"title": destination.Title, "conversation_history": payload.Conversation.History,
		"runtime": runtime, "created_at": destination.CreatedAt, "updated_at": now.Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if err = writeRawFileToWorkspace(r.Context(), path, string(data)); err != nil {
		return err
	}
	if err = writeChatHistoryResumeSnapshot(r.Context(), path, data); err != nil {
		return err
	}
	if err = updatePersistedChatHistoryIndex(claims.UserID, destination.SessionID, "multi-agent", payload.Conversation.History, runtime, path, int64(len(data)), now, nil); err != nil {
		return err
	}
	if err = api.seedForkChatJournal(claims.UserID, destination, offer.Agent.ChatKey, offer.Agent.Name, payload.Conversation.History); err != nil {
		return err
	}
	log.Printf("[AGENT_FORK] project=%s fork=%s copy=%s session=%s event=imported native=%t", offer.Project, offer.ID, offer.CopyAgentID, destination.SessionID, runtime != nil)
	return nil
}

func crewAgentForkChatKey(offer crewAgentFork) string {
	return offer.Project + codeChatSideMarker + "fork-" + offer.ID
}

func (api *StreamingAPI) importCrewAgentForkNative(r *http.Request, roots crewroot.Roots, destination ProductConversationRecord, bundle *sessiontransfer.Bundle) (*ChatHistoryAgentRuntime, error) {
	claims := GetUserFromContext(r.Context())
	// Match the ordinary chat-fork copy budget before duplicating a native
	// database. Dialogue continuity remains available when space is tight.
	var size uint64
	for _, file := range bundle.Files {
		if file.Size < 0 || uint64(file.Size) > 64<<20 || size > (64<<20)-uint64(file.Size) {
			return nil, fmt.Errorf("native copy budget exceeded")
		}
		size += uint64(file.Size)
	}
	free, err := localChatFreeBytes(roots.Private)
	if err != nil || free < size*3+(256<<20) {
		return nil, fmt.Errorf("not enough free space for the native copy")
	}
	if err := bundle.ValidateFiles(); err != nil {
		return nil, err
	}
	cwd, err := crewCLIWorkingDir(roots.Virtual, claims.UserID, destination.SessionID, bundle.Provider, false)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(roots.Private, "agent-fork-import-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	independent, err := sessiontransfer.ForkJSONL(bundle, filepath.Join(stage, "native"), cwd)
	if err != nil {
		return nil, err
	}
	return api.importCrewNativeInto(r.Context(), claims, roots, destination, destination.SessionID, independent, cwd)
}
