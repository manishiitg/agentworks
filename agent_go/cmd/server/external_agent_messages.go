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
	return api.externalMessagePeerForCaller(ctx, claims, externalCrewCaller(claims), inboxID, send)
}

func (api *StreamingAPI) externalMessagePeerForCaller(ctx context.Context, claims *UserClaims, caller triggerLinkCaller, inboxID string, send bool) (agentMessageEndpoint, error) {
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return agentMessageEndpoint{}, err
	}
	for _, conversation := range store.Conversations {
		if conversation.ID != inboxID {
			continue
		}
		for side, endpoint := range conversation.Endpoints {
			if endpoint.External && messageEndpointMatches(endpoint, claims.UserID, caller) {
				peer := conversation.Endpoints[1-side]
				if err := api.externalAuthorizeMessagePeer(ctx, claims, peer, send); err != nil {
					return agentMessageEndpoint{}, err
				}
				return peer, nil
			}
		}
	}
	for _, conversation := range store.Expired {
		if conversation.ID != inboxID || time.Since(conversation.ExpiredAt) > agentConversationIdleKeep {
			continue
		}
		for side, endpoint := range conversation.Endpoints {
			if endpoint.External && messageEndpointMatches(endpoint, claims.UserID, caller) {
				if err := api.externalAuthorizeMessagePeer(ctx, claims, conversation.Endpoints[1-side], send); err != nil {
					return agentMessageEndpoint{}, err
				}
				return agentMessageEndpoint{}, fmt.Errorf("%w: inbox was removed by retention or capacity cleanup; start a new conversation", errCommunicationExpired)
			}
		}
	}
	return agentMessageEndpoint{}, fmt.Errorf("conversation unavailable or access denied")
}

func (api *StreamingAPI) externalAuthorizeMessagePeer(ctx context.Context, claims *UserClaims, peer agentMessageEndpoint, send bool) error {
	token := claims.AccessToken
	denied := fmt.Errorf("conversation unavailable or access denied")
	if peer.External {
		if peer.UserID != claims.UserID && peer.ShareID != "" {
			return api.externalAuthorizeSharedPeer(ctx, claims, peer, send)
		}
		if peer.UserID != claims.UserID || !namedAgentOperationAllowed(claims, send) {
			return denied
		}
		if peer.NamedAgentID != "" {
			if _, err := namedAgentByID(ctx, peer.UserID, peer.NamedAgentID); err != nil {
				return denied
			}
		}
		return nil
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
	return api.externalMessageTargetForOperation(ctx, claims, args, true)
}

func (api *StreamingAPI) externalMessageTargetForOperation(ctx context.Context, claims *UserClaims, args map[string]any, send bool) (triggerTarget, error) {
	crewID, workflowID, name := externalArg(args, "crew_id"), externalArg(args, "workflow_id"), strings.TrimSpace(externalArg(args, "target"))
	if (crewID != "" && workflowID != "") || (name != "" && (crewID != "" || workflowID != "")) {
		return triggerTarget{}, fmt.Errorf("choose exactly one of crew_id, workflow_id or target")
	}
	agentTarget := func(agentName string) (triggerTarget, error) {
		if !namedAgentOperationAllowed(claims, send) {
			return triggerTarget{}, fmt.Errorf("connection lacks messaging access")
		}
		senderAgentID := ""
		if sender := externalArg(args, "agent_name"); sender != "" {
			if a, err := namedAgentForClaims(ctx, claims, sender); err == nil {
				senderAgentID = a.ID
			}
		}
		return resolveNamedMessageAgent(ctx, claims.UserID, agentName, senderAgentID)
	}
	if strings.HasPrefix(strings.ToLower(name), "agent:") {
		return agentTarget(name[len("agent:"):])
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
	if len(targets) == 0 && crewID == "" && workflowID == "" && name != "" && !strings.Contains(name, ":") {
		// A bare name that is no Crew or workflow is an agent when one of the caller's agents has that name.
		if target, err := agentTarget(name); err == nil {
			return target, nil
		}
	}
	if len(targets) != 1 {
		return triggerTarget{}, fmt.Errorf("no single accessible Crew, workflow or agent matches; use agent:NAME for an agent (or group:NAME for a group), or choose a Crew or workflow by ID")
	}
	target := targets[0]
	if err := api.externalAuthorizeMessagePeer(ctx, claims, targetMessageEndpoint(claims.UserID, target), send); err != nil {
		return triggerTarget{}, err
	}
	return target, nil
}

// externalOwnerAuthority reports whether a message to a Crew over this connection may start the Crew owner's own turn
// (856-59): the connection is the person's own (not bound to one registered agent), may write Crews, is not narrowed to
// other Crews or to a folder guard, and the Crew is one it may use. A read-only, run-only, folder-capped, agent-bound or
// Crew-narrowed connection never qualifies; whether the person owns the Crew is checked again at delivery.
func externalOwnerAuthority(claims *UserClaims, target triggerTarget) bool {
	if claims == nil || claims.AccessToken == nil || target.Kind != triggerCallerCrew {
		return false
	}
	token := claims.AccessToken
	return strings.TrimSpace(token.BoundAgentName) == "" && strings.TrimSpace(token.BoundAgentID) == "" &&
		token.Allows("crews:write") && token.Allows("crews:run") && token.AllowsCrew(target.CrewID) && token.FileGuard == nil
}

func (api *StreamingAPI) externalSendMessage(w http.ResponseWriter, r *http.Request, args map[string]any, target triggerTarget) {
	claims := GetUserFromContext(r.Context())
	caller, err := api.externalMessageCaller(r.Context(), claims, args)
	if err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	caller.OwnerAuthority = caller.NamedAgentID == "" && externalOwnerAuthority(claims, target)
	inbox := strings.TrimSpace(externalArg(args, "inbox_id"))
	if inbox != "" {
		peer, err := api.externalMessagePeerForCaller(r.Context(), claims, caller, inbox, true)
		if err != nil {
			externalCommunicationError(w, 404, "not_found", err)
			return
		}
		if target.Kind != "" && (target.NamedAgentID != "" && peer.NamedAgentID != target.NamedAgentID || target.NamedAgentID == "" && (peer.Kind != target.Kind || peer.ID != target.stampID() || peer.Profile != normalizeInternalProfileID(target.CrewProfile) || target.Chat != nil && peer.Session != target.Chat.SessionID)) {
			externalError(w, 400, "invalid_arguments", "inbox_id belongs to another target conversation")
			return
		}
	} else if err := api.externalAuthorizeMessagePeer(r.Context(), claims, targetMessageEndpoint(claims.UserID, target), true); err != nil {
		externalCommunicationError(w, 404, "not_found", err)
		return
	}
	out, err := api.sendAgentMessage(context.WithoutCancel(r.Context()), claims.UserID, caller, target, externalArg(args, "message"), inbox, externalArg(args, "submission_id"))
	if err != nil {
		externalCommunicationError(w, 400, "message_refused", err)
		return
	}
	if caller.NamedAgentID != "" {
		out["agent_name"] = caller.Label
		out["next"] = "Accepted means stored, not received by a live session. To read use messages action=read with agent_name and inbox_id. To reply in this conversation use action=send with your agent_name and this inbox_id; omit target."
	}
	externalJSON(w, out)
}

func (api *StreamingAPI) externalAgentMessages(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	inbox := strings.TrimSpace(externalArg(args, "inbox_id"))
	switch externalArg(args, "action") {
	case "update_purpose":
		api.externalNamedAgentPurpose(w, r, args)
	case "register", "unregister":
		api.externalNamedAgentRegistration(w, r, args)
	case "ack":
		api.externalAgentAttachment(w, r, args)
	case "list_agents":
		if !namedAgentOperationAllowed(claims, false) {
			externalError(w, 403, "insufficient_scope", "Connection lacks messaging read access.")
			return
		}
		out, err := listNamedMessageAgents(withAgentUsageTimezone(r.Context(), externalArg(args, "usage_timezone")), claims.UserID)
		if err != nil {
			externalCommunicationError(w, 400, "directory_refused", err)
			return
		}
		// Chats running inside AgentWorks that this person's own agents can reach (listed only to them).
		if chats := api.hostedChatsFor(r.Context(), claims); len(chats) > 0 {
			items := make([]map[string]interface{}, 0, len(chats))
			for _, chat := range chats {
				items = append(items, chat.item())
			}
			out["chats_inside_agentworks"] = items
		}
		externalJSON(w, out)
	case "state":
		api.externalAgentMessageState(w, r, args)
	case "ping_person":
		api.externalAgentPing(w, r, args)
	case "join_group", "leave_group", "list_groups", "group_members":
		api.externalGroupAction(w, r, args)
	case "send":
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(externalArg(args, "target"))), "group:") {
			api.externalSendGroup(w, r, args)
			return
		}
		var target triggerTarget
		if inbox == "" || externalArg(args, "crew_id") != "" || externalArg(args, "workflow_id") != "" || externalArg(args, "target") != "" {
			var err error
			target, err = api.externalMessageTarget(r.Context(), claims, args)
			if err != nil {
				externalCommunicationError(w, 404, "not_found", err)
				return
			}
		}
		api.externalSendMessage(w, r, args, target)
	case "read":
		if inbox == "" && externalArg(args, "agent_name") != "" {
			out, err := api.readNamedAgentMessages(r.Context(), claims, args)
			if err != nil {
				externalCommunicationError(w, 404, "read_refused", err)
				return
			}
			externalJSON(w, out)
			return
		}
		if inbox == "" {
			externalError(w, 400, "invalid_arguments", "inbox_id is required for a bounded conversation read")
			return
		}
		caller, err := api.externalMessageCaller(r.Context(), claims, args)
		if err != nil {
			externalCommunicationError(w, 404, "not_found", err)
			return
		}
		if _, err := api.externalMessagePeerForCaller(r.Context(), claims, caller, inbox, false); err != nil {
			externalCommunicationError(w, 404, "not_found", err)
			return
		}
		wait := time.Duration(externalInt(args, "wait_seconds", 0)) * time.Second
		out, err := api.readAgentMessages(r.Context(), claims.UserID, caller, inbox, externalInt(args, "after", 0), externalInt(args, "limit", 30), wait)
		if err != nil {
			externalCommunicationError(w, 400, "read_refused", err)
			return
		}
		// Access can be revoked while a bounded read waits; do not release its page.
		if wait > 0 && caller.NamedAgentID != "" && claims.AccessToken != nil {
			live, e := agentMessageConnectionClaims(r.Context(), claims.AccessToken.ID)
			if e != nil || live.UserID != claims.UserID || !namedAgentOperationAllowed(live, false) {
				externalError(w, 404, "not_found", "Connection is no longer authorized.")
				return
			}
			claims = live
		}
		if _, err = api.externalMessageCaller(r.Context(), claims, args); err != nil {
			externalCommunicationError(w, 404, "not_found", err)
			return
		}
		peer, err := api.externalMessagePeerForCaller(r.Context(), claims, caller, inbox, false)
		if err != nil {
			externalCommunicationError(w, 404, "not_found", err)
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
		externalError(w, 400, "invalid_arguments", "action must be register, unregister, list_agents, send, read, state, ack or update_purpose; live attachment is configured by the person through the CLI")
	}
}

func externalAgentMessageDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("messages", "Register an account-owned agent_name with action=register; list_agents discovers agent:<name> addresses and unregister removes a name. Registration defaults to this connection; explicitly allowed_connection_ids can use the same name. Re-register without that field preserves the reader list. Names are addresses, not authenticated device identities. Send conversational agent messages or read explicit replies from your authenticated inbox. Read with agent_name and no inbox_id to discover first contacts across conversations; each reply_address is an inbox_id for a later send. Named reads use a mailbox cursor; conversation reads use their own cursor. Mailbox storage alone does not wake a client. An opt-in exact session attachment receives all same-account senders through the authenticated event stream; agentworks mcp serve --claude-channel exposes official native Claude notifications. Stored, admitted and processed are different: admission and processing require explicit receiver acknowledgements. Only the person configures live attachment through the signed-in CLI bridge, supplying its name and exact native session; the agent can update its own purpose with action=update_purpose; agents cannot attach through this tool. Moving the name fences its old attachment. Registration and named sends require existing run consent; read-only connections cannot register or send. An inbox belongs to your account: other authorized clients of that account holding its inbox_id can read and reply. Sending returns a delivery receipt without call_id; the recipient may send any messages or none. Final chat answers are never forwarded. Read with inbox_id and a non-destructive cursor; wait_seconds is bounded to 25 seconds. Retention or capacity cleanup can expire an inbox; start a new conversation after an expired error. action=state is an owner-only, paginated view of a selected Crew or workflow’s delivery and wakeup metadata, including errors and receiving session IDs; it does not expose message bodies.", false, false, map[string]any{
		"usage_timezone":         map[string]any{"type": "string", "description": "Optional IANA timezone for owner-only native hourly-reading daily totals (default UTC)."},
		"action":                 map[string]any{"type": "string", "enum": []any{"register", "unregister", "list_agents", "send", "read", "state", "ack", "update_purpose", "join_group", "leave_group", "list_groups", "group_members", "ping_person"}},
		"group":                  externalString("Group name for join_group, leave_group (omit to leave all) and group_members; a group is an open list of agents anyone on the server can see and join."),
		"purpose":                externalString("What this attached agent works on; 1 to 512 characters."),
		"attachment_id":          externalString("Exact attachment generation in the channel event; required for receipt acknowledgements."),
		"message_id":             externalString("Incoming message ID being explicitly acknowledged; for ping_person, a message this agent sent that is still waiting for a reply (one ping per message: the target's person gets a desktop notification)."),
		"delivery":               externalString("Adapter receipt: admitted, typed, processed or failed; processed requires an explicit receiving-agent acknowledgement."),
		"transport":              externalString("Receipt route: codex_queue, codex_hook, claude_channel, claude_hook, muse_session, tmux or mailbox."),
		"agent_name":             externalString("Account-owned registered name. Register/unregister selects it; send selects the named sender; read without inbox_id polls all incoming contacts for this name. Only its allowed connections can use it."),
		"allowed_connection_ids": map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": externalString("Active connection ID belonging to this account; register only. Defaults to the registering connection. A reconnect with a new connection ID must be explicitly allowed.")},
		"crew_id":                externalString("Crew ID for an initial send or owner-only state inspection."),
		"workflow_id":            externalString("Workflow ID for an initial send or owner-only state inspection."),
		"target":                 externalString("Accessible Crew or workflow name/ID, agent:<name> from this account's directory, agent:<owner>/<name> for an agent in a group you share, or group:<name> to send to every other agent in a group your agent belongs to, for an initial send."),
		"message":                externalString("Message to send; required for action send."),
		"inbox_id":               externalString("Inbox returned by send; required for action read, reusable for later sends."),
		"submission_id":          map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Unique ID for this message. Reuse on uncertain delivery."},
		"after":                  externalInteger(0, 1000000000), "limit": externalInteger(1, 100),
		"wait_seconds": externalInteger(0, externalCrewMaxWaitSeconds),
	}, "action")
}

func (api *StreamingAPI) externalFunctionReadDetails(w http.ResponseWriter, ctx context.Context, args map[string]any, call *crewFunctionCall, out map[string]interface{}) bool {
	api.addCrewFunctionReadDetails(ctx, out, call, externalInt(args, "after", -1), externalInt(args, "message_limit", 50), externalInt(args, "after_event", -1))
	if name := strings.TrimSpace(externalArg(args, "file")); name != "" {
		page, err := readCrewFunctionOutput(ctx, call, name, externalInt(args, "offset", 0), externalInt(args, "limit", 256<<10))
		if err != nil {
			externalCommunicationError(w, 400, "output_refused", err)
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
