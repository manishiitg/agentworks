package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One online intent on the private copy ref, not a second audience or runtime.
// The source row's normal InTeam/chosen settings decide who reaches the agent.
type crewPutOnlineRequest struct {
	Agent      string   `json:"agent_key"`
	SourceTab  string   `json:"source_tab,omitempty"`
	SourceName string   `json:"source_name,omitempty"`
	Network    string   `json:"network,omitempty"`
	Audience   string   `json:"audience,omitempty"`
	People     []string `json:"chosen_people,omitempty"`
	crewOfflineBinding
}

type crewPutOnlineResult struct {
	SourceProject string                   `json:"source_project_id"`
	Agent         string                   `json:"agent_id"`
	Address       string                   `json:"source_address"`
	Home          string                   `json:"online_home"`
	Audience      string                   `json:"audience"`
	People        []crewTeamAudiencePerson `json:"chosen_people"`
	Requested     bool                     `json:"requested"`
	Note          string                   `json:"note"`
}

func (api *StreamingAPI) handleCrewPutOnline(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		externalError(w, 403, "online_forbidden", "Put one of your own agents online from its Crew.")
		return
	}
	var in crewPutOnlineRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in) != nil {
		externalError(w, 400, "invalid_online", "Choose your agent and who can ask it.")
		return
	}
	if IsLocalMode() {
		api.putCrewAgentOnlineFromMac(w, r, in)
		return
	}
	var serverSource ProductConversationRecord
	if in.Provider == "" {
		id, record, e := registerCrewPutOnlineServerSource(r.Context(), claims.UserID, mux.Vars(r)["project"], in.Agent)
		if e != nil {
			externalError(w, 403, "source_unavailable", e.Error())
			return
		}
		in.Agent, serverSource = id, record
	}
	if in.Provider == "agentworks" {
		if in.SourceTab != "" {
			if err := registerCrewPutOnlineSource(r, mux.Vars(r)["project"], in); err != nil {
				externalError(w, 403, "source_unavailable", "This source app or its pair changed.")
				return
			}
			in.Agent = in.SourceTab
		}
		t, id, e := crewOfflineOwnAgent(r.Context(), mux.Vars(r)["project"], claims.UserID, in.Agent)
		if e != nil || crewOfflineAppOwner(r, t, id, in.crewOfflineBinding) != nil {
			externalError(w, 403, "source_unavailable", "This source app or its pair changed.")
			return
		}
		in.Agent = id
	}
	out, err := api.putCrewAgentOnline(r.Context(), claims.UserID, mux.Vars(r)["project"], in)
	if err != nil {
		code, status := "online_unavailable", 409
		if errors.Is(err, errCrewTeamLastShared) {
			code, status = "last_shared_agent", 400
		}
		if errors.Is(err, errCrewTeamAgentNotYours) {
			code, status = "agent_not_yours", 403
		}
		log.Printf("[AGENT_OFFLINE_COPY] project=%s actor=%s event=refused reason=%s", mux.Vars(r)["project"], claims.UserID, code)
		externalError(w, status, code, err.Error())
		return
	}
	if serverSource.SessionID != "" {
		api.queueCrewOfflineAppTurn(claims.UserID, serverSource.SessionID, serverSource.WorkspacePath)
	}
	externalJSON(w, out)
}

// Callers already proved their own person/Builder role. A child terminal/MCP
// adapter must additionally derive its agent from the exact attached session;
// it must not forward a model-selected source as another person's authority.
func (api *StreamingAPI) putCrewAgentOnline(ctx context.Context, person, project string, in crewPutOnlineRequest) (crewPutOnlineResult, error) {
	var out crewPutOnlineResult
	team, id, err := crewOfflineOwnAgent(ctx, project, person, in.Agent)
	if err != nil {
		return out, err
	}
	a := team.Agents[id]
	if !crewTeamAccountActive(person) {
		return out, errCrewTeamAgentNotYours
	}
	audience := in.Audience
	if audience == "" {
		audience = "only_me"
		if team.On && a.InTeam {
			audience = a.Rule.audienceName()
		}
	}
	if audience != "only_me" && audience != crewTeamAudienceChosen && audience != crewTeamAudienceWholeTeam {
		return out, fmt.Errorf("choose only_me, chosen or whole_team")
	}
	if !team.On && audience != "only_me" {
		return out, fmt.Errorf("Turn on Team before choosing teammates; Only me works without it.")
	}
	// The normal setter owns the last-shared rule, including this new action.
	if team.On {
		if audience == "only_me" {
			if a.InTeam {
				if err = setCrewTeamAgentChoice(ctx, project, person, id, false); err != nil {
					return out, err
				}
			}
		} else {
			if _, err = setCrewTeamAgentAudience(ctx, project, person, id, audience, in.People, true); err != nil {
				return out, err
			}
			if !a.InTeam {
				if err = setCrewTeamAgentChoice(ctx, project, person, id, true); err != nil {
					return out, err
				}
			}
		}
	}
	home, err := api.ensureCrewTeamOnlineHome(ctx, project, "put_online", id)
	if err != nil {
		return out, err
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return out, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	latest, err := readCrewTeam(project)
	if err != nil || latest.OnlineHome != home || ownerOfCrewProjectID(project) != team.OwnerID {
		return out, errCrewTeamAgentNotYours
	}
	current, ok := latest.Agents[id]
	if !ok || current.OwnerID != person || current.ChatKey != a.ChatKey || current.Tab != a.Tab || crewOfflineSourceOwner(latest, person, current) != nil {
		return out, errCrewTeamAgentNotYours
	}
	binding := in.crewOfflineBinding
	if current.Terminal != nil {
		named, e := currentCrewTeamTerminalWithPair(ctx, latest, current, func() (string, bool) { return crewOfflineMakerPair(latest, current) })
		if e != nil || named.Attachment == nil {
			return out, fmt.Errorf("this source session's attachment changed")
		}
		binding = crewOfflineBinding{Provider: current.Terminal.Provider, Session: current.ChatKey, Attachment: current.Terminal.AttachmentID}
	} else if !binding.valid() || binding.Provider != "agentworks" {
		// Server-run source records are looked up under their actual owner.
		doc, e := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(person))
		if e != nil {
			return out, e
		}
		record, found := doc.Entries[productConversationRegistryEntryKey(crewProfileID, current.ChatKey)]
		if !found || record.ProfileID != crewProfileID || crewResourceProjectID(record.ConversationKey) != project {
			return out, fmt.Errorf("save this agent's conversation before putting it online")
		}
		binding = crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	}
	if !binding.valid() {
		return out, fmt.Errorf("this source conversation is unavailable")
	}
	copyDir, err := crewOfflineDirectory(project, id)
	if err != nil {
		return out, err
	}
	// Preserve the execution adapter's fields. This is the same source mutex
	// its final refresh write must hold; neither writer can lose online intent.
	ref := map[string]json.RawMessage{}
	raw, err := os.ReadFile(filepath.Join(copyDir, "copy.json"))
	if err == nil {
		if err = json.Unmarshal(raw, &ref); err != nil {
			return out, err
		}
	} else if !os.IsNotExist(err) {
		return out, err
	}
	ref["put_online"] = json.RawMessage("true")
	ref["home"], _ = json.Marshal(home)
	ref["binding"], _ = json.Marshal(binding)
	if err = fsutil.WriteJSONAtomic(filepath.Join(copyDir, "copy.json"), ref, 0600); err != nil {
		return out, err
	}
	out = crewPutOnlineResult{SourceProject: project, Agent: id, Address: crewTeamAgentBridgeAddress(person, id), Home: home, Audience: audience, People: latest.audiencePeople(current.Rule), Requested: true, Note: "Online preparation requested. It runs with this online home's AI accounts and shared files, not your personal logins or private working folders."}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s actor=%s home=%s event=put_online_requested audience=%s", project, id, person, home, audience)
	publishCrewPanelChanged(project)
	return out, nil
}
