package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A Crew has two roles, owner (Builder) and reader (Run), with separate
// prompts/skills in private linked runtimes. The per-message notice also carries
// current restrictions into live input. Tools and folder guards enforce them.
const (
	sessionModeOpen  = "[AGENTWORKS SESSION]"
	sessionModeClose = "[/AGENTWORKS SESSION]"
	sessionModeSplit = "\n\n[USER MESSAGE]\n"
)

// crewSessionModeNotice is the block for a read-only reader of a Crew: a non-owner
// chatting in it, a guest call into the owner's Crew, or a read-only Slack/WhatsApp
// channel route. shared is true for a chat channel, where several people write to one
// conversation, so the "this conversation is yours alone" line is left out.
func crewSessionModeNotice(crewRoot, runFolder string, shared bool) string {
	owner := ""
	if ownerID, ok := crewProjectOwnerID(crewRoot); ok {
		owner = crewOwnerDisplayName(ownerID)
	}
	who := "someone else's"
	if owner != "" {
		who = owner + "'s"
	}
	scope := "This conversation is the current user's alone. "
	if shared {
		scope = "This is a shared chat channel: several people write to this conversation. "
	}
	output := "Change nothing: no file, database, schedule, trigger, selection, identity, folder or bot changes; mutation tools are not available, so do not work around that. "
	if runFolder != "" {
		where := "`" + runFolder + "`"
		if abs := cliPolicyPath(runFolder); abs != "" {
			where += " (absolute path `" + abs + "`)"
		}
		output = "Save everything a run produces (evidence, recordings, reports, results, scratch files) in this conversation's run folder " + where + ", " +
			"also in the environment variable " + crewRunDirEnv + ". It exists already and is the only place you can write; when a script writes elsewhere, point it there with an argument or environment variable rather than editing it. " +
			"Change nothing else: not the Crew's files, code, memory, skills, functions, database, schedules, triggers, selections, identity, folders or bots; those tools are not available, so do not work around that. "
	}
	return sessionModeOpen + "\nYou are in Run mode on " + who + " Crew: you run what its owner built and cannot change the Crew. " +
		"Inspect freely (files, briefs, configuration, schedules, triggers, run history), and run the Crew's functions, scripts and attached workflow triggers when asked. " +
		output +
		"If the user wants something changed, offer it to the owner with `" + crewSuggestionToolName + "` (their request in their words). " +
		scope + "Never print secret values.\n" + sessionModeClose
}

// withSessionMode puts the notice in front of a message. It is idempotent for
// the server's own notice only: a message that merely starts with a look-alike
// block (typed by the user) still gets the real notice in front of it.
func withSessionMode(notice, message string) string {
	if notice == "" || strings.HasPrefix(message, notice+sessionModeSplit) {
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

// agentSessionModeForTurn carries current access guidance into fresh and
// retained turns. Codex's built-in sandbox is distinct from platform tools.
func agentSessionModeForTurn(req QueryRequest, currentUserID, sessionID string, resolvedProfile *resolvedAgentProfile, readOnly bool) string {
	if resolvedProfile != nil && resolvedProfile.Definition.ID == sparkquillproduct.ParentProfileID && !readOnly {
		if agentProfileToolsMode(resolvedProfile) == "full" {
			return sessionModeOpen + "\nYou are in SparkQuill Parent Mode with full native tools. Use native tools within the granted workspace and the admitted platform tools for product actions. If a built-in tool reports read-only, platform tools such as execute_shell_command still enforce their own actual folder and access permissions; attempt authorised work through them and report an actual denial instead of asking the parent to enable editing from the CLI label alone.\n" + sessionModeClose
		}
		return sessionModeOpen + "\nYou are in SparkQuill Parent Mode. Use the admitted platform tools, including execute_shell_command, to create and save requested lessons in the family workspace. Codex's read-only sandbox applies to its built-in tools; it does not make the platform workspace tools viewing-only. Those tools enforce the actual folder and access permissions. Use them for authorised writes and report their actual errors if denied; do not ask the parent to enable editing merely because the CLI reports read-only.\n" + sessionModeClose
	}
	return crewSessionModeForTurn(req, currentUserID, sessionID, resolvedProfile, readOnly)
}

// crewSessionModeForTurn is the notice for this turn, or "" for an owner or editor.
//
// It follows whether the TURN is read-only, not who the caller is: a Slack or WhatsApp
// channel route with a read grant runs as the Crew's OWNER (the route belongs to them)
// but with read-only access, so "the caller is not the owner" would miss it, while tools
// and folder guards already treat it as read-only. A non-owner reader and a guest call
// are read-only turns too; they are also matched directly as a belt-and-braces check.
func crewSessionModeForTurn(req QueryRequest, currentUserID, sessionID string, resolvedProfile *resolvedAgentProfile, readOnly bool) string {
	if resolvedProfile == nil || !isProjectProfileID(resolvedProfile.Definition.ID) {
		return ""
	}
	if readOnly || isCrewReaderTurn(req, currentUserID) || crewGuestCallerForTurn(req, currentUserID) != "" {
		runFolder := crewRunFolder(agentProfileRuntimeWorkspace(currentUserID, req.SelectedFolder), sessionID)
		return crewSessionModeNotice(req.SelectedFolder, runFolder, strings.TrimSpace(req.BotPlatform) != "")
	}
	return ""
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
// is not a read-only Crew reader. The message names the mode and where to send
// the change.
func readerDeniedToolRefusal(ctx context.Context, tool string) string {
	if _, denied := readerDeniedToolNames[strings.TrimSpace(tool)]; !denied {
		return ""
	}
	// Only a Crew reader: the same tool names (perform_ui_action, secrets) are
	// legitimate in other read-only sessions such as a workflow Run chat.
	cfg := common.GetSessionShellConfig(chatSessionIDFromContext(ctx))
	if cfg == nil || !cfg.CrewReader {
		return ""
	}
	return "The tool `" + strings.TrimSpace(tool) + "` is not available: it changes the project" + readOnlyRefusalHint(ctx)
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
	where := ""
	if cfg.RunOutputPath != "" {
		where = " Outputs belong in the run folder `" + cfg.RunOutputPath + "`."
	}
	return ". This session is in Run mode, which cannot change the Crew or workflow, so this change is not possible: do not work around it." + where + " Offer the change to the owner with `" + tool + "` (the user's request in their words)"
}
