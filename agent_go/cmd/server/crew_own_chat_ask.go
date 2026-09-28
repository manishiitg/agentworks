package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

// A person asking a Crew (MCP ask_crew, `agentworks crews ask`) talks in their
// own chat of it, the same rule as a 1:1 Slack DM or WhatsApp
// (senderProfileTurn): the owner's ask continues the Crew's own chat, anyone
// else's continues their reader chat. Only Crews and workflows calling a Crew
// get a separate per-caller conversation. Before this, an owner's MCP ask
// opened a new "Called by external connection" chat with a machine-written
// task instead of their message (RTS 2026-09-28).

// crewOwnChatAskTurn runs the turn; tests replace it.
var crewOwnChatAskTurn func(api *StreamingAPI, ctx context.Context, reqMap map[string]interface{}, sessionID, userID string) (internalSessionTurnResult, error)

// isPersonCrewAsk reports the built-in ask on a Crew from a signed-in person.
func isPersonCrewAsk(target triggerTarget, caller triggerLinkCaller, fn crewFunction) bool {
	return target.Kind == triggerCallerCrew && caller.Stamp.Type == triggerCallerUser &&
		fn.Name == crewFunctionAskName && fn.CreatedBy == defaultAskCrewFunction().CreatedBy
}

// crewOwnChatAskRequest builds the turn in the person's own chat of the Crew,
// resolved exactly as their web chat and Slack DMs resolve it.
func (api *StreamingAPI) crewOwnChatAskRequest(ctx context.Context, userID string, target triggerTarget, message string) (map[string]interface{}, string, error) {
	if api == nil || api.agentProfiles == nil {
		return nil, "", fmt.Errorf("Crews are unavailable on this server")
	}
	profileID := firstNonEmptyTrimmed(target.CrewProfile, "work")
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return nil, "", fmt.Errorf("resolve Crew profile: %w", err)
	}
	binding, owned, err := resolveConversationBindingForUser(ctx, userID, profile, target.CrewID)
	if err != nil {
		return nil, "", fmt.Errorf("open your chat of %q: %w", target.Label, err)
	}
	if owned {
		if err := initializeProductConversationWorkspace(ctx, userID, profile, binding); err != nil {
			return nil, "", err
		}
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, userID, profile, binding, "")
	if err != nil {
		return nil, "", fmt.Errorf("open your chat of %q: %w", target.Label, err)
	}
	reqMap, sessionID, _, err := productBotTurnRequest(ctx, userID, profile, conversation, services.BotIncomingMessage{Text: message}, services.ThreadID{})
	if err != nil {
		return nil, "", err
	}
	reqMap["triggered_by"] = "external"
	reqMap["triggered_by_label"] = "Asked via MCP"
	return reqMap, sessionID, nil
}

// runCrewOwnChatAsk sends the person's message to their own chat of the Crew
// and settles the call with the Crew's final reply. Like a workflow ask, the
// timeout counts from the Crew's last sign of life and only releases the
// caller; the turn keeps running and its reply is delivered late.
func (api *StreamingAPI) runCrewOwnChatAsk(call *crewFunctionCall, target triggerTarget, message string, timeout time.Duration) {
	hardCap := crewFunctionHardCap(timeout)
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: call.UserID}), hardCap)
	defer cancel()
	reqMap, sessionID, err := api.crewOwnChatAskRequest(ctx, call.UserID, target, message)
	if err != nil {
		call.settle("failed", nil, err.Error())
		return
	}
	call.mu.Lock()
	call.Status = "running"
	call.RunID, call.RunIDs = sessionID, []string{sessionID}
	call.mu.Unlock()
	call.persist()
	turnDone := make(chan struct{})
	go api.watchAskActivity(call, sessionID, "Crew "+fmt.Sprintf("%q", call.TargetLabel), timeout, turnDone)
	var result internalSessionTurnResult
	if crewOwnChatAskTurn != nil {
		result, err = crewOwnChatAskTurn(api, ctx, reqMap, sessionID, call.UserID)
	} else {
		result, err = api.startSessionInternalWithResult(ctx, reqMap, sessionID, call.UserID, nil)
	}
	close(turnDone)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			call.settle("failed", nil, fmt.Sprintf("the Crew was still not done after %s", hardCap))
			return
		}
		call.settle("failed", nil, fmt.Sprintf("the Crew did not answer: %v", err))
		return
	}
	answer := strings.TrimSpace(result.FinalResponse)
	if answer == "" {
		call.settle("failed", nil, "the Crew finished without a reply")
		return
	}
	call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(answer)}, "")
}
