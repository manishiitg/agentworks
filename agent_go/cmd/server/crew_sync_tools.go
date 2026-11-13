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
	return reg.RegisterCustomToolWithTimeout("manage_crew_sync", "This Crew's Share page, from chat (the owner only, on this Mac). run_online: when the person asks an agent to run online (or presses Run online), make its online agent (<name> · online, its own agent that runs only on the server; call it the online agent, not a copy or backup); the original stays on this Mac. Omit agent to run yourself online. You decide what the copy needs, without a preview: paths = the folders and files inside this Crew it works with (empty for its chat, memory and skills only; dot for all), automations = names of this agent's schedules, webhooks and watchers that should run online instead (they stop on this Mac; ones that start a workflow or another agent stay). Before calling, write a short handover note in your reply (what the agent is for, where its files are, which Mac paths or apps it will not have on the server); the copy keeps this conversation, note included. It starts when this reply ends and a message arrives in this chat when the copy runs online, as <name> · online. Account on the server: the person's own or one the company provides; when both exist the reply lists them, ask the person which, then retry with account_id (the choice is saved for Run online and Backup). server_account: show or change that choice (agent by name, account_id to change). No account at all: sign in on the server's Providers page or ask the admin for the company's account; never copy credentials. status: on this Mac or online. reach_on: turn on Use from phone and web (the person's words for it) while the Mac is on (with several networks, pass network; if unsure, ask the person which one). reach_off: only in the app on this Mac; removes its empty online copy. While the Team is on it is refused: the Team keeps the Crew online, so the owner turns the Team off first. Moving the whole Crew online is not a chat action: it is Move online in the Mac app (Where it lives); to put an agent online from chat, use run_online. links: the website and phone link, the address for ChatGPT or Claude, and where Slack is set up. agents: this Crew's agents (Main and its tabs) with purpose and whether each is In team. share: put one agent In team (agent by name): everyone in this Crew's Team sees it and its chat live and their agents can message it. A purpose is optional. It only answers their questions unless can_make_changes=true; when_others_ask adds the owner's own rule for others' asks. public: make one agent Public on AgentBridge (any agent on the server can find and message it; it does not appear on anyone's website; it stays on this Mac); only_my_agents: take that back. share and unshare are about people and leave Public as it is. unshare: back to Only me. New agents are In team; Main is always In team. lent: what this Mac's Vault gives the Crew that teammates' agents cannot use yet (each with a key); lent_copy key=secret:NAME copies a secret to your Vault on the server for this Crew, lent_move key=app:ID moves a signed-in app there. team: the Team switch (on=true or false; the owner only); on also turns on Mac online when it is off (with several networks, ask the person which one and use reach_on first). On shares every agent with the team and, when no shared folder is chosen, makes an empty team/ folder and shares it. Off leaves only the owner and takes every agent out of the team (people stay saved); Public on AgentBridge stays. People and the folder are team_sync configure. While this Mac is off, a shared agent's team copy on the server answers people (always; there is no setting); this is separate from permanent Publish. sync_anyway: one file of the shared folder that looks like it holds passwords or keys (path, exact, from the Sync section's skipped files) synced to everyone in the Team anyway (on=true), or taken back (on=false: removed from the shared folder, kept on this Mac); the person asks for it by name. Use the Share page names Mac online, Publish and Team. Describe execution as On this Mac or On the server.", map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "reach_on", "reach_off", "links", "agents", "share", "unshare", "public", "only_my_agents", "team", "lent", "lent_copy", "lent_move", "sync_anyway", "run_online", "server_account"}}, "key": map[string]any{"type": "string", "description": "For lent_copy and lent_move: the item's key from lent."}, "on": map[string]any{"type": "boolean", "description": "For team and sync_anyway: true on, false off."}, "path": map[string]any{"type": "string", "description": "For sync_anyway: the skipped file's path inside the shared folder."}, "paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For run_online: paths inside this Crew the online agent needs; empty for chat, memory and skills only, dot for all working files."}, "automations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "For run_online: names of this agent's schedules, webhooks and watchers that move to the online agent."}, "account_id": map[string]any{"type": "string", "description": "For run_online and server_account: the server account the person chose (their own or the company's), from the account_id the reply lists."}, "network": map[string]any{"type": "string", "description": "For reach_on with several networks: the network's name, as the person said it."}, "agent": map[string]any{"type": "string", "description": "For share, unshare and run_online: the agent's name as listed by agents (Main for the main chat)."}, "can_make_changes": map[string]any{"type": "boolean", "description": "For share: false (default) it only answers their questions; true it can change files, run commands and use the owner's apps and logins for them."}, "when_others_ask": map[string]any{"type": "string", "maxLength": whenOthersAskMaxRunes, "description": "For share: the owner's optional rule for what this agent may do when someone else asks it (\"\" clears it). An instruction, not a lock."}}, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		caller, err := api.interactiveLaptopOwner(ctx, claims, sessionID, root)
		if err != nil {
			return "", err
		}
		manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
		if err != nil {
			return "", err
		}
		switch action := externalArg(args, "action"); action {
		case "run_online":
			return api.crewRunOnlineChatAction(ctx, caller, manifest.ID, sessionID, args)
		case "server_account":
			return api.crewServerAccountChatAction(ctx, caller, manifest.ID, sessionID, args)
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
		case "lent", "lent_copy", "lent_move":
			owner, binding, connection, err := api.desktopCrewChatBinding(ctx, manifest.ID)
			if err != nil {
				return "", fmt.Errorf("turn on the Team first; it works through Mac online")
			}
			roots, err := api.crewSyncRoots(ctx, owner, manifest.ID)
			if err != nil {
				return "", err
			}
			w := httptest.NewRecorder()
			api.desktopCrewTeamLent(w, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), owner, roots, binding, connection, action, externalArg(args, "key"))
			if w.Code >= 300 {
				return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
			}
			return strings.TrimSpace(w.Body.String()), nil
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
			return "", fmt.Errorf("Moving the whole Crew online is not done from chat. To put this agent online, use run_online (the original stays on this Mac). To move the whole Crew, the owner uses Move online in the Mac app, under Where it lives")
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
