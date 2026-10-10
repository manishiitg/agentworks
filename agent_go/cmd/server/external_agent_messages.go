package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// An inbox is an address, not an access grant. Recheck both the authenticated
// caller and the peer's current product/token selection on every operation.
func (api *StreamingAPI) externalMessagePeer(ctx context.Context, claims *UserClaims, inboxID string, send bool) (agentMessageEndpoint, error) {
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		return agentMessageEndpoint{}, err
	}
	for _, conversation := range store.Conversations {
		if conversation.ID != inboxID {
			continue
		}
		for side, endpoint := range conversation.Endpoints {
			if endpoint.External && endpoint.UserID == claims.UserID && endpoint.Kind == triggerCallerUser && endpoint.ID == claims.UserID {
				peer := conversation.Endpoints[1-side]
				if err := api.externalAuthorizeMessagePeer(ctx, claims, peer, send); err != nil {
					return agentMessageEndpoint{}, err
				}
				return peer, nil
			}
		}
	}
	return agentMessageEndpoint{}, fmt.Errorf("conversation unavailable or access denied")
}

func (api *StreamingAPI) externalAuthorizeMessagePeer(ctx context.Context, claims *UserClaims, peer agentMessageEndpoint, send bool) error {
	token := claims.AccessToken
	denied := fmt.Errorf("conversation unavailable or access denied")
	if peer.External {
		return denied
	}
	if peer.Profile == codeproduct.ProfileID {
		if peer.UserID != claims.UserID || !externalCodeRunAllowed(claims) {
			return denied
		}
		binding, _, ok := api.externalCodeRunOwnProject(ctx, claims.UserID, peer.ID)
		if !ok || normalizeTrackedWorkspacePath(agentProfileRuntimeWorkspace(claims.UserID, binding.WorkspacePath)) != normalizeTrackedWorkspacePath(peer.Path) {
			return denied
		}
		chats, err := api.externalCodeRunChats(ctx, claims.UserID, peer.ID, binding)
		if err != nil {
			return denied
		}
		for _, chat := range chats {
			if chat.SessionID == peer.Session && chat.Key == peer.ChatKey {
				if send && api.codeLocalSession(chat.SessionID) {
					return fmt.Errorf("Local Code chats cannot receive external agent messages")
				}
				return nil
			}
		}
		return denied
	}
	if peer.Kind == triggerCallerCrew {
		if token != nil && !(token.Allows("crews:run") || !send && token.Allows("crews:read")) {
			return denied
		}
		crew, _, _, ok := api.externalCrewResolve(ctx, claims, peer.ID)
		if !ok || normalizeTrackedWorkspacePath(agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)) != normalizeTrackedWorkspacePath(peer.Path) {
			return denied
		}
		return nil
	}
	if peer.Kind == triggerCallerWorkflow {
		if token != nil && (!token.AllowsWorkflow(peer.ID) || !(token.Allows("runs:execute") || !send && token.Allows("workflows:read"))) {
			return denied
		}
		workflows, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return denied
		}
		for _, workflow := range filterWorkflowManifestsForUser(claims, workflows) {
			if workflow.Manifest != nil && workflow.Manifest.ID == peer.ID && workflow.Manifest.Kind != "relay" && normalizeTrackedWorkspacePath(workflow.WorkspacePath) == normalizeTrackedWorkspacePath(peer.Path) {
				return nil
			}
		}
	}
	return denied
}

func (api *StreamingAPI) externalMessageTarget(ctx context.Context, claims *UserClaims, args map[string]any) (triggerTarget, error) {
	crewID, workflowID, name := externalArg(args, "crew_id"), externalArg(args, "workflow_id"), strings.TrimSpace(externalArg(args, "target"))
	if (crewID != "" && workflowID != "") || (name != "" && (crewID != "" || workflowID != "")) {
		return triggerTarget{}, fmt.Errorf("choose exactly one of crew_id, workflow_id or target")
	}
	targets := []triggerTarget{}
	if workflowID == "" {
		crews, err := api.externalCrewsVisible(ctx, claims, "")
		if err != nil {
			return triggerTarget{}, err
		}
		for _, summary := range crews {
			id, label := fmt.Sprint(summary["id"]), fmt.Sprint(summary["name"])
			if crewID != id && !(crewID == "" && name != "" && (strings.EqualFold(name, label) || strings.EqualFold(name, id) || strings.EqualFold(name, "crew:"+id))) {
				continue
			}
			crew, manifest, _, ok := api.externalCrewResolve(ctx, claims, id)
			if ok {
				targets = append(targets, triggerTarget{Kind: triggerCallerCrew, Path: crew.Binding.WorkspacePath, Label: label, CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID})
			}
		}
	}
	if crewID == "" {
		workflows, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return triggerTarget{}, err
		}
		for _, workflow := range filterWorkflowManifestsForUser(claims, workflows) {
			m := workflow.Manifest
			if m == nil || m.Kind == "relay" || claims.AccessToken != nil && !claims.AccessToken.AllowsWorkflow(m.ID) {
				continue
			}
			if workflowID != m.ID && !(workflowID == "" && name != "" && (strings.EqualFold(name, m.ID) || strings.EqualFold(name, m.Label) || strings.EqualFold(name, "workflow:"+m.ID))) {
				continue
			}
			targets = append(targets, triggerTarget{Kind: triggerCallerWorkflow, Path: workflow.WorkspacePath, Label: firstNonEmptyTrimmed(m.Label, m.ID), Manifest: m})
		}
	}
	if len(targets) != 1 {
		return triggerTarget{}, fmt.Errorf("choose one accessible Crew or workflow by ID; target is unavailable or ambiguous")
	}
	target := targets[0]
	if err := api.externalAuthorizeMessagePeer(ctx, claims, targetMessageEndpoint(claims.UserID, target), true); err != nil {
		return triggerTarget{}, err
	}
	return target, nil
}

func (api *StreamingAPI) externalSendMessage(w http.ResponseWriter, r *http.Request, args map[string]any, target triggerTarget) {
	claims := GetUserFromContext(r.Context())
	inbox := strings.TrimSpace(externalArg(args, "inbox_id"))
	if inbox != "" {
		peer, err := api.externalMessagePeer(r.Context(), claims, inbox, true)
		if err != nil {
			externalError(w, 404, "not_found", err.Error())
			return
		}
		if target.Kind != "" && (peer.Kind != target.Kind || peer.ID != target.stampID() || peer.Profile != normalizeInternalProfileID(target.CrewProfile) || target.Chat != nil && peer.Session != target.Chat.SessionID) {
			externalError(w, 400, "invalid_arguments", "inbox_id belongs to another target conversation")
			return
		}
	} else if err := api.externalAuthorizeMessagePeer(r.Context(), claims, targetMessageEndpoint(claims.UserID, target), true); err != nil {
		externalError(w, 404, "not_found", err.Error())
		return
	}
	out, err := api.sendAgentMessage(context.WithoutCancel(r.Context()), claims.UserID, externalCrewCaller(claims), target, externalArg(args, "message"), inbox, externalArg(args, "submission_id"))
	if err != nil {
		externalError(w, 400, "message_refused", err.Error())
		return
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) externalAgentMessages(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	inbox := strings.TrimSpace(externalArg(args, "inbox_id"))
	switch externalArg(args, "action") {
	case "send":
		var target triggerTarget
		if inbox == "" || externalArg(args, "crew_id") != "" || externalArg(args, "workflow_id") != "" || externalArg(args, "target") != "" {
			var err error
			target, err = api.externalMessageTarget(r.Context(), claims, args)
			if err != nil {
				externalError(w, 404, "not_found", err.Error())
				return
			}
		}
		api.externalSendMessage(w, r, args, target)
	case "read":
		if inbox == "" {
			externalError(w, 400, "invalid_arguments", "inbox_id is required for a bounded conversation read")
			return
		}
		if _, err := api.externalMessagePeer(r.Context(), claims, inbox, false); err != nil {
			externalError(w, 404, "not_found", err.Error())
			return
		}
		wait := time.Duration(externalInt(args, "wait_seconds", 0)) * time.Second
		out, err := api.readAgentMessages(r.Context(), claims.UserID, externalCrewCaller(claims), inbox, externalInt(args, "after", 0), externalInt(args, "limit", 30), wait)
		if err != nil {
			externalError(w, 400, "read_refused", err.Error())
			return
		}
		// Access can be revoked while a bounded read waits; do not release its page.
		peer, err := api.externalMessagePeer(r.Context(), claims, inbox, false)
		if err != nil {
			externalError(w, 404, "not_found", err.Error())
			return
		}
		// Preserve the owner's existing masked command evidence without
		// exposing it to other callers or unrelated conversations.
		if peer.Kind == triggerCallerCrew && peer.UserID == claims.UserID {
			if commands := api.crewSessionCommands(peer.Session); len(commands) > 0 {
				out["commands_run"] = commands
			}
		}
		externalJSON(w, out)
	default:
		externalError(w, 400, "invalid_arguments", "action must be send or read")
	}
}

func externalAgentMessageDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("messages", "Send conversational agent messages or read explicit replies from your authenticated inbox. Sending returns a delivery receipt without call_id; the recipient may send any messages or none. Final chat answers are never forwarded. Read with inbox_id and a non-destructive cursor; wait_seconds is bounded to 25 seconds.", false, false, map[string]any{
		"action":        map[string]any{"type": "string", "enum": []any{"send", "read"}},
		"crew_id":       externalString("Crew ID for an initial send."),
		"workflow_id":   externalString("Workflow ID for an initial send."),
		"target":        externalString("Accessible Crew or workflow name/ID for an initial send."),
		"message":       externalString("Message to send; required for action send."),
		"inbox_id":      externalString("Inbox returned by send; required for action read, reusable for later sends."),
		"submission_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Unique ID for this message. Reuse on uncertain delivery."},
		"after":         externalInteger(0, 1000000000), "limit": externalInteger(1, 100),
		"wait_seconds": externalInteger(0, externalCrewMaxWaitSeconds),
	}, "action")
}

func (api *StreamingAPI) externalFunctionReadDetails(w http.ResponseWriter, ctx context.Context, args map[string]any, call *crewFunctionCall, out map[string]interface{}) bool {
	api.addCrewFunctionReadDetails(ctx, out, call, externalInt(args, "after", -1), externalInt(args, "message_limit", 50), externalInt(args, "after_event", -1))
	if name := strings.TrimSpace(externalArg(args, "file")); name != "" {
		page, err := readCrewFunctionOutput(ctx, call, name, externalInt(args, "offset", 0), externalInt(args, "limit", 256<<10))
		if err != nil {
			externalError(w, 400, "output_refused", err.Error())
			return false
		}
		out["file"] = page
	}
	return true
}

// The private index retains existing call IDs across restart. Listing reads it
// first, then applies the same per-Crew/per-caller filters as live records.
func loadExternalSavedFunctionCalls(ctx context.Context) {
	listing, exists, err := listWorkspaceFolder(ctx, "_system/function_calls", 1)
	if err != nil || !exists {
		return
	}
	var paths []string
	collectWorkspaceFilePaths(listing, &paths)
	for _, path := range paths {
		id := strings.TrimSuffix(path, ".json")
		if cut := strings.LastIndex(id, "/"); cut >= 0 {
			id = id[cut+1:]
		}
		if strings.HasPrefix(id, "fn-") {
			lookupCrewFunctionCall(id)
		}
	}
}
