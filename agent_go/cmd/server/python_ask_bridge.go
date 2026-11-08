package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// These are private Python bridge adapters, not additional model tools. They
// reuse the captured run actor and the same Crew ask/inbox authorization.
func (api *StreamingAPI) bindPythonAskExecutors(executors map[string]interface{}, actor UserClaims, source func(context.Context) (triggerLinkCaller, error), checkTarget func(context.Context, triggerTarget, bool) error) {
	executors["ask_crew"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		caller, err := source(ctx)
		if err != nil {
			return "", err
		}
		ctx = context.WithValue(ctx, UserContextKey, &actor)
		name := strings.TrimSpace(externalArg(args, "crew"))
		visible, err := api.externalCrewsVisible(ctx, &actor, "")
		if err != nil {
			return "", fmt.Errorf("Choose a Crew you can access by its exact name or ID.")
		}
		matches := []string{}
		for _, item := range visible {
			id, label := fmt.Sprint(item["id"]), fmt.Sprint(item["name"])
			if strings.EqualFold(name, id) || strings.EqualFold(name, label) {
				matches = append(matches, id)
			}
		}
		if len(matches) != 1 {
			return "", fmt.Errorf("Choose a Crew you can access by its exact name or ID.")
		}
		requestArgs := map[string]interface{}{"crew_id": matches[0], "chat_id": externalArg(args, "chat_id"), "message": externalArg(args, "message"), "submission_id": externalArg(args, "submission_id")}
		target, err := api.externalMessageTarget(ctx, &actor, requestArgs)
		if err != nil {
			return "", err
		}
		if err = checkTarget(ctx, target, true); err != nil {
			return "", err
		}
		if caller.isTarget(target) {
			return "", fmt.Errorf("This script cannot ask its own Crew. Use agent() for work inside this run.")
		}
		if err := requireLinkedOwnCrew(ctx, actor.UserID, caller, target); err != nil {
			return "", err
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/external/call", nil)
		response := &internalResponseCapture{header: http.Header{}}
		api.externalSendMessage(response, request, requestArgs, target)
		if response.status >= 400 {
			var failure struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(response.body.Bytes(), &failure) == nil && failure.Error.Message != "" {
				return "", fmt.Errorf("%s", failure.Error.Message)
			}
			return "", fmt.Errorf("The message could not be sent. Inspect this run before trying again.")
		}
		return response.body.String(), nil
	}
	executors["read_agent_messages"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		if _, err := source(ctx); err != nil {
			return "", err
		}
		ctx = context.WithValue(ctx, UserContextKey, &actor)
		caller := externalCrewCaller(&actor)
		inbox := externalArg(args, "inbox_id")
		if _, err := api.externalMessagePeerForCaller(ctx, &actor, caller, inbox, false); err != nil {
			return "", err
		}
		seconds := float64(0)
		if value, ok := args["wait_seconds"].(float64); ok {
			seconds = value
		}
		seconds = min(25, max(0, seconds))
		result, err := api.readAgentMessages(ctx, actor.UserID, caller, inbox, externalInt(args, "after", 0), 30, time.Duration(seconds*float64(time.Second)))
		if err != nil {
			return "", err
		}
		// A server notification is not an answer from the recipient. Retain only
		// explicit peer messages from this authorized inbox.
		conversation, side, found, err := callerAgentMailboxConversation(ctx, inbox, actor.UserID, caller)
		if err != nil || !found {
			return "", fmt.Errorf("The reply is unavailable; inspect this conversation before trying again.")
		}
		peerIDs := map[string]bool{}
		for _, message := range conversation.Messages {
			if message.From != side && !serverAuthoredKind(message.Kind) {
				peerIDs[message.ID] = true
			}
		}
		messages, _ := result["messages"].([]map[string]interface{})
		kept := []map[string]interface{}{}
		for _, message := range messages {
			if peerIDs[fmt.Sprint(message["message_id"])] {
				kept = append(kept, message)
			}
		}
		result["messages"] = kept
		data, err := json.Marshal(result)
		return string(data), err
	}
}
