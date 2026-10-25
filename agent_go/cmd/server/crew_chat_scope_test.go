package server

import (
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// A Crew reads every conversation held with it, including other users',
// but never another Crew's chats, even though other Crews' files are shared.
func TestCrewChatScope(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	own := registerTestCrew(t, "sde", "alice", true)
	registerTestCrew(t, "qa", "bob", true)
	registerTestCrew(t, "ops", "alice", true)
	refs := []string{
		"Crew/qa",          // another owner's Crew
		"Crew/ops",         // caller's own other Crew
		"Crew/sde",         // this Crew itself
		"Workflow/reports", // workflows are not Crews
	}
	got := foreignCrewChatBlockedPaths(own, refs)
	want := []string{
		"Crew/qa/builder/",
		"Crew/ops/builder/",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("blocked = %v, want %v", got, want)
	}

	mirror, ok := crewReaderConversationMirrorPath("bob", own+"/", "work:project:abc", time.Now())
	if !ok || mirror != own+"/builder/crew-chats/users/bob/session-work_project_abc-conversation.json" && !strings.HasPrefix(mirror, own+"/builder/crew-chats/users/bob/session-") {
		t.Fatalf("mirror path = %q ok=%v", mirror, ok)
	}
	if !strings.HasPrefix(mirror, own+"/") {
		t.Fatal("reader chats must be mirrored inside the Crew they were held with")
	}
	for _, blocked := range got {
		if strings.HasPrefix(mirror, blocked) {
			t.Fatalf("this Crew's own mirrored chats must stay readable, blocked by %s", blocked)
		}
	}
	if _, ok := crewReaderConversationMirrorPath("bob", "Crew/unregistered", "s", time.Now()); ok {
		t.Fatal("a Crew with no owner cannot be mirrored into")
	}
}

func TestUpdateWorkSessionWorkflowGuardBlocksAttachedCrewChats(t *testing.T) {
	session := "session-crew-chat-scope"
	common.SetSessionFolderGuard(session, []string{"Crew/sde/"}, []string{"Crew/sde/"})
	common.SetSessionFolderGuardBlockedPaths(session, nil)
	defer common.ClearSessionShellConfig(session)

	updateWorkSessionWorkflowGuard(session, []string{"Crew/qa", "Workflow/reports"})
	cfg := common.GetSessionShellConfig(session)
	if len(cfg.BlockedPaths) != 1 || cfg.BlockedPaths[0] != "Crew/qa/builder/" {
		t.Fatalf("blocked after attach = %v", cfg.BlockedPaths)
	}
	updateWorkSessionWorkflowGuard(session, nil, "Crew/qa")
	if cfg := common.GetSessionShellConfig(session); len(cfg.BlockedPaths) != 0 {
		t.Fatalf("blocked after detach = %v", cfg.BlockedPaths)
	}
}

// A Crew reads another Crew's database but writes only its own.
func TestAttachedCrewDatabaseIsReadOnly(t *testing.T) {
	if got := foreignCrewDBWriteBlockedPaths("Crew/sde", []string{"Crew/sde", "Crew/qa/", "Workflow/reports"}); len(got) != 1 || got[0] != "Crew/qa/db/" {
		t.Fatalf("foreign db write blocks = %v", got)
	}
	session := "session-crew-db-scope"
	common.SetSessionFolderGuard(session, []string{"Crew/sde/"}, []string{"Crew/sde/"})
	common.SetSessionFolderGuardBlockedWritePaths(session, []string{"Crew/sde/db/db.sqlite"})
	defer common.ClearSessionShellConfig(session)

	updateWorkSessionWorkflowGuard(session, []string{"Crew/qa"})
	cfg := common.GetSessionShellConfig(session)
	if !stringSliceContains(cfg.BlockedWritePaths, "Crew/qa/db/") || !stringSliceContains(cfg.BlockedWritePaths, "Crew/sde/db/db.sqlite") {
		t.Fatalf("write blocks after attach = %v", cfg.BlockedWritePaths)
	}
	if stringSliceContains(cfg.BlockedPaths, "Crew/qa/db/") {
		t.Fatal("another Crew's database must stay readable")
	}
	updateWorkSessionWorkflowGuard(session, nil, "Crew/qa")
	if cfg := common.GetSessionShellConfig(session); stringSliceContains(cfg.BlockedWritePaths, "Crew/qa/db/") || !stringSliceContains(cfg.BlockedWritePaths, "Crew/sde/db/db.sqlite") {
		t.Fatalf("write blocks after detach = %v", cfg.BlockedWritePaths)
	}
}
