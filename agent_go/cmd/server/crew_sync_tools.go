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
	api.runDesktopCrewSync(w, request)
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
		out, err := api.crewSync(r.Context(), claims, roots.ID, crewSyncRequest{Action: "status"})
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
	externalError(w, 400, "invalid_action", "Choose status, reach_on, reach_off, links, agents, share or unshare.")
}

func (api *StreamingAPI) registerCrewSyncChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, sessionID, root string) error {
	if !IsLocalMode() || readOnly || !websiteDeviceClaims(claims) {
		return nil
	}
	gate.Declare("manage_crew_sync")
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "This project's Share page, from chat (the owner only, on this Mac). Agents are not published or put online; to keep work running while this Mac is off, put the automation online (schedules, Watchers, webhooks) or ask the Team agent. status: on this Mac or online. reach_on: turn on Use from phone and web (the person's words for it) while the Mac is on (with several networks, pass network; if unsure, ask the person which one). reach_off: only in the app on this Mac; removes its empty online copy. While the Team is on it is refused: the Team keeps the project online, so the owner turns the Team off first. links: the website and phone link, the address for ChatGPT or Claude, and where Slack is set up. agents: this project's agents (Main and its tabs) with purpose and whether each is Shared with team. share: put one agent Shared with team (agent by name): everyone in this project's Team sees it and its chat live and their agents can message it. A purpose is optional. It helps them as it helps the owner; when_others_ask adds the owner's own rule for others' asks (the only limit). public: turn on Discovery for one agent on AgentBridge (any agent on the server can find and message it; it does not appear on anyone's website; it stays on this Mac); only_my_agents: turn Discovery off. share and unshare are about people and leave Discovery as it is. unshare: back to Only me. New agents are Shared with team; Main is always Shared with team. team: the Team switch (on=true or false; the owner only); on also turns on Mac online when it is off (with several networks, ask the person which one and use reach_on first). On shares every agent with the team and, when no shared folder is chosen, makes an empty team/ folder and shares it. Off leaves only the owner and takes every agent out of the team (people stay saved); Discovery on AgentBridge stays. People and the folder are team_sync configure. While this Mac is off, a shared agent's team copy on the server answers people (always; there is no setting). sync_anyway: one file of the shared folder that looks like it holds passwords or keys (path, exact, from the Sync section's skipped files) synced to everyone in the Team anyway (on=true), or taken back (on=false: removed from the shared folder, kept on this Mac); the person asks for it by name. Use the Share page names Mac online and Team. Describe execution as On this Mac or On the server.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "reach_on", "reach_off", "links", "agents", "share", "unshare", "public", "only_my_agents", "team", "sync_anyway"}}, "on": map[string]any{"type": "boolean", "description": "For team and sync_anyway: true on, false off."}, "path": map[string]any{"type": "string", "description": "For sync_anyway: the skipped file's path inside the shared folder."}, "network": map[string]any{"type": "string", "description": "For reach_on with several networks: the network's name, as the person said it."}, "agent": map[string]any{"type": "string", "description": "For share and unshare: the agent's name as listed by agents (Main for the main chat)."}, "when_others_ask": map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "For share: the owner's optional rule for what this agent may do when someone else asks it (\"\" clears it). An instruction, not a lock."}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			return "", err
		}
		switch action := externalArg(args, "action"); action {
		case "links", "agents", "share", "unshare", "public", "only_my_agents":
			return api.crewAccessChatAction(ctx, manifest.ID, action, args)
		case "sync_anyway":
			on, ok := args["on"].(bool)
			path, _ := args["path"].(string)
			if !ok || strings.TrimSpace(path) == "" {
				return "", fmt.Errorf("say which skipped file (path) and on=true to sync it or on=false to stop")
			}
			binding, err := crewroot.ReadSync(manifest.ID)
			if err != nil || binding == nil || binding.Side != "local" {
				return "", fmt.Errorf("turn on the Team first; its shared folder syncs through Mac online")
			}
			dir, err := api.crewTeamLocalDirFor(ctx, caller.UserID, binding.ConnectionID, binding.PeerProjectID)
			if err != nil {
				return "", err
			}
			if err = setCrewTeamSyncAnyway(ctx, dir, strings.TrimSpace(path), on); err != nil {
				return "", err
			}
			raw, _ := json.Marshal(map[string]any{"path": path, "on": on, "note": map[bool]string{true: "It syncs to everyone in the Team from the next round.", false: "It leaves the shared folder; it stays on this Mac."}[on]})
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
