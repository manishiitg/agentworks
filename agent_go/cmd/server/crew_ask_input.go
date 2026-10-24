package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Only canonical external Ask dispatch creates this provenance. It follows a
// queued turn, but is never accepted from model arguments or a request body.
type crewAskInputScope struct {
	User, Connection, Workspace, Chat, Session, Query string
}
type crewAskInputKey struct{}

func crewAskInputFromContext(ctx context.Context) *crewAskInputScope {
	scope, _ := ctx.Value(crewAskInputKey{}).(*crewAskInputScope)
	return scope
}

func (scope *crewAskInputScope) matches(r *http.Request, workspace, chat, session, query string) bool {
	user, connection := mcpTaskPrincipal(r)
	return scope != nil && scope.User == user && scope.Connection == connection &&
		scope.Workspace == workspace && scope.Chat == chat && scope.Session == session && scope.Query == query
}

// These prompts use the existing clarification waiter and choice validator.
// Unlike a Builder UI card, an external Ask prompt is returned only to the
// asking connection, never broadcast to other owners watching the Crew.
func (api *StreamingAPI) externalCrewAskPending(r *http.Request, workspace, chat, session, query string) []map[string]any {
	api.codingAgentClarificationsMu.Lock()
	defer api.codingAgentClarificationsMu.Unlock()
	p := api.codingAgentClarifications[session]
	if p == nil || !p.ask.matches(r, workspace, chat, session, query) || p.ctx.Err() != nil || api.currentConversationTurnExecutionID(session) != query || clarificationAskingTurnEnded(p.askedTurn, api.clarificationTurnToken(session)) {
		return []map[string]any{}
	}
	return []map[string]any{{"request_id": p.promptID, "questions": p.questions}}
}

func (api *StreamingAPI) replyCrewAskInput(w http.ResponseWriter, r *http.Request, args map[string]any, workspace, chat, session, query string) {
	var answers []codingAgentQuestionAnswerInput
	raw, err := json.Marshal(args["answers"])
	if err == nil {
		err = json.Unmarshal(raw, &answers)
	}
	if err != nil || len(answers) == 0 || len(answers) > 12 || query == "" || strings.TrimSpace(externalArg(args, "request_id")) == "" {
		externalError(w, 400, "invalid_arguments", "Answer the pending question with its query_id, request_id and answers.")
		return
	}
	// Check and consume under the same lock; another turn cannot replace the
	// prompt between an authorization snapshot and submission.
	api.codingAgentClarificationsMu.Lock()
	p := api.codingAgentClarifications[session]
	if p == nil || !p.ask.matches(r, workspace, chat, session, query) || p.promptID != externalArg(args, "request_id") || p.ctx.Err() != nil || api.currentConversationTurnExecutionID(session) != query || clarificationAskingTurnEnded(p.askedTurn, api.clarificationTurnToken(session)) {
		api.codingAgentClarificationsMu.Unlock()
		externalError(w, 409, "input_not_pending", "That question is no longer pending for this Ask. Read your chat for its current status.")
		return
	}
	err = api.settleCodingAgentClarificationLocked(p, false, answers)
	api.codingAgentClarificationsMu.Unlock()
	if err != nil {
		externalError(w, 409, "invalid_answer", err.Error())
		return
	}
	log.Printf("[CREW_ASK_INPUT] session=%s query=%s prompt=%s status=answered", session, query, p.promptID)
	externalJSON(w, map[string]any{"workspace_id": workspace, "chat_id": chat, "query_id": query, "request_id": p.promptID, "status": "submitted"})
}

func validateCrewAskQuestions(args map[string]any) error {
	questions, err := parseCodingAgentClarification(args)
	if err != nil {
		return err
	}
	for _, q := range questions {
		text := q.Question + " " + q.Header
		for _, option := range q.Options {
			text += " " + option.Label + " " + option.Description
		}
		if agentworksclient.SecretQuestion(text) {
			return fmt.Errorf("Ask questions cannot collect passwords, tokens or other secrets. Sign in through Accounts, or add the secret in Vault from chat instead")
		}
	}
	return nil
}
