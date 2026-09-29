package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A Crew has two roles, owner and reader. Both run the same profile prompt (so
// the instruction file a CLI reads is identical for everyone sharing the
// folder); a reader's role reaches the CLI as a short block at the front of
// each message it is sent. Tools and folder guards enforce the role; this
// block only tells the model what it is so refusals are coherent.
const (
	sessionModeOpen  = "[AGENTWORKS SESSION]"
	sessionModeClose = "[/AGENTWORKS SESSION]"
	sessionModeSplit = "\n\n[USER MESSAGE]\n"
)

// crewSessionModeNotice is the block for a read-only reader of a Crew (a
// non-owner chatting in it, or a guest call into the owner's Crew).
func crewSessionModeNotice(crewRoot string) string {
	owner := ""
	if ownerID, ok := crewProjectOwnerID(crewRoot); ok {
		owner = crewOwnerDisplayName(ownerID)
	}
	who := "someone else's"
	if owner != "" {
		who = owner + "'s"
	}
	return sessionModeOpen + "\nYou are a read-only reader of " + who + " Crew. " +
		"Inspect freely (files, briefs, configuration, schedules, triggers, run history) and run the Crew's attached workflow triggers when asked. " +
		"Change nothing: no file, shell, database, schedule, trigger, selection, identity, folder or bot changes; mutation tools are not available, so do not work around that. " +
		"If the user wants something changed, offer it to the owner with `" + crewSuggestionToolName + "` (their request in their words). " +
		"This conversation is the current user's alone. Never print secret values.\n" + sessionModeClose
}

// withSessionMode puts the notice in front of a message. It is idempotent.
func withSessionMode(notice, message string) string {
	if notice == "" || strings.HasPrefix(strings.TrimSpace(message), sessionModeOpen) {
		return message
	}
	return notice + sessionModeSplit + message
}

// stripSessionMode returns the message without a leading session block.
func stripSessionMode(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, sessionModeOpen) {
		return text
	}
	if _, rest, ok := strings.Cut(trimmed, sessionModeSplit); ok {
		return rest
	}
	return text
}

type sessionModeNoticeKey struct{}

// contextWithSessionMode carries the notice to the live-input delivery, which
// sends it to the CLI but records the message as the user typed it.
func contextWithSessionMode(ctx context.Context, notice string) context.Context {
	if notice == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionModeNoticeKey{}, notice)
}

func sessionModeFromContext(ctx context.Context) string {
	notice, _ := ctx.Value(sessionModeNoticeKey{}).(string)
	return notice
}

// crewSessionModeForTurn is the notice for this turn, or "" for an owner.
func crewSessionModeForTurn(req QueryRequest, currentUserID string, resolvedProfile *resolvedAgentProfile) string {
	if resolvedProfile == nil || !isProjectProfileID(resolvedProfile.Definition.ID) {
		return ""
	}
	if isCrewReaderTurn(req, currentUserID) || crewGuestCallerForTurn(req, currentUserID) != "" {
		return crewSessionModeNotice(req.SelectedFolder)
	}
	return ""
}

// stripSessionModeFromMessage removes the block from a message read back from a
// CLI's own transcript, so the user's text is what is kept and shown.
func stripSessionModeFromMessage(message builderConversationMessage) builderConversationMessage {
	if len(message.Parts) == 0 || !strings.HasPrefix(strings.TrimSpace(message.Parts[0].Text), sessionModeOpen) {
		return message
	}
	parts := append([]builderConversationPart(nil), message.Parts...)
	parts[0].Text = stripSessionMode(parts[0].Text)
	message.Parts = parts
	return message
}

// readOnlyRefusalHint is appended to a refused write in a read-only session so
// the model explains the limit and offers the change to the owner instead of
// retrying or working around it.
func readOnlyRefusalHint(ctx context.Context) string {
	cfg := common.GetSessionShellConfig(chatSessionIDFromContext(ctx))
	if cfg == nil || !cfg.WorkflowReadOnly {
		return ""
	}
	tool := crewSuggestionToolName
	if strings.TrimSpace(cfg.WorkflowPath) != "" {
		tool = "submit_workflow_suggestion"
	}
	return ". This session is read-only, so this change is not possible: do not work around it. Offer it to the owner with `" + tool + "` (the user's request in their words)"
}

var readerDeniedToolNames = func() map[string]struct{} {
	names := map[string]struct{}{}
	for _, name := range crewReaderDeniedTools() {
		names[name] = struct{}{}
	}
	return names
}()

// readerDeniedToolRefusal answers a call to a mutating tool that a read-only
// session was never given (it is not registered, so the bridge would only say
// "not found"). It returns "" when the tool is not one of those or the session
// is not read-only. The message names the mode and where to send the change.
func readerDeniedToolRefusal(ctx context.Context, tool string) string {
	if _, denied := readerDeniedToolNames[strings.TrimSpace(tool)]; !denied {
		return ""
	}
	hint := readOnlyRefusalHint(ctx)
	if hint == "" {
		return ""
	}
	return "The tool `" + strings.TrimSpace(tool) + "` is not available: it changes the project" + hint
}

// refuseReaderDeniedTool writes the refusal in the bridge's error shape and
// reports whether it did.
func refuseReaderDeniedTool(w http.ResponseWriter, ctx context.Context, tool string) bool {
	message := readerDeniedToolRefusal(ctx, tool)
	if message == "" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": message})
	return true
}

var readOnlyShellDenials = []string{"Operation not permitted", "Permission denied", "Read-only file system"}

// withReadOnlyShellHint runs a shell tool call and, when it fails in a
// read-only session because a write was blocked, adds the mode and where to send
// the change to the command's stderr (the sandbox itself only says "Operation
// not permitted"). Other calls, and other failures, pass through untouched.
func withReadOnlyShellHint(w http.ResponseWriter, ctx context.Context, tool string, serve func(http.ResponseWriter)) {
	hint := strings.TrimPrefix(readOnlyRefusalHint(ctx), ". ")
	if strings.TrimSpace(tool) != "execute_shell_command" || hint == "" {
		serve(w)
		return
	}
	rec := &internalResponseCapture{header: http.Header{}}
	serve(rec)
	rec.body = *bytes.NewBuffer(annotateReadOnlyShellResult(rec.body.Bytes(), hint))
	copyInternalResponse(w, rec)
}

func annotateReadOnlyShellResult(body []byte, hint string) []byte {
	var outer map[string]interface{}
	if err := json.Unmarshal(body, &outer); err != nil {
		return body
	}
	resultText, _ := outer["result"].(string)
	var result map[string]interface{}
	if json.Unmarshal([]byte(resultText), &result) != nil {
		return body
	}
	stderr, _ := result["stderr"].(string)
	denied := false
	for _, marker := range readOnlyShellDenials {
		if strings.Contains(stderr, marker) {
			denied = true
			break
		}
	}
	if !denied {
		return body
	}
	result["stderr"] = strings.TrimRight(stderr, "\n") + "\n" + hint
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return body
	}
	outer["result"] = string(encodedResult)
	encoded, err := json.Marshal(outer)
	if err != nil {
		return body
	}
	return encoded
}
