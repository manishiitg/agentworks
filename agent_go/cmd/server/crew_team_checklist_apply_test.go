package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-1045: Apply can only reach the asker's own agent through the normal
// durable inbox. Acceptance cannot fill a checklist, duplicate a retry, lose a
// long instruction file, send to a teammate or copy any key value.
func TestTeamChecklistApplyStaysOwnAndKeyStatusIsLiveMetadata(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	vault := newOwnerVaultFake(t)
	history, historyErr := chathistory.NewFilesystemStore(t.TempDir())
	if historyErr != nil {
		t.Fatal(historyErr)
	}
	f.api.chatStore = history
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Folder: crewTeamFolder{ID: "files", Folders: []string{"team"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("Check the example service.\n", 1000) // exceeds a message's prose cap; must remain complete.
	chunks, err := crewTeamChunkStore("team")
	if err != nil {
		t.Fatal(err)
	}
	file, err := chunkReader(strings.NewReader(text), int64(len(text)), false, func(id string, b []byte) error { return putChunk(chunks, id, b) })
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := commitCrewTeamChanges(t.Context(), "team", "owner", "instructions", crewTransferDigest("instructions"), []crewTeamChange{{Path: "team/apply.md", After: &file}})
	if err != nil {
		t.Fatal(err)
	}
	member := &UserClaims{UserID: "other", Username: "other", RegisteredClaims: jwt.RegisteredClaims{ID: "member-login", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	owner := &UserClaims{UserID: "owner", Username: "owner", RegisteredClaims: jwt.RegisteredClaims{ID: "owner-login", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}
	ctx := context.WithValue(t.Context(), UserContextKey, member)
	setup, err := saveCrewChecklist(member, "team", crewChecklist{Title: "Example setup", Instruction: crewChecklistInstruction{Path: "team/apply.md", Revision: receipt.Revision}, Items: []crewChecklistItem{{Label: "Connect and check"}}, Keys: []crewChecklistKey{{Name: "EXAMPLE_TOKEN"}, {Name: "TEAM_TOKEN"}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	register := func(c *UserClaims, name, teamProject string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"action": "register", "agent_name": name, "device_id": "device", "conversation_id": "chat-" + name, "runtime_info": map[string]any{"team_project_id": teamProject, "team_tab": map[bool]string{true: macMainTab, false: "tab-" + name}[name == "own-worker"]}})
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/external/v1/agents/app", bytes.NewReader(body)).WithContext(context.WithValue(t.Context(), UserContextKey, c))
		f.api.handleAppAgentRegistration(w, r)
		if w.Code != 200 {
			t.Fatalf("register: %d %s", w.Code, w.Body.String())
		}
		var a map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &a)
		return a
	}
	own, foreign, elsewhere := register(member, "own-worker", "team"), register(owner, "owner-worker", "team"), register(member, "other-project-worker", "another-team")
	call := func(args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(args)
		w := httptest.NewRecorder()
		f.api.handleCrewTeamChecklists(w, adminRequest(http.MethodPost, "/api/crew-teams/team/checklists", string(raw), member, map[string]string{"project": "team"}))
		return w
	}
	targets := call(map[string]any{"action": "setup_targets"})
	if !strings.Contains(targets.Body.String(), `"main":true`) || strings.Count(targets.Body.String(), `"main":true`) != 1 { // join setup goes to the person's Main agent only
		t.Fatalf("Main not marked as the one join-setup target: %s", targets.Body.String())
	}
	if targets.Code != 200 || !strings.Contains(targets.Body.String(), own["agent_id"].(string)) || strings.Contains(targets.Body.String(), foreign["agent_id"].(string)) {
		t.Fatal("target list crossed ownership", targets.Body.String())
	}
	if strings.Contains(targets.Body.String(), elsewhere["agent_id"].(string)) { // owner, 16 Nov: only the current project's agents
		t.Fatal("target list offered an agent of another project", targets.Body.String())
	}
	// A Todo assigned to other people is not the member's: not counted, not applicable, not assignable to a stranger.
	instruction := crewChecklistInstruction{Path: "team/apply.md", Revision: receipt.Revision}
	items := func() []crewChecklistItem { return []crewChecklistItem{{Label: "Do it"}} } // saving fills in item IDs, so never share the slice
	if _, err = saveCrewChecklist(member, "team", crewChecklist{Title: "Bad", Instruction: instruction, Items: items(), Assignees: []string{"stranger"}}, 0); err == nil {
		t.Fatal("assigned a Todo to someone outside the Team")
	}
	forOwner, err := saveCrewChecklist(member, "team", crewChecklist{Title: "Owner only", Instruction: instruction, Items: items(), Assignees: []string{"owner"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(map[string]any{"action": "setup_list"}); !strings.Contains(w.Body.String(), `"for_you":false`) || !strings.Contains(w.Body.String(), `"to_do":1`) {
		t.Fatal("assigned Todo counted for the wrong person", w.Body.String())
	}
	if w := call(map[string]any{"action": "setup_apply", "setup_id": forOwner.ID, "setup_revision": forOwner.Revision, "instruction_revision": receipt.Revision, "target_agent_id": own["agent_id"], "submission_id": "not-mine"}); w.Code == 200 {
		t.Fatal("applied a Todo assigned to someone else", w.Body.String())
	}
	// The normal Vault service stores values; the setup reads only metadata and
	// notices additions/removal without any setup write or cached status.
	get := map[string]any{"action": "setup_get", "setup_id": setup.ID}
	before := call(get)
	if before.Code != 200 || !strings.Contains(before.Body.String(), `"state":"missing"`) {
		t.Fatal("missing requirement not visible", before.Body.String())
	}
	vault.seed(t, f.api, "other", "", "EXAMPLE_TOKEN", "fixture-private-value")
	vault.seed(t, f.api, "team-team", "team", "TEAM_TOKEN", "fixture-team-value")
	vault.mu.Lock()
	teamVault := ""
	for id, p := range vault.projects {
		if p[1] == "team-team" {
			teamVault = id
		}
	}
	vault.mu.Unlock()
	if err = writeTeamWay("team", teamWayRecord{Vault: teamVault}); err != nil {
		t.Fatal(err)
	}
	after := call(get)
	if after.Code != 200 || !strings.Contains(after.Body.String(), `"source":"you"`) || !strings.Contains(after.Body.String(), `"source":"team"`) || strings.Contains(after.Body.String(), "fixture-private-value") || strings.Contains(after.Body.String(), "fixture-team-value") {
		t.Fatal("live metadata missing or value leaked", after.Body.String())
	}
	args := map[string]any{"action": "setup_apply", "setup_id": setup.ID, "setup_revision": setup.Revision, "instruction_revision": receipt.Revision, "target_agent_id": foreign["agent_id"], "submission_id": "apply-once"}
	if w := call(args); w.Code != 403 {
		t.Fatal("Apply sent to another person's agent", w.Body.String())
	}
	args["target_agent_id"] = elsewhere["agent_id"]
	if w := call(args); w.Code != 403 {
		t.Fatal("Apply sent to an agent of another project", w.Body.String())
	}
	args["target_agent_id"] = own["agent_id"]
	args["instruction_revision"] = receipt.Revision + 1
	if w := call(args); w.Code != 403 {
		t.Fatal("stale instructions dispatched", w.Body.String())
	}
	args["instruction_revision"] = receipt.Revision
	narrow := crewWriter("other")
	narrow.AccessToken.ProjectKind = "work" // Existing project-only grants cannot send arbitrary agent mail.
	if _, gateErr := crewChecklistTeam(narrow, "team", true); gateErr != nil {
		t.Fatal("project writer must reach the Team before the messaging refusal", gateErr)
	}
	if _, err = f.api.crewChecklistAction(ctx, narrow, "team", "setup_apply", args, crewChecklistActor{}); err == nil {
		t.Fatal("Apply upgraded a project-only connection to agent messaging")
	}
	guarded := *member
	guarded.AccessToken = &accesstokens.Token{Scopes: []string{"crews:read", "crews:write", "messages:send"}, AllCrews: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{"other-folder"}}}
	if _, err = f.api.crewChecklistAction(context.WithValue(ctx, UserContextKey, &guarded), &guarded, "team", "setup_apply", args, crewChecklistActor{}); err == nil {
		t.Fatal("Apply bypassed the instruction file guard")
	}
	if _, err = f.api.crewChecklistChat(ctx, member, nil, "team", "team", "session", "", "", true, "setup_apply", args); err == nil {
		t.Fatal("restricted chat applied a setup")
	}
	if _, err = f.api.crewChecklistChat(ctx, member, nil, "team", "team", "session", "owner", "team:chat:one", false, "setup_apply", args); err == nil {
		t.Fatal("shared ask applied as the runtime owner")
	}
	for n := 0; n < 2; n++ {
		if w := call(args); w.Code != 200 {
			t.Fatalf("own Apply %d: %d %s", n, w.Code, w.Body.String())
		}
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, conversation := range store.Conversations {
		for _, m := range conversation.Messages {
			if m.Submission != "apply-once" {
				continue
			}
			found++
			if conversation.Endpoints[1-m.From].UserID != "other" || conversation.Endpoints[1-m.From].NamedAgentID != own["agent_id"] || len(m.Attachments) != 1 {
				t.Fatalf("wrong recipient or missing instructions: %+v", m.Attachments)
			}
			raw, ok, err := readFileFromWorkspace(ctx, agentMessageFilesDir+m.Attachments[0].ID+".json")
			if err != nil || !ok {
				t.Fatal("instructions not durable", err)
			}
			var stored agentMessageFile
			if err = json.Unmarshal([]byte(raw), &stored); err != nil {
				t.Fatal(err)
			}
			decoded, err := base64.StdEncoding.DecodeString(stored.Content)
			if err != nil || string(decoded) != text || strings.Contains(m.Text, "fixture-private-value") {
				t.Fatal("instructions truncated or key value leaked", err)
			}
		}
	}
	if found != 1 {
		t.Fatalf("one Apply retry created %d inputs", found)
	}
	data, _ := readCrewChecklists("team")
	if len(data.Setups[setup.ID].Progress) != 0 {
		t.Fatal("queue acceptance marked work done")
	}
	vault.mu.Lock()
	vault.names[vault.personal["other"]] = nil
	copies := vault.copies
	vault.mu.Unlock()
	if w := call(get); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"missing"`) {
		t.Fatal("removed key stayed added", w.Body.String())
	}
	if copies != 0 {
		t.Fatal("Apply copied a key")
	}
	data, _ = readCrewChecklists("team")
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), `"state"`) || strings.Contains(string(raw), "fixture-private-value") || strings.Contains(string(raw), "fixture-team-value") {
		t.Fatal("metadata store retained derived status or key values")
	}
}

// A person's native CLI agent has no app runtime info; it belongs to this Team's project through the Team's own
// registry row for the same person, and a teammate's registry row or another project never counts.
func TestChecklistAgentInProjectCountsRegisteredTerminalAgent(t *testing.T) {
	team := crewTeam{Agents: map[string]crewTeamRegisteredAgent{"row": {Kind: "terminal", OwnerID: "me", Terminal: &crewTeamTerminal{NamedAgentID: "cli"}}}}
	agent := func(id, user string) namedMessageAgent {
		return namedMessageAgent{ID: id, UserID: user, Attachment: &namedAgentAttachment{}}
	}
	if !crewChecklistAgentInProject(team, agent("cli", "me"), "team") {
		t.Fatal("own registered terminal agent was not offered")
	}
	if crewChecklistAgentInProject(team, agent("cli", "other"), "team") || crewChecklistAgentInProject(team, agent("unregistered", "me"), "team") {
		t.Fatal("an agent outside the Team's registry was offered")
	}
}
