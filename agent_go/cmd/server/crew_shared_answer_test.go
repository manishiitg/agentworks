package server

import (
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
	got, err := crewSharedAnswerFiles(team, state, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "SHARED_REPORT") {
		t.Fatal("allowed shared report missing")
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
	rec := crewSharedAnswerRecord{ID: "msg-one", AgentID: agent.ID, Owner: "owner", Asker: "other", Question: "Shared report status?", Answer: "The report is ready", NeedsWork: true, At: time.Now()}
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
