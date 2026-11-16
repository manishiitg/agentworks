package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-1045: a shared setup grants no membership, file or secret access;
// only a current own agent can fill that person's checklist, never a human,
// another member's attachment, a shared ask or the Team agent.
func TestTeamChecklistUsesCurrentMembershipFilesAndOwnAgent(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	newOwnerVaultFake(t) // setup_get checks requirement metadata, never a real Vault service.
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","products":["work"]},{"id":"other","products":["work"]},{"id":"invited","products":["work"]},{"id":"outsider","products":["work"]},{"id":"team-team","products":["work"]}]}`)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}, {UserID: "invited", Invited: true}}, Folder: crewTeamFolder{ID: "shared", Folders: []string{"team"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"one": true}, "other": {"two": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	const instructions = "Use the example service and check the result.\n"
	chunkStore, err := crewTeamChunkStore("team")
	if err != nil {
		t.Fatal(err)
	}
	put := func(body, transfer, before string) uint64 {
		t.Helper()
		file, err := chunkReader(strings.NewReader(body), int64(len(body)), false, func(id string, b []byte) error { return putChunk(chunkStore, id, b) })
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := commitCrewTeamChanges(t.Context(), "team", "owner", transfer, crewTransferDigest(transfer), []crewTeamChange{{Path: "team/setup.md", Before: before, After: &file}})
		if err != nil {
			t.Fatal(err)
		}
		return receipt.Revision
	}
	rev := put(instructions, "first", "")
	login := func(person string) *UserClaims {
		return &UserClaims{UserID: person, Username: person, RegisteredClaims: jwt.RegisteredClaims{ID: "login-" + person, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	}
	owner, member := login("owner"), login("other")
	request := func(c *UserClaims, args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(args)
		rec := httptest.NewRecorder()
		f.api.handleCrewTeamChecklists(rec, adminRequest(http.MethodPost, "/api/crew-teams/team/checklists", string(raw), c, map[string]string{"project": "team"}))
		return rec
	}
	share := map[string]any{"action": "setup_share", "setup": map[string]any{"title": "Example setup", "instruction": map[string]any{"path": "team/setup.md", "revision": rev}, "items": []any{map[string]any{"label": "Connect"}, map[string]any{"label": "Check"}}, "keys": []any{map[string]any{"name": "EXAMPLE_TOKEN"}, map[string]any{"provider": "example"}}}}
	for _, person := range []string{"outsider", "invited"} {
		if rec := request(login(person), share); rec.Code != 403 {
			t.Fatalf("%s shared without membership: %d %s", person, rec.Code, rec.Body.String())
		}
	}
	rec := request(member, share)
	if rec.Code != 200 {
		t.Fatalf("member share: %d %s", rec.Code, rec.Body.String())
	}
	var saved struct {
		ID       string `json:"setup_id"`
		Revision uint64 `json:"revision"`
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || saved.ID == "" {
		t.Fatal(rec.Body.String(), err)
	}
	// The list's counts replace per-row detail fetches; no other person's
	// checklist flags or inactive/invited member identities may leak.
	listed := request(owner, map[string]any{"action": "setup_list"})
	var roster struct {
		Setups []struct {
			People []map[string]any `json:"people"`
		} `json:"setups"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &roster) != nil || len(roster.Setups) != 1 || len(roster.Setups[0].People) != 2 {
		t.Fatalf("admitted count-only list: %d %s", listed.Code, listed.Body.String())
	}
	for _, person := range roster.Setups[0].People {
		if len(person) != 4 || (person["person_id"] != "owner" && person["person_id"] != "other") {
			t.Fatalf("list exposed more than current-member counts: %#v", person)
		}
	}

	data, err := readCrewChecklists("team")
	if err != nil {
		t.Fatal(err)
	}
	setup := data.Setups[saved.ID]
	dir, _ := crewTeamDir("team")
	raw, err := os.ReadFile(filepath.Join(dir, crewChecklistFile))
	if err != nil || bytes.Contains(raw, []byte(instructions)) {
		t.Fatal("instruction body duplicated in metadata", err)
	}
	get := map[string]any{"action": "setup_get", "setup_id": saved.ID}
	if rec = request(owner, get); rec.Code != 200 || !strings.Contains(rec.Body.String(), instructions[:20]) {
		t.Fatalf("owner get: %d %s", rec.Code, rec.Body.String())
	}
	guarded := crewWriter("other")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"other-folder"}}
	if rec = request(guarded, get); rec.Code != 403 {
		t.Fatal("a narrowed connection read instruction text", rec.Body.String())
	}
	if rec = request(guarded, map[string]any{"action": "setup_list"}); rec.Code != 200 || strings.Contains(rec.Body.String(), saved.ID) {
		t.Fatal("file guard leaked setup metadata", rec.Body.String())
	}
	// The mutation carries no person or agent ID; a fresh server attachment pins it.
	progress := map[string]any{"action": "setup_checklist", "setup_id": saved.ID, "setup_revision": saved.Revision, "instruction_revision": rev, "done": map[string]bool{setup.Items[0].ID: true}}
	if rec = request(member, progress); rec.Code != 403 {
		t.Fatal("a human filled the checklist", rec.Body.String())
	}
	register := func(c *UserClaims, name string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"action": "register", "agent_name": name, "device_id": "device", "conversation_id": "chat-" + c.UserID})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/external/v1/agents/app", bytes.NewReader(body)).WithContext(context.WithValue(t.Context(), UserContextKey, c))
		f.api.handleAppAgentRegistration(rec, req)
		if rec.Code != 200 {
			t.Fatalf("own agent registration: %d %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatal(rec.Body.String())
		}
		return out
	}
	owned := register(member, "own-agent")
	foreign := register(owner, "owner-agent")
	progress["agent_name"], progress["attachment_id"] = "owner-agent", foreign["attachment_id"]
	if rec = request(member, progress); rec.Code != 403 {
		t.Fatal("borrowed attachment changed progress", rec.Body.String())
	}
	progress["agent_name"], progress["attachment_id"] = "own-agent", owned["attachment_id"]
	if rec = request(member, progress); rec.Code != 200 {
		t.Fatalf("own agent checklist: %d %s", rec.Code, rec.Body.String())
	}
	// #2827: a source handler test missed the absent canonical catalog entries.
	// Use the real MCP discovery and call path, with the same current attachment.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, member)
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, map[string]any{"names": "workspace"})
	requireRemoteSuccess(t, spec, "Team setup schema")
	for _, action := range []string{"list", "get", "share", "edit", "checklist", "targets", "apply"} {
		if !strings.Contains(marshalStructured(t, spec), `"team_setup_`+action+`"`) {
			t.Fatalf("Team setup action missing from callable MCP schema: %s", action)
		}
	}
	progress["action"], progress["crew_id"] = "team_setup_checklist", "team"
	progress["done"] = map[string]bool{setup.Items[1].ID: true}
	result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "workspace", "arguments": progress})
	requireRemoteSuccess(t, result, "MCP own progress")
	data, _ = readCrewChecklists("team")
	p := data.Setups[saved.ID].Progress
	if len(p) != 1 || crewChecklistDone(setup, p["other"], rev) != 2 || p["other"].AgentID != owned["agent_id"] {
		t.Fatalf("person, agent or incremental flags lost: %+v", p)
	}
	delete(progress, "crew_id")
	progress["action"] = "setup_checklist"
	// Native tools use their actual current registry session, not agent arguments.
	key := "team" + codeChatSideMarker + "two"
	doc := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): {ProfileID: crewProfileID, ConversationKey: key, SessionID: "session-two", WorkspacePath: externalCommandCrewRoot}}}
	if err = defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("other"), doc); err != nil {
		t.Fatal(err)
	}
	reg := &singleToolCaptureRegistrar{}
	if err = f.api.registerCrewTeamChatTool(reg, newProductToolGate(nil), member, false, "session-two", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	progress["done"] = map[string]bool{setup.Items[0].ID: false}
	if _, err = reg.exec(context.WithValue(t.Context(), UserContextKey, member), progress); err != nil {
		t.Fatal("native own progress", err)
	}
	shared := &singleToolCaptureRegistrar{}
	if err = f.api.registerCrewTeamChatTool(shared, newProductToolGate(nil), member, false, "session-two", externalCommandCrewRoot, "owner", "team:chat:two"); err != nil {
		t.Fatal(err)
	}
	if _, err = shared.exec(context.WithValue(t.Context(), UserContextKey, member), progress); err == nil {
		t.Fatal("shared ask filled the runtime owner's checklist")
	}
	// Real admission is re-evaluated after removal, not a stored setup grant.
	team.People = nil
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if rec = request(member, get); rec.Code != 403 {
		t.Fatal("removed member retained setup access", rec.Body.String())
	}
	refused := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "workspace", "arguments": map[string]any{"action": "team_setup_get", "crew_id": "team", "setup_id": saved.ID}})
	requireRemoteError(t, refused, "removed member MCP setup read", "team_setup_refused")
	if rec = request(member, progress); rec.Code != 403 {
		t.Fatal("removed member retained checklist access", rec.Body.String())
	}
	team.People = []crewTeamPerson{{UserID: "other"}}
	team.On = false
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if rec = request(member, get); rec.Code != 403 {
		t.Fatal("off Team admitted a member", rec.Body.String())
	}
	// A valid shared document changed independently: old completion must vanish,
	// stale writes fail, and a fresh check starts with no carried-over flags.
	team.On = true
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	state, _ := readCrewTeamCurrentState("team")
	newRev := put("Recheck the changed instructions.\n", "second", state.Versions["team/setup.md"].File.Hash)
	if rec = request(member, progress); rec.Code != 403 {
		t.Fatal("old instructions marked complete", rec.Body.String())
	}
	progress["instruction_revision"] = newRev
	progress["done"] = map[string]bool{setup.Items[0].ID: true}
	if rec = request(member, progress); rec.Code != 200 {
		t.Fatal("fresh instruction check refused", rec.Body.String())
	}
	data, _ = readCrewChecklists("team")
	if got := crewChecklistDone(data.Setups[saved.ID], data.Setups[saved.ID].Progress["other"], newRev); got != 1 {
		t.Fatalf("old completion survived changed instructions: %d", got)
	}
	// Human editors cannot smuggle progress or values into metadata.
	bad := map[string]any{"action": "setup_share", "setup": map[string]any{"progress": map[string]any{"owner": true}}}
	if rec = request(owner, bad); rec.Code != 403 {
		t.Fatal("human-supplied progress accepted", rec.Body.String())
	}
	// Editing uses a compare-and-swap and requires everyone to recheck.
	edit := map[string]any{"action": "setup_edit", "setup_id": saved.ID, "setup_revision": saved.Revision, "setup": crewChecklistInput{Title: "Edited setup", Instruction: crewChecklistInstruction{Path: "team/setup.md", Revision: newRev}, Items: setup.Items}}
	if rec = request(owner, edit); rec.Code != 200 {
		t.Fatal("owner edit", rec.Body.String())
	}
	if rec = request(member, edit); rec.Code != 403 {
		t.Fatal("stale edit overwrote a newer setup", rec.Body.String())
	}
	data, _ = readCrewChecklists("team")
	if len(data.Setups[saved.ID].Progress) != 0 {
		t.Fatal("edit kept stale completion")
	}
	// A real Team home may read shared setup instructions, but even its trusted
	// runtime cannot fill personal progress. Ensure the refusal isn't missing setup.
	team, err = readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	team.OnlineHome = "home"
	if err = defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "team-home", ProjectID: "home", OwnerID: "team-team"}); err != nil {
		t.Fatal(err)
	}
	f.write(t, "Crew/team-home/.agentworks/settings/product.json", `{"schema_version":1,"product":"work","id":"home","title":"Team home"}`)
	if err = writeCrewTeamRecord(crewTeam{ProjectID: "home", OwnerID: "team-team", On: true, RosterSource: "team"}, nil, true); err != nil {
		t.Fatal(err)
	}
	// OnlineHome is backend-owned: the ordinary Team settings writer deliberately
	// ignores it. Seed that provisioned state, not a person's settings payload.
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), team, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = crewChecklistTeam(&UserClaims{UserID: "team-team"}, "team", true); err != nil {
		t.Fatal("Team home setup authority not established", err)
	}
	// A Team agent is not a person even if a caller fabricates an internal actor.
	virtual := &UserClaims{UserID: "team-team"}
	if _, err = writeCrewChecklistProgress(virtual, "team", saved.ID, crewChecklistActor{person: virtual.UserID, agent: "team-agent"}, saved.Revision, newRev, map[string]bool{setup.Items[0].ID: true}, nil); err == nil {
		t.Fatal("Team agent filled personal progress")
	}
	// Token scopes remain effective for setup edits as for other Team writes.
	narrow := &UserClaims{UserID: "other", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	if rec = request(narrow, share); rec.Code != 403 {
		t.Fatal("read connection shared a setup", rec.Body.String())
	}
}

// An MCP or skill is installed in a person's own app, so the Team never reads it: it shows only what that person's own
// agent reported for the current setup revision, and only for names the Todo lists.
func TestChecklistMcpAndSkillStatusIsWhatTheirOwnAgentReported(t *testing.T) {
	team := crewTeam{ProjectID: "team", OwnerID: "owner", People: []crewTeamPerson{{UserID: "other"}}}
	setup := crewChecklist{Title: "Tools", Revision: 3, Items: []crewChecklistItem{{Label: "Check"}}, Keys: []crewChecklistKey{{Name: "deepwiki", Kind: "mcp"}, {Name: "project-memory", Kind: "skill"}}}
	if err := validateCrewChecklist(&setup, nil, team); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []crewChecklistKey{{Name: "x", Kind: "plugin"}, {Kind: "mcp"}, {Name: "deepwiki", Kind: "skill"}, {Name: "x", Provider: "p", Kind: "mcp"}} {
		again := crewChecklist{Title: "Tools", Items: setup.Items, Keys: []crewChecklistKey{{Name: "deepwiki", Kind: "mcp"}, bad}}
		if validateCrewChecklist(&again, nil, team) == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
	setup.Progress = map[string]crewChecklistProgress{
		"owner": {SetupRevision: 3, InstructionRevision: 9, Installed: map[string]bool{"deepwiki": true, "project-memory": false}},
		"other": {SetupRevision: 2, InstructionRevision: 9, Installed: map[string]bool{"deepwiki": true}}, // an older revision does not count
	}
	var api *StreamingAPI // MCP and skill names need no Vault or Team lookup
	got := api.crewChecklistNeedStates(t.Context(), "team", setup, []string{"owner", "other"}, 9)
	want := map[string][]string{"owner": {"installed", "missing"}, "other": {"unreported", "unreported"}}
	for person, states := range want {
		for i, state := range states {
			if got[person][i].State != state {
				t.Fatalf("%s %s = %q, want %q", person, got[person][i].Name, got[person][i].State, state)
			}
		}
	}
}
