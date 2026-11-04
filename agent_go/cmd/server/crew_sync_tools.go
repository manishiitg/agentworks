package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// "Reach it from your phone and the web" from chat (owner, 3 Nov): on goes online on the one saved sign-in, or on the
// network the person named when there are several; off removes the online copy. The same Mac actions as the switch.
func (api *StreamingAPI) pairedCrewReach(w http.ResponseWriter, r *http.Request, project string, on bool, network string) {
	claims := GetUserFromContext(r.Context())
	binding, err := crewroot.ReadSync(project)
	if err != nil {
		externalError(w, 409, "sync_unavailable", err.Error())
		return
	}
	connection := ""
	if binding != nil {
		connection = binding.ConnectionID
	}
	if on && binding == nil {
		saved, err := readDesktopConnections(claims.UserID)
		if err != nil || len(saved) == 0 {
			externalError(w, 409, "sign_in_first", "Sign in to a network in the AgentWorks app first.")
			return
		}
		names := []string{}
		for _, item := range saved {
			label := desktopConnectionLabel(item)
			names = append(names, label)
			if len(saved) == 1 || strings.EqualFold(network, label) || strings.EqualFold(network, item.Name) || network == item.ID {
				connection = item.ID
			}
		}
		if connection == "" {
			externalError(w, 400, "choose_network", "Which network? "+strings.Join(names, ", ")+". Ask the person, then call reach_on with network.")
			return
		}
	}
	if !on && binding == nil {
		externalJSON(w, map[string]any{"reach": "off"})
		return
	}
	action := map[bool]string{true: "relay", false: "offline"}[on]
	request := r.Clone(r.Context())
	request.Method = http.MethodPost
	data, _ := json.Marshal(map[string]string{"action": action})
	request.Body = io.NopCloser(bytes.NewReader(data))
	request = mux.SetURLVars(request, map[string]string{"project": project, "connection": connection})
	api.runDesktopCrewSync(w, request, false)
}

// desktopConnectionLabel is how a saved sign-in is named to people: its server's host, such as agents.example.com.
func desktopConnectionLabel(item desktopConnection) string {
	if parsed, err := url.Parse(item.Server); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return item.Name
}

// Pairing delegates only these project-bound operations. It does not grant
// tools the desktop connection proxy, arbitrary remote routes or credentials.
func (api *StreamingAPI) pairedCrewSyncAction(w http.ResponseWriter, r *http.Request, id, action, network string) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, id)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	if action == "" || action == "status" {
		out, err := api.crewSync(r.Context(), claims, id, crewSyncRequest{Action: "status"})
		if err != nil {
			externalError(w, 409, "sync_unavailable", err.Error())
			return
		}
		externalJSON(w, out)
		return
	}
	if action == "reach_on" || action == "reach_off" {
		api.pairedCrewReach(w, r, roots.ID, action == "reach_on", network)
		return
	}
	if action != "server" {
		externalError(w, 400, "invalid_action", "Choose status, reach_on, reach_off, server (move online), links, agents, share or unshare.")
		return
	}
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil || binding.Side != "local" {
		externalError(w, 409, "pair_first", "This Crew is not ready to move yet. Open it in the Mac app once while you are signed in.")
		return
	}
	data, _ := json.Marshal(map[string]string{"action": action})
	request := r.Clone(r.Context())
	request.Method = http.MethodPost
	request.Body = io.NopCloser(bytes.NewReader(data))
	request = mux.SetURLVars(request, map[string]string{"project": roots.ID, "connection": binding.ConnectionID})
	api.runDesktopCrewSync(w, request, false)
}

func (api *StreamingAPI) registerCrewSyncChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string) error {
	if !IsLocalMode() || readOnly || !websiteDeviceClaims(claims) {
		return nil
	}
	gate.Declare("manage_crew_sync")
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "This Crew's Access page, from chat (the owner only, on this Mac). status: on this Mac or online. reach_on: turn on Use from phone and web (the person's words for it) while the Mac is on (with several networks, pass network; if unsure, ask the person which one). reach_off: only in the app on this Mac; removes its empty online copy. server: move it online (one-way; tell the person, get their yes, then pass confirmed=true). links: the website and phone link, the address for ChatGPT or Claude, and where Slack is set up. agents: this Crew's agents (Main and its tabs) with purpose and who can message each. share: let people message one agent (agent by name; audience team (My team), everyone (the company), or chosen with Team members by name or email; ask the person when a name matches several). It opens the agent to those people and their own AI agents at its address; the agent needs a purpose first (set_my_agent_identity in that tab). It only answers their questions unless can_make_changes=true. unshare: back to only you. Needs Use from phone and web on. Say \"On this Mac\", \"Offline\" and \"Online\" to the person; never server, sync or relay.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "reach_on", "reach_off", "server", "links", "agents", "share", "unshare"}}, "network": map[string]any{"type": "string", "description": "For reach_on with several networks: the network's name, as the person said it."}, "confirmed": map[string]any{"type": "boolean", "description": "For server: the person said yes to the one-way move in this chat."}, "agent": map[string]any{"type": "string", "description": "For share and unshare: the agent's name as listed by agents (Main for the main chat)."}, "audience": map[string]any{"type": "string", "enum": []string{"team", "everyone", "chosen"}}, "people": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For share with chosen: people's names or emails."}, "can_make_changes": map[string]any{"type": "boolean", "description": "For share: false (default) it only answers their questions; true it can change files, run commands and use the owner's apps and logins for them."}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			return "", err
		}
		switch action := externalArg(args, "action"); action {
		case "links", "agents", "share", "unshare":
			return api.crewAccessChatAction(ctx, manifest.ID, action, args)
		case "server":
			// One-way, like the screen's confirm: the person says yes in chat first.
			if confirmed, _ := args["confirmed"].(bool); !confirmed {
				return "", fmt.Errorf("Moving online is one-way: it will live online from then on and cannot move back to this Mac (a backup copy of the folder stays here). Tell the person this, ask for a yes, then call again with confirmed=true")
			}
		}
		request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, caller))
		response := httptest.NewRecorder()
		api.pairedCrewSyncAction(response, request, manifest.ID, externalArg(args, "action"), externalArg(args, "network"))
		if response.Code >= 400 {
			return "", fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
		}
		return strings.TrimSpace(response.Body.String()), nil
	}, 5*time.Minute, "workspace_advanced")
}
