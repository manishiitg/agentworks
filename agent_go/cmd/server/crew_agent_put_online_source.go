package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The exact paired app may introduce its own physical source before it has
// ever been shared. Initial discovery saves Only me atomically, never a brief
// shared row followed by a toggle. Existing rows retain their identity/rules.
func registerCrewPutOnlineSource(r *http.Request, project string, in crewPutOnlineRequest) error {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" || in.Provider != "agentworks" || !in.crewOfflineBinding.valid() || (in.SourceTab != macMainTab && !crewMacTabID(in.SourceTab)) {
		return fmt.Errorf("source identity unavailable")
	}
	t, err := readCrewTeamOrNew(project, claims.UserID)
	prior, existing := t.registeredAgent(claims.UserID, in.SourceTab)
	owned := existing && crewOfflineSourceOwner(t, claims.UserID, prior) == nil
	if err != nil || (!t.member(claims.UserID) && !owned) || !crewTeamAccountActive(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return fmt.Errorf("source owner unavailable")
	}
	pair, ok := crewMacPairFor(claims.UserID, project)
	if owned {
		pair, ok = crewOfflineMakerPair(t, prior)
	}
	sent := r.Header.Get(crewMacPairHeader)
	if !ok || sent == "" || subtle.ConstantTimeCompare([]byte(pair), []byte(sent)) != 1 {
		return fmt.Errorf("source pair changed")
	}
	if _, found := t.registeredAgent(claims.UserID, in.SourceTab); found {
		return nil
	}
	key := crewMacTabRow(project, crewMacTeamTab{Tab: in.SourceTab, Person: claims.UserID}).ConversationKey
	if claims.UserID == t.OwnerID {
		key = macSharedAgentChatKey(project, in.SourceTab)
	}
	private := false
	people := []crewTeamAgentsPerson{{UserID: claims.UserID, Agents: []crewTeamAgent{{Tab: in.SourceTab, ChatKey: key, Name: in.SourceName, Place: "mac", initialInTeam: &private}}}}
	if err := assignCrewTeamAgentIDs(t, people); err != nil {
		return err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s actor=%s event=source_registered", project, claims.UserID)
	return nil
}

func (api *StreamingAPI) putCrewAgentOnlineFromMac(w http.ResponseWriter, r *http.Request, in crewPutOnlineRequest) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		externalError(w, 403, "source_unavailable", "Choose your own Crew and agent.")
		return
	}
	records, err := defaultProductConversationRegistryStore().crewWorkspaceChats(r.Context(), claims.UserID, roots.Virtual)
	if err != nil {
		externalError(w, 503, "source_unavailable", "Source conversations are unavailable.")
		return
	}
	var source ProductConversationRecord
	name := ""
	for _, record := range records {
		if record.ProfileID != crewProfileID || record.WorkspacePath != roots.Virtual || isSharedThreadKey(record.ConversationKey) || hiddenFromTabStrip(record.ConversationKey) {
			continue
		}
		tab := crewAgentPublishTab(record.ConversationKey)
		if tab == "" {
			continue
		}
		identity, e := defaultProductConversationRegistryStore().agentIdentity(r.Context(), claims.UserID, crewProfileID, record.ConversationKey)
		if e != nil {
			continue
		}
		matched := in.Agent == record.ConversationKey || in.Agent == tab || (in.Agent != "" && strings.EqualFold(in.Agent, identity.Name))
		for _, saved := range readMacSharedAgents(project) {
			if saved.Tab == tab && saved.Address != "" && strings.HasSuffix(saved.Address, "/"+in.Agent) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		if source.ConversationKey != "" {
			externalError(w, 400, "ambiguous_agent", "Choose the agent by its exact tab.")
			return
		}
		source, name = record, identity.Name
	}
	if source.ConversationKey == "" {
		externalError(w, 403, "agent_not_yours", "Choose one of your own saved agents in this Crew.")
		return
	}
	pair, err := crewroot.ReadSync(project)
	if err != nil {
		externalError(w, 409, "source_unavailable", "This Crew's server binding is unavailable.")
		return
	}
	if pair == nil {
		// Reuse the explicit owner pairing action; never replace an independent pair.
		response := httptest.NewRecorder()
		api.pairedCrewReach(response, r, project, true, in.Network)
		if response.Code >= 300 {
			for k, v := range response.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(response.Code)
			w.Write(response.Body.Bytes())
			return
		}
		pair, err = crewroot.ReadSync(project)
	}
	if err != nil || pair == nil || pair.Side != "local" || pair.Editing != "local" || pair.ConnectionID == "" || pair.PeerProjectID == "" {
		externalError(w, 409, "source_unavailable", "Keep this source on your Mac; its server binding is unavailable.")
		return
	}
	connection, err := desktopConnectionFor(claims.UserID, pair.ConnectionID)
	if err != nil {
		externalError(w, 409, "network_unavailable", "Sign in to this Crew's network again.")
		return
	}
	remote := in
	remote.Agent = crewAgentPublishTab(source.ConversationKey)
	remote.SourceTab, remote.SourceName = remote.Agent, name
	remote.Network = ""
	remote.crewOfflineBinding = crewOfflineBinding{Provider: "agentworks", Session: source.SessionID, Attachment: source.ConversationID}
	var out crewPutOnlineResult
	route := "/api/crew-teams/" + url.PathEscape(pair.PeerProjectID) + "/put-online"
	if err := crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, route, remote, &out); err != nil {
		var refusal *crewRemoteHTTPError
		if errors.As(err, &refusal) {
			externalError(w, refusal.Status, refusal.Code, refusal.Message)
		} else {
			externalError(w, 409, "online_unavailable", err.Error())
		}
		return
	}
	// Explicit Put online also works for an idle saved agent; it need not wait
	// for another real turn. The same coalesced delta worker does the capture.
	api.queueCrewOfflineAppTurn(claims.UserID, source.SessionID, roots.Virtual)
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=initial_sync_queued", project, out.Agent, source.SessionID)
	externalJSON(w, out)
}

// Chat/MCP registrations call the same HTTP action, retaining their real
// claims. A scoped token is not upgraded to a desktop sign-in here.
func (api *StreamingAPI) crewPutOnlineChatAction(ctx context.Context, claims *UserClaims, project, session string, args map[string]any) (string, error) {
	current := GetUserFromContext(ctx)
	if claims == nil || current == nil || current.UserID != claims.UserID || current.AccessToken != nil || current.Scope != "" {
		return "", fmt.Errorf("put your own agent online from its Crew")
	}
	in := crewPutOnlineRequest{Agent: externalArg(args, "agent"), Audience: externalArg(args, "audience"), Network: externalArg(args, "network")}
	if people, ok := args["chosen_people"].([]any); ok {
		for _, p := range people {
			id, ok := p.(string)
			if !ok {
				return "", fmt.Errorf("chosen_people must contain person IDs from this Crew")
			}
			in.People = append(in.People, id)
		}
	}
	if in.Agent == "" {
		doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
		if err != nil {
			return "", err
		}
		for _, record := range doc.Entries {
			if record.ProfileID != crewProfileID || record.SessionID != session || crewResourceProjectID(record.ConversationKey) != project || isSharedThreadKey(record.ConversationKey) || hiddenFromTabStrip(record.ConversationKey) {
				continue
			}
			if in.Agent != "" {
				return "", fmt.Errorf("this source session is ambiguous; choose an exact agent")
			}
			in.Agent = record.ConversationKey
		}
	}
	if in.Agent == "" {
		return "", fmt.Errorf("choose one of your saved agents in this Crew")
	}
	body, _ := json.Marshal(in)
	request := httptest.NewRequest(http.MethodPost, "/api/crew-teams/"+url.PathEscape(project)+"/put-online", bytes.NewReader(body)).WithContext(ctx)
	request = mux.SetURLVars(request, map[string]string{"project": project})
	response := httptest.NewRecorder()
	api.handleCrewPutOnline(response, request)
	if response.Code >= 300 {
		return "", fmt.Errorf("%s", strings.TrimSpace(response.Body.String()))
	}
	return response.Body.String(), nil
}
