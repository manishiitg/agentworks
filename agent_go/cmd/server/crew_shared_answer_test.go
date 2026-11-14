package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// Real disk reads must not turn a shared-folder selector into private memory,
// credentials, an unshared file, or a symlink to the maker's other data.
func TestCrewSharedAnswerReadsOnlyAdmittedFiles(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"team/report.md": "SHARED_REPORT", "private.md": "PRIVATE_REPORT", "MEMORY.md": "PRIVATE_MEMORY", "team/.env": "PRIVATE_CREDENTIAL"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "private.md"), filepath.Join(root, "team", "link.md")); err != nil {
		t.Fatal(err)
	}
	state := crewTeamFileState{Versions: map[string]crewTeamVersion{}}
	for _, name := range []string{"team/report.md", "private.md", "MEMORY.md", "team/.env", "team/link.md"} {
		state.Versions[name] = crewTeamVersion{File: &crewsync.File{Size: 10}}
	}
	team := crewTeam{Folder: crewTeamFolder{Folders: []string{"team", "MEMORY.md"}}}
	got, err := crewSharedAnswerFiles(team, state, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "SHARED_REPORT") {
		t.Fatal("allowed shared report missing")
	}
	denied, err := crewSharedAnswerFiles(team, state, root, func(string) bool { return false })
	if err != nil || denied != "" {
		t.Fatal("narrowed connection file guard bypassed")
	}
	for _, private := range []string{"PRIVATE_REPORT", "PRIVATE_MEMORY", "PRIVATE_CREDENTIAL"} {
		if strings.Contains(got, private) {
			t.Fatalf("private source exposed: %s", private)
		}
	}
}

// The digest is scoped to the current maker AND the exact registered agent.
func TestCrewSharedAnswerDigestOnlyExactOwnerChat(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	newExternalCrewCommandFixture(t)
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"one": true, "another": true}, "other": {"two": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := team.registeredAgent("owner", "one")
	if !ok {
		t.Fatal("allowed agent fixture missing")
	}
	rec := crewSharedAnswerRecord{Authority: crewSharedAnswerAuthority(team, agent), ID: "msg-one", AgentID: agent.ID, Owner: "owner", Asker: "other", Question: "Shared report status?", Answer: "The report is ready", NeedsWork: true, At: time.Now()}
	if err = saveCrewSharedAnswer("team", rec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][2]string{{"other", agent.ChatKey}, {"owner", "team:chat:another"}} {
		got, err := crewSharedAnswerDigest("team", tc[0], tc[1])
		if err != nil || got != "" {
			t.Fatalf("foreign digest %v: %q %v", tc, got, err)
		}
	}
	got, err := crewSharedAnswerDigest("team", "owner", agent.ChatKey)
	if err != nil || !strings.Contains(got, rec.Answer) || !strings.Contains(got, "Queued") {
		t.Fatalf("own digest: %q %v", got, err)
	}
	got, err = crewSharedAnswerDigest("team", "owner", agent.ChatKey)
	if err != nil || got != "" {
		t.Fatal("digest repeated")
	}
	// Narrowing Live chat while an answer is generated invalidates that
	// result, even though the agent remains In team and otherwise askable.
	hidden := team.Agents[agent.ID]
	hidden.Rule.ChatShare = crewChatShareAgent
	team.Agents[agent.ID] = hidden
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	rec.ID = "msg-chat-narrowed"
	if err = saveCrewSharedAnswer("team", rec); err == nil {
		t.Fatal("answer retained revoked chat authority")
	}
	team.Agents[agent.ID] = agent
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	// Access was real above; now revoking sharing must refuse a new answer.
	saved := team.Agents[agent.ID]
	saved.InTeam = false
	team.Agents[agent.ID] = saved
	if err = writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	rec.ID = "msg-revoked"
	if err = saveCrewSharedAnswer("team", rec); err == nil {
		t.Fatal("revoked agent answer admitted")
	}
}

// Actual answer/store/mailbox path, substituting only the remote model call:
// generation uses asker authority and cannot start the maker's real chat.
func TestCrewSharedAnswerUsesAskerAndQueuesWithoutMakerTurn(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	fixture, _ := newExternalCrewCommandFixture(t)
	ctx := internalBotRequestContext(t.Context(), "other")
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"one": true}, "other": {"two": true}})}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	team, err := readCrewTeam("team")
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := team.registeredAgent("owner", "one")
	if !ok {
		t.Fatal("registered maker missing")
	}
	c := agentConversation{ID: "inbox-shared-answer", Endpoints: [2]agentMessageEndpoint{{UserID: "other", Kind: triggerCallerUser, ID: "other", External: true}, {UserID: "owner", Kind: triggerCallerCrew, ID: "team", TeamAgentID: agent.ID, ChatKey: agent.ChatKey}}}
	m := agentMessage{ID: "msg-shared-question", From: 0, Text: "Please update the shared report", Delivery: "delivered"}
	c.Messages = []agentMessage{m}
	agentMessageMu.Lock()
	err = saveAgentMessageStore(ctx, agentMessageStore{Conversations: []agentConversation{c}})
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	previous := crewSharedAnswerTextForTest
	defer func() { crewSharedAnswerTextForTest = previous }()
	calls := 0
	crewSharedAnswerTextForTest = func(ctx context.Context, actor string, record *publishedProject, input, system string) (string, error) {
		calls++
		if actor != "other" || record.OwnerID != "other" || record.ConnectionID != "" {
			t.Fatal("answer inherited the maker's account")
		}
		dir, err := record.historyDir()
		if err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("answer did not start in an empty directory")
		}
		return `{"answer":"The report update needs a real turn.","needs_work":true}`, nil
	}
	if err = fixture.api.answerCrewSharedMessage(ctx, c, m, c.Endpoints[0], c.Endpoints[1]); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("model calls: %d", calls)
	}
	records, err := readCrewSharedAnswers("team")
	if err != nil || len(records) != 1 || !records[0].NeedsWork || records[0].Presented {
		t.Fatalf("queued records: %+v %v", records, err)
	}
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(ctx)
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	last := store.Conversations[0].Messages[len(store.Conversations[0].Messages)-1]
	if last.Kind != "shared_answer" || !strings.Contains(last.Text, "Queued") {
		t.Fatalf("answer receipt missing: %+v", last)
	}
	digest, err := crewSharedAnswerDigest("team", "owner", agent.ChatKey)
	if err != nil || !strings.Contains(digest, m.Text) {
		t.Fatalf("queued work not delivered to owner: %q %v", digest, err)
	}
}
