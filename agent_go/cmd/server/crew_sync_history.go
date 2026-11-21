package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// crewHandoffHistoryLimit is the one size limit for a moved or copied conversation (#3218, owner 19 Nov: about 300 MB).
// A copy of a larger shared chat keeps its newest part (readCrewForkJournalTail).
const crewHandoffHistoryLimit = 300 << 20

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
type crewHandoffConversation struct {
	ConversationID string                    `json:"conversation_id"`
	ModelID        string                    `json:"model_id,omitempty"`
	History        []llmtypes.MessageContent `json:"conversation_history"`
	Native         *sessiontransfer.Bundle   `json:"native,omitempty"`
}

func (api *StreamingAPI) captureCrewHandoffConversation(ctx context.Context, claims *UserClaims, roots crewroot.Roots, conversation ProductConversationRecord, nativeOnly ...bool) (*crewHandoffConversation, error) {
	strict := len(nativeOnly) > 0 && nativeOnly[0]
	data, err := ReadChatHistoryConversation(claims.UserID, conversation.SessionID, conversation.WorkspacePath)
	if errors.Is(err, errChatHistoryConversationNotFound) {
		if strict {
			return nil, fmt.Errorf("this agent has no saved native session yet; send it a message before publishing")
		}
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
		Runtime *ChatHistoryAgentRuntime  `json:"runtime"`
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
	out := &crewHandoffConversation{ConversationID: conversation.ConversationID, ModelID: conversation.ModelID, History: doc.History}
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
	log.Printf("[CREW_HANDOFF] project=%s phase=capture mode=%s conversation=%s messages=%d", roots.ID, mode, out.ConversationID, len(out.History))
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
