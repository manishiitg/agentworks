package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-971: a fork is not general Team access to somebody's private
// conversation; only the named recipient may download it, while still joined.
func TestCrewAgentForkOnlyNamedCurrentRecipient(t *testing.T) {
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{RegistryVersion: 1, ProjectID: "room", OwnerID: "maker", On: true,
		People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentFork(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("incomplete uploads must not be downloadable")
	}
	if offer.CopyAgentID == "" || offer.CopyAgentID == agent.ID {
		t.Fatal("a fork must allocate a new identity, never transfer the maker's")
	}
	offer.Ready = true
	if err := offer.admit(team, "recipient", true, now); err != nil {
		t.Fatal(err)
	}
	for _, person := range []string{"maker", "other", ""} {
		if err := offer.admit(team, person, true, now); err == nil {
			t.Fatalf("private bundle admitted %q", person)
		}
	}
	if _, err := newCrewAgentFork(team, "other", "recipient", agent.ID, now); err == nil {
		t.Fatal("foreign chat fork admitted")
	}
	if err := offer.admit(team, "recipient", true, offer.Expires); err == nil {
		t.Fatal("expired offer admitted")
	}
	team.People[0].Invited = true
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("removed recipient admitted")
	}
	team.People[0].Invited = false
	changed := agent
	changed.OwnerID = "other"
	team.Agents[agent.ID] = changed
	if err := offer.admit(team, "recipient", true, now); err == nil {
		t.Fatal("stale owner admitted")
	}
}

// A completed upload is private to its intended recipient, and cancel/expiry
// remove the entire fork namespace including native chunks and history.
func TestCrewAgentForkPrivateStageAndCleanup(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	now := time.Now()
	agent := crewTeamRegisteredAgent{ID: "agent", OwnerID: "maker", Tab: "tab", ChatKey: "room:chat:tab"}
	team := crewTeam{RegistryVersion: 1, ProjectID: "room", OwnerID: "maker", On: true, People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent.ID: agent}}
	offer, err := newCrewAgentFork(team, "maker", "recipient", agent.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	payload := crewAgentForkPayload{Conversation: &crewHandoffConversation{ConversationID: "conversation-selected"}}
	if err = stageCrewAgentFork(team, offer, "other", payload, now); err == nil {
		t.Fatal("foreign upload accepted")
	}
	if err = stageCrewAgentFork(team, offer, "maker", payload, now); err != nil {
		t.Fatal(err)
	}
	offer, err = readCrewAgentFork(team.ProjectID, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !offer.Ready {
		t.Fatal("validated history was not made ready")
	}
	if _, err = downloadCrewAgentFork(team, offer, "other", now); err == nil {
		t.Fatal("third-party download accepted")
	}
	got, err := downloadCrewAgentFork(team, offer, "recipient", now)
	if err != nil || got.Conversation.ConversationID != payload.Conversation.ConversationID {
		t.Fatalf("recipient history: %v", err)
	}
	store, err := crewAgentForkChunkStore(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(store, "private-bytes"), []byte("selected-session"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = cancelCrewAgentFork(team, offer, "other", now); err == nil {
		t.Fatal("third-party cancel accepted")
	}
	if err = cancelCrewAgentFork(team, offer, "recipient", now); err != nil {
		t.Fatal(err)
	}
	dir, _ := crewAgentForkDirectory(team.ProjectID, offer.ID)
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("declined fork retained private data")
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	if err = expireCrewAgentForks(context.Background(), team.ProjectID, offer.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("expired fork retained private data")
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	if err = completeCrewAgentFork(team, offer, "recipient", now); err == nil {
		t.Fatal("accept removed the payload before an imported copy existed")
	}
	if _, err = prepareCrewAgentForkCopy(team, offer, "other", now); err == nil {
		t.Fatal("third party prepared a private copy")
	}
	copy, err := prepareCrewAgentForkCopy(team, offer, "recipient", now)
	if err != nil {
		t.Fatal(err)
	}
	savedTeam, err := readCrewTeam(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if saved := savedTeam.Agents[copy.ID]; saved.InTeam || saved.OwnerID != "recipient" || saved.ChatKey != crewAgentForkChatKey(offer) {
		t.Fatal("copy was not saved as recipient-owned Only me before discovery")
	}
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "maker", now); err == nil {
		t.Fatal("maker accepted the recipient's fork")
	}
	copy.InTeam = true
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "recipient", now); err == nil {
		t.Fatal("fork accepted without the required Only-me boundary")
	}
	copy.InTeam = false
	team.Agents[copy.ID] = copy
	if err = completeCrewAgentFork(team, offer, "recipient", now); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("accepted fork retained staged private data")
	}
	if source := team.Agents[agent.ID]; source.OwnerID != agent.OwnerID || source.ChatKey != agent.ChatKey || source.InTeam != agent.InTeam {
		t.Fatal("accept changed the maker's agent")
	}
}

// PLAT-746-971: the real download/list routes must never turn Team membership
// into authority to another person's full private conversation.
func TestCrewAgentForkHTTPNamedRecipientBoundary(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "recipient"}, {UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"private": false}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	var source crewTeamRegisteredAgent
	for _, agent := range team.Agents {
		source = agent
	}
	now := time.Now()
	offer, err := newCrewAgentFork(team, "owner", "recipient", source.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveCrewAgentFork(offer); err != nil {
		t.Fatal(err)
	}
	payload := crewAgentForkPayload{Conversation: &crewHandoffConversation{ConversationID: "conversation-private-copy"}}
	if err = stageCrewAgentFork(team, offer, "owner", payload, now); err != nil {
		t.Fatal(err)
	}
	request := func(person, method, op string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/crew-teams/team/agent-forks", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &UserClaims{UserID: person, Username: person}))
		vars := map[string]string{"project": "team"}
		if op != "" {
			vars["fork"], vars["op"] = offer.ID, op
		}
		w := httptest.NewRecorder()
		f.api.handleCrewAgentFork(w, mux.SetURLVars(req, vars))
		return w
	}
	if w := request("recipient", http.MethodPost, "download"); w.Code != 200 {
		t.Fatalf("named recipient: %d %s", w.Code, w.Body.String())
	}
	for _, person := range []string{"owner", "other"} {
		if w := request(person, http.MethodPost, "download"); w.Code != 403 {
			t.Fatalf("%s downloaded a private copy: %d", person, w.Code)
		}
	}
	if w := request("other", http.MethodGet, ""); w.Code != 200 || strings.Contains(w.Body.String(), offer.ID) {
		t.Fatalf("third-party list exposed private offer: %d", w.Code)
	}
	team.People[0].Invited = true
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if w := request("recipient", http.MethodPost, "download"); w.Code != 403 {
		t.Fatalf("removed member downloaded: %d", w.Code)
	}
}

// #2975: a visible Team row is not export authority. Only a currently Live,
// audience-admitted source may be copied, including when its Mac is offline.
func TestCrewAgentSelfForkSourceAndHiddenBoundary(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other", Where: "mac", MacPair: "recipient-pair"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"source": true}})}
	var source crewTeamRegisteredAgent
	for _, a := range team.Agents {
		source = a
	}
	source.Place = "mac"
	source.Name = "Source agent"
	source.Rule.Audience = crewTeamAudienceChosen
	source.Rule.People = []string{"other", "private-audience-marker"}
	team.Agents[source.ID] = source
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	saved, err := readCrewTeam(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	teamDir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: "product-source", Attachment: "conversation-source"}
	dir, err := crewOfflineWritableDirectory(team.ProjectID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The source has its own private uploaded history, not a responder transcript.
	raw, _ := json.Marshal(llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "source-only marker"))
	raw = append(raw, '\n')
	h, err := readCrewOfflineHistory(dir, b)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := chunkReader(bytes.NewReader(raw), int64(len(raw)), false, func(id string, data []byte) error { return putChunk(filepath.Join(dir, "chunks"), id, data) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = commitCrewOfflineDelta(team.ProjectID, source.ID, dir, h, crewOfflineDelta{crewOfflineBinding: b, Offset: 0, PreviousTailHash: h.TailHash, Delta: delta, SourceSize: int64(len(raw))}); err != nil {
		t.Fatal(err)
	}
	req := mux.SetURLVars(sharedSecretsRequest(http.MethodPost, "/api/crew-teams/team/agent-forks?pair=recipient-pair", "other", map[string]any{"operation": "self", "agent_id": source.ID}), map[string]string{"project": "team"})
	response := httptest.NewRecorder()
	f.api.handleCrewAgentFork(response, req)
	if response.Code != 200 {
		t.Fatalf("self fork HTTP: %d %s", response.Code, response.Body.String())
	}
	var offer crewAgentFork
	if err = json.Unmarshal(response.Body.Bytes(), &offer); err != nil {
		t.Fatal(err)
	}
	if offer.SourceBinding != nil {
		t.Fatal("receipt exposed private source binding")
	}
	offerBytes, _ := json.Marshal(offer)
	if bytes.Contains(offerBytes, []byte("private-audience-marker")) {
		t.Fatal("copy leaked maker audience")
	}
	offer, err = readCrewAgentFork(team.ProjectID, offer.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := downloadCrewAgentFork(team, offer, "other", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(payload)
	if !offer.SelfFork || !offer.Ready || offer.SnapshotAt.IsZero() || !bytes.Contains(exported, []byte("source-only marker")) {
		t.Fatalf("wrong source copy: %s", exported)
	}
	// #3194: the teammate's agent reads this same synced Live chat whether the maker's agent is online or not (owner,
	// 23 Nov), and the maker is told who read it; a Hidden chat is refused.
	if err := crewroot.WriteSync(team.ProjectID, crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "maker-pair", PeerProjectID: "maker-mac"}); err != nil {
		t.Fatal(err)
	}
	otherCtx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "other"})
	address := crewTeamAgentAddress(team.ProjectID, source.ID)
	read, err := crewTeamReadChat(otherCtx, "other", address, map[string]any{"query": "source-only"})
	if found, _ := read["result"].(chatHistorySearchResult); err != nil || len(found.Matches) != 1 {
		t.Fatalf("a Live chat was not readable: %v %v", read, err)
	}
	if notes, err := readCrewOfflineReturns(dir, b); err != nil || len(notes.Pending) != 1 || notes.Pending[0].Asker != "other" {
		t.Fatal("the maker is not told who read the chat", err)
	}
	hidden := saved
	hidden.Agents = maps.Clone(saved.Agents)
	hiddenAgent := hidden.Agents[source.ID]
	hiddenAgent.Rule.ChatShare = crewChatShareAgent
	hidden.Agents[source.ID] = hiddenAgent
	if err = fsutil.WriteJSONAtomic(filepath.Join(teamDir, "team.json"), hidden, 0600); err != nil {
		t.Fatal(err)
	}
	// Refused by the Hidden (Agent only) rule itself: the same line the Team tab shows in place of the chat.
	if _, err := crewTeamReadChat(otherCtx, "other", address, map[string]any{"query": "source-only"}); err == nil || err.Error() != crewChatPrivateLine("owner") {
		t.Fatal("a Hidden chat was read", err)
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(teamDir, "team.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}
	if state, err := crewroot.StateRoot(); err != nil || os.Remove(filepath.Join(state, "crew-sync", team.ProjectID+".json")) != nil {
		t.Fatal("could not put the maker back online", err)
	}
	// The recipient never becomes the source owner, and the original is unchanged.
	if offer.Sender != "owner" || offer.Recipient != "other" || offer.CopyAgentID == source.ID || team.Agents[source.ID].OwnerID != "owner" {
		t.Fatal("self fork transferred source ownership")
	}
	for _, change := range []string{"hidden", "only_me", "chosen_other", "removed"} {
		t.Run(change, func(t *testing.T) {
			current := team
			current.Agents = map[string]crewTeamRegisteredAgent{source.ID: source}
			a := source
			switch change {
			case "hidden":
				a.Rule.ChatShare = crewChatShareAgent
			case "only_me":
				a.InTeam = false
			case "chosen_other":
				a.Rule.Audience = crewTeamAudienceChosen
				a.Rule.People = []string{"somebody-else"}
			case "removed":
				current.People = nil
			}
			current.Agents[source.ID] = a
			if _, err := f.api.selfForkCrewAgent(current, "other", source.ID, time.Now()); err == nil {
				t.Fatal("source export admitted after sharing changed")
			}
			if _, err := downloadCrewAgentFork(current, offer, "other", time.Now()); err == nil {
				t.Fatal("existing pending copy bypassed new source rule")
			}
			if err := completeCrewAgentFork(current, offer, "other", time.Now()); err == nil {
				t.Fatal("completion bypassed new source rule")
			}
			if change == "hidden" {
				if err := crewAgentSelfForkAdmit(current, "other", a); err == nil || !strings.Contains(err.Error(), "chat is hidden") {
					t.Fatal("Hidden did not explain why copying is refused")
				}
			}
		})
	}
	if _, err := downloadCrewAgentFork(team, offer, "owner", time.Now()); err == nil {
		t.Fatal("maker read a recipient-only export")
	}
	// #3218: the maker's Send copies the same synced chat, only to someone the agent is shared with.
	sent, err := f.api.sendCrewAgentFromSyncedCopy(team, "owner", "other", source.ID, time.Now())
	if err != nil || sent.SelfFork || !sent.Ready || sent.Recipient != "other" {
		t.Fatalf("maker send from synced copy: %+v %v", sent, err)
	}
	unshared := team
	unshared.Agents = map[string]crewTeamRegisteredAgent{source.ID: source}
	only := source
	only.InTeam = false
	unshared.Agents[source.ID] = only
	if _, err := f.api.sendCrewAgentFromSyncedCopy(unshared, "owner", "other", source.ID, time.Now()); err == nil || !strings.Contains(err.Error(), "Share this agent") {
		t.Fatalf("unshared agent was sent: %v", err)
	}
	if _, err := f.api.sendCrewAgentFromSyncedCopy(team, "other", "owner", source.ID, time.Now()); err == nil {
		t.Fatal("a teammate sent someone else's agent")
	}
	// Terminal source: the export retains the native bundle, not the virtual
	// responder's runtime or its threads. The same Hidden revocation applies.
	native := source
	native.ID = crewTeamAgentIDPrefix + "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	native.Kind = "terminal"
	native.Tab = "native"
	native.ChatKey = "11111111-2222-4333-8444-555555555555"
	native.Terminal = &crewTeamTerminal{Provider: "claude", AttachmentID: "source-attachment"}
	team.Agents[native.ID] = native
	// Preserve the already provisioned home while installing the second source.
	if err = fsutil.WriteJSONAtomic(filepath.Join(teamDir, "team.json"), team, 0600); err != nil {
		t.Fatal(err)
	}
	nb := crewOfflineBinding{Provider: "claude", Session: native.ChatKey, Attachment: native.Terminal.AttachmentID}
	nd, err := crewOfflineWritableDirectory(team.ProjectID, native.ID)
	if err != nil {
		t.Fatal(err)
	}
	nh, err := readCrewOfflineHistory(nd, nb)
	if err != nil {
		t.Fatal(err)
	}
	// #3140: a native source travels whole, tool calls and results included, so a copy on another program has the full chat.
	nr := []byte(`{"type":"user","sessionId":"11111111-2222-4333-8444-555555555555","timestamp":"2026-11-17T05:00:00Z","message":{"content":"native source marker"}}` + "\n" +
		`{"type":"assistant","sessionId":"11111111-2222-4333-8444-555555555555","timestamp":"2026-11-17T05:00:01Z","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm test"}}]}}` + "\n" +
		`{"type":"user","sessionId":"11111111-2222-4333-8444-555555555555","timestamp":"2026-11-17T05:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"FAIL login"}]}}` + "\n")
	batch, err := chunkReader(bytes.NewReader(nr), int64(len(nr)), false, func(id string, data []byte) error { return putChunk(filepath.Join(nd, "chunks"), id, data) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = commitCrewOfflineDelta(team.ProjectID, native.ID, nd, nh, crewOfflineDelta{crewOfflineBinding: nb, PreviousTailHash: nh.TailHash, Delta: batch, SourceSize: int64(len(nr))}); err != nil {
		t.Fatal(err)
	}
	no, err := f.api.selfForkCrewAgent(team, "other", native.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	np, err := downloadCrewAgentFork(team, no, "other", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if np.Conversation.Native == nil || len(np.NativeFiles) == 0 || len(np.Conversation.History) != 3 || np.Conversation.History[2].Role != llmtypes.ChatMessageTypeTool {
		t.Fatalf("native source context missing: %+v", np)
	}

	posts, err := readCrewTeamActivityPosts(team.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, post := range posts.Forks {
		if post.Event == "forked" && post.OwnerID == "other" {
			found = true
		}
	}
	if !found {
		t.Fatal("maker got no Team Activity audit of the fork")
	}
	// #3577: a synced chat is kept while its agent is on the Team with a Live chat and deleted once that ends.
	if err = fsutil.WriteJSONAtomic(filepath.Join(teamDir, "team.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}
	pruneCrewSyncedChats(team.ProjectID)
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err != nil {
		t.Fatal("a current agent's synced chat was pruned", err)
	}
	// Reviewer, #3577: an unreadable people directory makes every account look inactive; that pass must delete nothing.
	working := userDirectoryStore
	userDirectoryStore = unreadableUserDirectory{working}
	pruneCrewSyncedChats(team.ProjectID)
	userDirectoryStore = working
	if _, err := os.Stat(filepath.Join(dir, "history.json")); err != nil {
		t.Fatal("an unreadable people directory pruned a kept synced chat", err)
	}
	// Owner, 28 Nov: hiding the chat deletes its server copy, like taking the agent off the Team.
	if err = fsutil.WriteJSONAtomic(filepath.Join(teamDir, "team.json"), hidden, 0600); err != nil {
		t.Fatal(err)
	}
	pruneCrewSyncedChats(team.ProjectID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a Hidden chat's synced copy was kept", err)
	}
}

// #3218 (owner, 19 Nov: "above that size limit .. we still need to transfer what we can"): a chat over the copy limit
// keeps its newest whole messages, starting at a person's message, never a cut-off reply.
func TestCrewForkJournalTailKeepsNewestFromPersonMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	var raw []byte
	var sizes []int64
	for _, m := range []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "old question"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "old answer"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "new question"),
		llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "new answer"),
	} {
		line, _ := json.Marshal(m)
		raw = append(append(raw, line...), '\n')
		sizes = append(sizes, int64(len(line))+1)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Room for the last three lines: the copy must skip "old answer" and open on "new question".
	limit := sizes[1] + sizes[2] + sizes[3]
	got, trimmed, err := readCrewForkJournalTail(file, limit)
	if err != nil || !trimmed || len(got) != 2 || got[0].Role != llmtypes.ChatMessageTypeHuman {
		t.Fatalf("tail: trimmed=%v err=%v %+v", trimmed, err, got)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if all, trimmed, err := readCrewForkJournalTail(file, int64(len(raw))); err != nil || trimmed || len(all) != 4 {
		t.Fatalf("whole chat under the limit: trimmed=%v err=%v n=%d", trimmed, err, len(all))
	}
}

// #3329: a refusal the person can act on is shown as written (a Mac recipient must accept on the Mac); any other
// error, which can carry a private path, is logged as a class only and never shown.
func TestCrewAgentForkShowsOnlyItsOwnRefusals(t *testing.T) {
	if got := crewAgentForkFailureReason(crewAgentForkRefuse("accept this copy on your computer")); got != "accept this copy on your computer" {
		t.Fatalf("refusal not kept: %q", got)
	}
	if got := crewAgentForkFailureReason(errors.New("open /Users/example/private/chat.json: no such file")); strings.Contains(got, "/Users/") {
		t.Fatalf("raw error with a path was kept: %q", got)
	}
	offer := crewAgentFork{Project: "team", Sender: "owner", Recipient: "recipient", Expires: time.Now().Add(-time.Hour)}
	var refusal crewAgentForkRefusal
	if err := offer.admit(crewTeam{ProjectID: "team", On: true}, "recipient", true, time.Now()); !errors.As(err, &refusal) {
		t.Fatalf("expired fork was not a plain refusal: %v", err)
	}
}

type unreadableUserDirectory struct{ userDirectoryBackend }

func (unreadableUserDirectory) Version(context.Context) (string, error) {
	return "", errors.New("people directory unavailable")
}
