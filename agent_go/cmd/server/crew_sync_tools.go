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

func (api *StreamingAPI) crewAgentPublishChatAction(ctx context.Context, claims *UserClaims, project string, args map[string]any) (string, error) {
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "local" {
		return "", fmt.Errorf("connect this Crew to its server in Mac online first")
	}
	connection, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
	if err != nil {
		return "", err
	}
	rows, err := api.macAgentRows(ctx, claims, project)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(externalArg(args, "agent"))
	selection := crewAgentPublishSelection{AccountID: externalArg(args, "account_id")}
	for _, row := range rows {
		if row.Tab == name || strings.EqualFold(row.Name, name) {
			if selection.Tab != "" {
				return "", fmt.Errorf("more than one agent has that name; choose its tab from agents")
			}
			selection.Tab = row.Tab
		}
	}
	if selection.Tab == "" {
		return "", fmt.Errorf("choose this Crew's agent by name from agents")
	}
	if raw, ok := args["paths"].([]any); ok {
		for _, item := range raw {
			p, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("paths must be folder or file names")
			}
			selection.Paths = append(selection.Paths, p)
		}
	}
	action := externalArg(args, "action")
	if action == "publish_agent" {
		if confirmed, _ := args["confirmed"].(bool); !confirmed {
			return "", fmt.Errorf("show the publish_preview first, including files, callers and account; after the person confirms the one-way move, call publish_agent with its transfer_id and confirmed=true")
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, claims))
	response := httptest.NewRecorder()
	api.runDesktopCrewAgentPublish(response, request, connection, project, action, externalArg(args, "transfer_id"), &selection)
	if response.Code >= 400 {
		return "", fmt.Errorf("%s", laptopActionErrorText(response.Body.String()))
	}
	return strings.TrimSpace(response.Body.String()), nil
}

func (api *StreamingAPI) registerCrewSyncChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string) error {
	if !IsLocalMode() || readOnly || !websiteDeviceClaims(claims) {
		return nil
	}
	gate.Declare("manage_crew_sync")
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "This Crew's Access page, from chat (the owner only, on this Mac). publish_preview: review moving one named agent permanently to the server with its native chat, memory, skills, selected paths and callers; paths are relative to this Crew (empty means no working files; dot means all). The response gives actual size, callers, own server accounts and transfer_id. publish_agent: after the person confirms that preview, pass the transfer_id and confirmed=true. Other agents stay on the Mac. Missing login: use the existing server Providers page, never copy credentials. status: on this Mac or online. reach_on: turn on Use from phone and web (the person's words for it) while the Mac is on (with several networks, pass network; if unsure, ask the person which one). reach_off: only in the app on this Mac; removes its empty online copy and turns the Team off too (its people and shared folder are kept for when it is turned on again). server: move it online (one-way; tell the person, get their yes, then pass confirmed=true). links: the website and phone link, the address for ChatGPT or Claude, and where Slack is set up. agents: this Crew's agents (Main and its tabs) with purpose and whether each is In team. share: put one agent In team (agent by name): everyone in this Crew's Team sees it and its chat live and their agents can message it; it needs a purpose and icon first (set_my_agent_identity in that tab; Main uses the Crew's). It only answers their questions unless can_make_changes=true. public: make one agent Public on AgentBridge (any agent on the server can find and message it; it does not appear on anyone's website; it stays on this Mac); only_my_agents: take that back. share and unshare are about people and leave Public as it is. unshare: back to Only me. team_all: All agents in team (on=true or false); on puts every agent with a purpose and icon In team now and as they get one. team: the Team switch (on=true or false); on also turns on Mac online when it is off (with several networks, ask the person which one and use reach_on first). On shares every agent that has a purpose and icon with the team and, when no shared folder is chosen, makes an empty team/ folder and shares it; the reply names agents still needing a purpose. Off leaves only the owner and takes every agent out of the team (people stay saved); Public on AgentBridge stays. People and the folder are team_sync configure. While this Mac is off, a shared agent's team copy on the server answers people (always; there is no setting); this is separate from permanent Publish. Use the Access page names Mac online, Publish and Team. Describe execution as On this Mac or On the server.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "reach_on", "reach_off", "server", "links", "agents", "share", "unshare", "public", "only_my_agents", "team", "team_all", "publish_preview", "publish_agent"}}, "on": map[string]any{"type": "boolean", "description": "For team: true turns the Team on, false off."}, "paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For publish_preview: paths inside this Crew; empty for memory/skills only, dot for all working files."}, "account_id": map[string]any{"type": "string", "description": "Own server account returned by publish_preview; required only when choosing among accounts."}, "transfer_id": map[string]any{"type": "string", "description": "For publish_agent: the exact reviewed transfer from publish_preview."}, "network": map[string]any{"type": "string", "description": "For reach_on with several networks: the network's name, as the person said it."}, "confirmed": map[string]any{"type": "boolean", "description": "For server: the person said yes to the one-way move in this chat."}, "agent": map[string]any{"type": "string", "description": "For share and unshare: the agent's name as listed by agents (Main for the main chat)."}, "can_make_changes": map[string]any{"type": "boolean", "description": "For share: false (default) it only answers their questions; true it can change files, run commands and use the owner's apps and logins for them."}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			return "", err
		}
		switch action := externalArg(args, "action"); action {
		case "publish_preview", "publish_agent":
			return api.crewAgentPublishChatAction(ctx, caller, manifest.ID, args)
		case "links", "agents", "share", "unshare", "public", "only_my_agents":
			return api.crewAccessChatAction(ctx, manifest.ID, action, args)
		case "team_all":
			on, ok := args["on"].(bool)
			if !ok {
				return "", fmt.Errorf("say on=true or on=false")
			}
			result, err := api.setCrewTeamAll(ctx, caller, manifest.ID, on)
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(result)
			return string(raw), nil
		case "team":
			on, ok := args["on"].(bool)
			if !ok {
				return "", fmt.Errorf("say on=true to turn the Team on or on=false to turn it off")
			}
			result, err := api.switchCrewTeam(ctx, caller, manifest.ID, on, "")
			if err != nil {
				return "", err
			}
			raw, _ := json.Marshal(result)
			return string(raw), nil
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
	}, 15*time.Minute, "workspace_advanced")
}
