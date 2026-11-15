package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Real signed-in HTTP: an app can sync only its own source under the current
// pair. Only-me chats are not silently uploaded unless Put online was explicit.
func TestCrewOfflineAppHTTPRequiresOwnPairAndOnlineIntent(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AUTH_SECRET", "offline-app-fixture-secret")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "agentworks,work,bullpen")
	const agent = "team-agent-11111111-2222-4333-8444-555555555555"
	row := crewTeamRegisteredAgent{ID: agent, OwnerID: "owner", Tab: "source", ChatKey: "alpha:chat:source", InTeam: true, Place: "mac", Rule: crewTeamAgentRule{ChatShare: crewChatShareAgent}}
	team := crewTeam{ProjectID: "alpha", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: map[string]crewTeamRegisteredAgent{agent: row}}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("alpha", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "current-pair", PeerProjectID: "local-alpha"}); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	env.api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	server := httptest.NewServer(AuthMiddleware(router))
	defer server.Close()
	ownerToken, err := GenerateJWT("owner", "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := GenerateJWT("other", "other", "")
	if err != nil {
		t.Fatal(err)
	}
	request := func(token, pair, method, path string, body []byte) (int, []byte) {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set(crewMacPairHeader, pair)
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, raw
	}
	binding := crewOfflineBinding{Provider: "agentworks", Session: "product-session-fixture", Attachment: "conversation-fixture"}
	statusBody, _ := json.Marshal(map[string]any{"provider": binding.Provider, "session": binding.Session, "attachment": binding.Attachment, "source_tab": "source"})
	statusRoute := "/api/crew-teams/alpha/offline-copy-status"
	if code, _ := request(ownerToken, "old-pair", "POST", statusRoute, statusBody); code != 403 {
		t.Fatal("stale app pair admitted", code)
	}
	code, raw := request(ownerToken, "current-pair", "POST", statusRoute, statusBody)
	var status struct {
		crewOfflineCursor
		Wanted bool   `json:"wanted"`
		Agent  string `json:"agent"`
	}
	if code != 200 || json.Unmarshal(raw, &status) != nil || !status.Wanted || status.Agent != agent {
		t.Fatalf("own paired source refused %d %s", code, raw)
	}
	data := []byte("{\"Role\":\"human\",\"Parts\":[{\"Text\":\"source question\"}]}\n")
	var frames bytes.Buffer
	version, err := chunkReader(bytes.NewReader(data), int64(len(data)), false, func(id string, data []byte) error { return writeChunkFrame(&frames, id, data) })
	if err != nil {
		t.Fatal(err)
	}
	in := crewOfflineDelta{crewOfflineBinding: binding, BaseRevision: status.Revision, Offset: status.Offset, PreviousTailHash: status.TailHash, Delta: version, SourceSize: int64(len(data))}
	manifest, _ := json.Marshal(in)
	route := "/api/crew-teams/alpha/agents/" + agent + "/offline-copy/"
	if code, _ = request(otherToken, "current-pair", "POST", route+"plan", manifest); code != 403 {
		t.Fatal("teammate uploaded another person's source", code)
	}
	if code, raw = request(ownerToken, "current-pair", "POST", route+"plan", manifest); code != 200 {
		t.Fatalf("plan %d %s", code, raw)
	}
	query := "?provider=agentworks&session=" + binding.Session + "&attachment=" + binding.Attachment
	if code, raw = request(ownerToken, "current-pair", "PUT", route+"chunks"+query, frames.Bytes()); code != 200 {
		t.Fatalf("chunks %d %s", code, raw)
	}
	if code, raw = request(ownerToken, "current-pair", "POST", route+"commit", manifest); code != 200 {
		t.Fatalf("commit %d %s", code, raw)
	}
	// Returned text is readable/acknowledgeable only by this exact maker pair,
	// never by the teammate who asked or a stale source installation.
	returnDir, err := crewOfflineDirectory("alpha", agent)
	if err != nil {
		t.Fatal(err)
	}
	returned := crewOfflineReturnedAnswer{ID: "returned-message", At: time.Now().UTC(), Asker: "other", Question: "teammate question", Answer: "private return fixture"}
	if err := appendCrewOfflineReturn("alpha", agent, returnDir, binding, returned); err != nil {
		t.Fatal(err)
	}
	readBody, _ := json.Marshal(binding)
	for _, denied := range []struct{ token, pair string }{{otherToken, "current-pair"}, {ownerToken, "old-pair"}} {
		if code, _ := request(denied.token, denied.pair, "POST", route+"handback", readBody); code != 403 {
			t.Fatalf("foreign/stale source read return: %d", code)
		}
	}
	if code, raw := request(ownerToken, "current-pair", "POST", route+"handback", readBody); code != 200 || !bytes.Contains(raw, []byte(returned.Answer)) {
		t.Fatalf("own return unavailable: %d %s", code, raw)
	}
	ackBody, _ := json.Marshal(map[string]any{"provider": binding.Provider, "session": binding.Session, "attachment": binding.Attachment, "ids": []string{returned.ID}})
	if code, raw := request(otherToken, "current-pair", "POST", route+"handback-ack", ackBody); code != 403 {
		t.Fatalf("teammate acknowledged return: %d %s", code, raw)
	}
	if code, raw := request(ownerToken, "current-pair", "POST", route+"handback-ack", ackBody); code != 200 {
		t.Fatalf("own acknowledgement failed: %d %s", code, raw)
	}
	if code, raw := request(ownerToken, "current-pair", "POST", route+"handback", readBody); code != 200 || bytes.Contains(raw, []byte(returned.Answer)) {
		t.Fatalf("ack retained return text: %d %s", code, raw)
	}
	row.InTeam = false
	team.Agents[agent] = row
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	code, raw = request(ownerToken, "current-pair", "POST", statusRoute, statusBody)
	if code != 200 || json.Unmarshal(raw, &status) != nil || status.Wanted {
		t.Fatalf("private source uploaded without explicit online intent: %d %s", code, raw)
	}
	dir, err := crewOfflineDirectory("alpha", agent)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "copy.json"), []byte(`{"put_online":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	team.On = false
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	code, raw = request(ownerToken, "current-pair", "POST", statusRoute, statusBody)
	if code != 200 || json.Unmarshal(raw, &status) != nil || !status.Wanted {
		t.Fatalf("owner-only source required Team on: %d %s", code, raw)
	}
	if code, _ = request(otherToken, "current-pair", "POST", route+"commit", manifest); code != 403 {
		t.Fatal("Team-off source admitted teammate upload", code)
	}
}
