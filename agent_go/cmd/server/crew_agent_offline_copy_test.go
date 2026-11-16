package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-1024: lost acknowledgements must not duplicate source history;
// a changed cursor, different native session or malformed tail must never replace it.
func TestCrewOfflineDeltaAcknowledgementAndBinding(t *testing.T) {
	dir := t.TempDir()
	binding := crewOfflineBinding{Provider: "claude", Session: "11111111-2222-4333-8444-555555555555", Attachment: "exact-attachment"}
	h, err := readCrewOfflineHistory(dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(body string) crewOfflineDelta {
		t.Helper()
		v, e := chunkReader(strings.NewReader(body), int64(len(body)), false, func(id string, data []byte) error { return putChunk(filepath.Join(dir, "chunks"), id, data) })
		if e != nil {
			t.Fatal(e)
		}
		return crewOfflineDelta{crewOfflineBinding: binding, BaseRevision: h.Revision, Offset: h.Offset, PreviousTailHash: h.TailHash, Delta: v, SourceSize: h.Offset + v.Size}
	}
	in := prepare("{\"text\":\"first\"}\n")
	ack, err := commitCrewOfflineDelta("crew", "agent", dir, h, in)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Revision != 1 || ack.Offset != in.Delta.Size {
		t.Fatal("cursor not acknowledged")
	}
	h, err = readCrewOfflineHistory(dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := commitCrewOfflineDelta("crew", "agent", dir, h, in)
	if err != nil || replay != ack {
		t.Fatal("retry did not return original acknowledgement", err)
	}
	in = prepare("{\"text\":\"second\"}\n")
	bad := in
	bad.Offset = 0
	if _, err = commitCrewOfflineDelta("crew", "agent", dir, h, bad); err == nil {
		t.Fatal("stale offset accepted")
	}
	bad = in
	bad.PreviousTailHash = crewOfflineEmptyHash()
	if _, err = commitCrewOfflineDelta("crew", "agent", dir, h, bad); err == nil {
		t.Fatal("changed prefix accepted")
	}
	bad = in
	bad.Session = "22222222-3333-4444-8555-666666666666"
	if _, err = commitCrewOfflineDelta("crew", "agent", dir, h, bad); err == nil {
		t.Fatal("rebound session accepted")
	}
	malformed := prepare("{\"partial\":")
	if _, err = commitCrewOfflineDelta("crew", "agent", dir, h, malformed); err == nil {
		t.Fatal("partial JSONL acknowledged")
	}
	bad = in
	bad.SourceSize = in.Offset
	if _, err = commitCrewOfflineDelta("crew", "agent", dir, h, bad); err == nil {
		t.Fatal("truncated source size accepted")
	}
	ack, err = commitCrewOfflineDelta("crew", "agent", dir, h, in)
	if err != nil || ack.Revision != 2 {
		t.Fatal("valid next delta refused", err)
	}
	h, err = readCrewOfflineHistory(dir, binding)
	if err != nil || len(h.Batches) != 2 {
		t.Fatal("history not retained", err)
	}
	changed := binding
	changed.Attachment = "new-current-lease"
	continued, err := readCrewOfflineHistory(dir, changed)
	if err != nil || continued.crewOfflineCursor != h.crewOfflineCursor || len(continued.Batches) != len(h.Batches) {
		t.Fatal("same native session lost its committed cursor", err)
	}
	changed.Session = "22222222-3333-4444-8555-666666666666"
	if _, err = readCrewOfflineHistory(dir, changed); err == nil {
		t.Fatal("a different native session inherited history")
	}
	appA := crewOfflineBinding{Provider: "agentworks", Session: "product-session", Attachment: "conversation-one"}
	appB := appA
	appB.Attachment = "conversation-two"
	if sameCrewOfflineSource(appA, appB) {
		t.Fatal("app conversation identity widened with native lease continuity")
	}
	raw, _ := json.Marshal(h.crewOfflineCursor)
	if strings.Contains(string(raw), "first") || strings.Contains(string(raw), "second") || strings.Contains(string(raw), "private_tail") {
		t.Fatal("ack leaked source text")
	}
	info, err := os.Stat(filepath.Join(dir, "history.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("source record not private", err)
	}
}

// Runs inside the real OAuth/exact-attachment join fixture, not fabricated claims.
func checkCrewOfflineCopyHTTP(t *testing.T, base, token, wrong string, a agentworksclient.AgentAttachment, team crewTeam, row crewTeamRegisteredAgent) {
	t.Helper()
	request := func(auth, method, path string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, base+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+auth)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, raw
	}
	binding := crewOfflineBinding{Provider: row.Terminal.Provider, Session: a.SessionID, Attachment: a.ID}
	statusBody, _ := json.Marshal(map[string]any{"action": "offline_copy_status", "provider": binding.Provider, "session": binding.Session, "attachment_id": binding.Attachment, "agent_name": a.AgentName})
	statusPath := "/api/external/v1/agents/attachment"
	if code, _ := request(wrong, "POST", statusPath, statusBody); code != 403 {
		t.Fatal("another login read source cursor", code)
	}
	code, raw := request(token, "POST", statusPath, statusBody)
	var status struct {
		Shared bool `json:"shared"`
		crewOfflineCursor
	}
	if code != 200 || json.Unmarshal(raw, &status) != nil || !status.Shared {
		t.Fatalf("own status failed: %d %s", code, raw)
	}
	var frames bytes.Buffer
	body := "{\"type\":\"user\",\"text\":\"private source\"}\n"
	v, err := chunkReader(strings.NewReader(body), int64(len(body)), false, func(id string, data []byte) error { return writeChunkFrame(&frames, id, data) })
	if err != nil {
		t.Fatal(err)
	}
	delta := crewOfflineDelta{crewOfflineBinding: binding, BaseRevision: status.Revision, Offset: status.Offset, PreviousTailHash: status.TailHash, Delta: v, SourceSize: status.Offset + v.Size}
	manifest, _ := json.Marshal(delta)
	route := fmt.Sprintf("/api/crew-teams/%s/agents/%s/offline-copy/", team.ProjectID, row.ID)
	if code, _ = request(token, "POST", route+"plan", manifest); code != 200 {
		t.Fatal("plan failed", code)
	}
	query := fmt.Sprintf("?provider=%s&session=%s&attachment=%s", binding.Provider, binding.Session, binding.Attachment)
	if code, _ = request(token, "PUT", route+"chunks"+query, frames.Bytes()); code != 200 {
		t.Fatal("chunks failed", code)
	}
	code, raw = request(token, "POST", route+"commit", manifest)
	if code != 200 {
		t.Fatalf("commit failed %d %s", code, raw)
	}
	code, replay := request(token, "POST", route+"commit", manifest)
	if code != 200 || !bytes.Equal(raw, replay) {
		t.Fatal("lost ack duplicated source tail", code)
	}
	changed := team
	changed.Agents = map[string]crewTeamRegisteredAgent{}
	for id, r := range team.Agents {
		changed.Agents[id] = r
	}
	revoked := row
	revoked.InTeam = false
	changed.Agents[row.ID] = revoked
	if err = writeCrewTeamAgentFixture(changed); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := writeCrewTeamAgentFixture(team); e != nil {
			t.Error(e)
		}
	}()
	// Capture is automatic even after Only me; it is not transcript reach.
	if code, _ = request(token, "POST", route+"commit", manifest); code != 200 {
		t.Fatal("private native source lost its own retry", code)
	}
	if code, _ = request(token, "POST", route+"status", manifest); code != 200 {
		t.Fatal("private native source lost its own cursor", code)
	}
	delete(changed.Agents, row.ID)
	if err = writeCrewTeamAgentFixture(changed); err != nil {
		t.Fatal(err)
	}
	if code, _ = request(token, "POST", route+"commit", manifest); code != 403 {
		t.Fatal("removed source admitted retry", code)
	}
	if code, _ = request(token, "POST", route+"status", manifest); code != 403 {
		t.Fatal("removed source exposed cursor", code)
	}
}

func TestCrewOfflineCopyOAuthTargetIsExact(t *testing.T) {
	route := "/api/crew-teams/crew/agents/team-agent-11111111-2222-4333-8444-555555555555/offline-copy/"
	for _, op := range []string{"status", "plan", "commit", "handback", "handback-ack"} {
		if !cliOAuthAllowedPath("POST", route+op) {
			t.Fatal("exact ingest target refused")
		}
	}
	if !cliOAuthAllowedPath("PUT", route+"chunks") {
		t.Fatal("chunk ingest refused")
	}
	for _, path := range []string{route + "download", route + "commit/extra", route + "../file", "/api/crew-teams/crew/file", strings.Replace(route, "team-agent-", "foreign-agent-", 1) + "commit"} {
		if cliOAuthAllowedPath("POST", path) {
			t.Fatal("child scope widened beyond exact ingest target", path)
		}
	}
	if cliOAuthAllowedPath("GET", route+"status") || cliOAuthAllowedPath("GET", route+"handback") || cliOAuthAllowedPath("PUT", route+"handback-ack") || cliOAuthAllowedPath("POST", route+"chunks") {
		t.Fatal("wrong method widened child scope")
	}
}
