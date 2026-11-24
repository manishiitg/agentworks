package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type crewOwnTerminalAgent struct {
	Activity     string `json:"activity"`
	InTeam       bool   `json:"in_team"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name"`
	// App is "desktop" for a Claude Code chat in the Claude desktop app; empty means a terminal (#3371).
	App      string `json:"app,omitempty"`
	Address  string `json:"address"`
	Online   bool   `json:"online"`
	Presence string `json:"presence"`
}

// Separate projection, not a fake writable app chat. Only this person's joined
// rows are present; merely attached sessions remain in the network directory.
func (api *StreamingAPI) handleCrewOwnTerminalAgents(w http.ResponseWriter, r *http.Request) {
	if !websiteDeviceClaims(GetUserFromContext(r.Context())) {
		externalError(w, 403, "terminal_forbidden", "Open your own project in AgentWorks.")
		return
	}
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	out := []crewOwnTerminalAgent{}
	for _, a := range t.Agents {
		if a.OwnerID != claims.UserID || a.Kind != "terminal" {
			continue
		}
		named, err := currentCrewTeamTerminal(r.Context(), t, a)
		if err != nil {
			continue
		}
		providerName := "Claude Code"
		provider := "claude-code"
		if a.Terminal.Provider == "codex" {
			providerName = "Codex"
			provider = "codex-cli"
		}
		online := namedAgentOnline(named)
		presence := "Offline"
		if online {
			presence = "online"
		}

		app := ""
		if named.Attachment != nil && named.Attachment.ClientApp == "desktop" {
			app = "desktop"
		}
		out = append(out, crewOwnTerminalAgent{InTeam: t.On && a.InTeam, ID: a.ID, Name: firstNonEmptyTrimmed(named.Label, named.Name), Provider: provider, ProviderName: providerName, App: app, Address: "agent:" + named.Name, Online: online, Presence: presence})
	}
	// One short deadline for every row together, not N sequential Mac waits.
	// A missing stream alone does not prove a process closed; timeouts stay Offline.
	statusCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var checks sync.WaitGroup
	for i := range out {
		if out[i].Online {
			continue
		}
		a := t.Agents[out[i].ID]
		checks.Add(1)
		go func(i int, a crewTeamRegisteredAgent) {
			defer checks.Done()
			payload, _ := json.Marshal(map[string]string{"provider": a.Terminal.Provider, "session": a.ChatKey, "attachment": a.Terminal.AttachmentID, "agent": a.Terminal.NamedAgentID})
			result := api.readCrewTerminalMac(statusCtx, t.ProjectID, a, crewChatCall{Operation: "native_terminal_status", Args: payload})
			var state struct {
				Online bool `json:"online"`
				Closed bool `json:"terminal_closed"`
			}
			if result.Status == 200 && json.Unmarshal(result.Body, &state) == nil {
				out[i].Online = state.Online
				if state.Online {
					out[i].Presence = "online"
				} else if state.Closed {
					out[i].Presence = "Terminal closed"
				}
			}
		}(i, a)
	}
	checks.Wait()
	// Re-read after metadata I/O; leaving or revoking membership cannot leave rows.
	current, err := terminalCrewAdmission(t.ProjectID, claims.UserID)
	if err != nil {
		externalError(w, 403, "terminal_forbidden", "This project is no longer available.")
		return
	}
	// Only keys feed the existing pending-message projection; no bodies or
	// other person's activity are returned by this owner-only endpoint.
	waitPeople := []crewTeamAgentsPerson{{UserID: claims.UserID}}
	for _, a := range current.Agents {
		if current.On && a.InTeam && a.OwnerID == claims.UserID {
			waitPeople[0].Agents = append(waitPeople[0].Agents, crewTeamAgent{ChatKey: a.ChatKey, InTeam: true})
		}
	}
	waits := crewTeamMessageWaitStarts(r.Context(), current.ProjectID, waitPeople)
	counts := map[string]int{}
	rows := out[:0]
	for _, item := range out {
		a, ok := current.Agents[item.ID]
		if !ok || a.OwnerID != claims.UserID {
			continue
		}
		if named, err := currentCrewTeamTerminal(r.Context(), current, a); err == nil {
			_, waiting := waits[a.ChatKey]
			label, _ := crewTeamTerminalState(named, nil, waiting && a.InTeam, "", true)
			item.Activity = label
			counts[label]++
			rows = append(rows, item)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s actor=%s outcome=menu_state rows=%d working=%d waiting=%d idle=%d offline=%d", current.ProjectID, claims.UserID, len(rows), counts["Working"], counts["Waiting for you"], counts["Idle"], counts["Offline"])
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"agents": rows})
}

// A thin website adapter to the normal AgentBridge mailbox. No terminal input,
// second Team transport or model-selected sender/recipient identity.
func (api *StreamingAPI) handleCrewOwnTerminalAsk(w http.ResponseWriter, r *http.Request) {
	if !websiteDeviceClaims(GetUserFromContext(r.Context())) {
		externalError(w, 403, "terminal_forbidden", "Open your own terminal agent in AgentWorks.")
		return
	}
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	a, ok := t.Agents[mux.Vars(r)["agent"]]
	if !ok || a.Kind != "terminal" || a.OwnerID != claims.UserID {
		externalError(w, 403, "terminal_forbidden", "This terminal session is not yours.")
		return
	}
	if _, err := currentCrewTeamTerminal(r.Context(), t, a); err != nil {
		externalError(w, 403, "terminal_forbidden", "This exact session's attachment changed.")
		return
	}
	var in struct {
		Message    string `json:"message"`
		Inbox      string `json:"inbox_id"`
		Submission string `json:"submission_id"`
		After      int    `json:"after"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		externalError(w, 400, "invalid_arguments", "Send a message or its original inbox.")
		return
	}
	caller := externalCrewCaller(claims)
	caller.Label = crewPersonShortName(claims.UserID)
	// Reading an inbox is still scoped to this person and this exact named target.
	if in.Inbox != "" {
		c, side, found, err := callerAgentMailboxConversation(r.Context(), in.Inbox, claims.UserID, caller)
		if err != nil || !found || side < 0 || side > 1 || c.Endpoints[1-side].UserID != claims.UserID || c.Endpoints[1-side].NamedAgentID != a.Terminal.NamedAgentID {
			externalError(w, 403, "terminal_forbidden", "This inbox does not belong to this terminal conversation.")
			return
		}
	}
	var out map[string]interface{}
	var err error
	if strings.TrimSpace(in.Message) != "" {
		out, err = api.sendAgentMessage(r.Context(), claims.UserID, caller, triggerTarget{Kind: triggerCallerUser, NamedAgentID: a.Terminal.NamedAgentID, Label: a.Name}, in.Message, in.Inbox, in.Submission)
	} else if in.Inbox != "" {
		out, err = api.readAgentMessages(r.Context(), claims.UserID, caller, in.Inbox, in.After, 30, 0)
	} else {
		externalError(w, 400, "invalid_arguments", "Write a question first.")
		return
	}
	if err != nil {
		externalError(w, 403, "terminal_message_refused", err.Error())
		return
	}
	log.Printf("[TEAM_TERMINAL_AGENT] project=%s agent=%s outcome=owner_mailbox", t.ProjectID, a.ID)
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}
