package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// PLAT-756, PLAT-812: a Run-mode Crew turn (a Slack channel, a guest function call) may write the Crew's output folder
// and nothing else of the Crew, so an owner's function that saves evidence works for every caller while the Crew stays
// unchanged. The owner's turns name the same folder.
func TestCrewRunModeWritesOnlyTheOutputFolder(t *testing.T) {
	const sid = "product-6cc15a4e"
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })
	folder := crewOutputFolder("Crew/c-1/")
	if folder != "Crew/c-1/outputs/" || crewOutputFolder("Crew/c-1") != folder || crewOutputFolder("") != "" {
		t.Fatalf("output folder = %q", folder)
	}
	if env := crewOutputFolderEnv(folder); env[crewOutputDirEnv] == "" || env[crewOutputDirEnv] != env[crewRunDirEnv] {
		t.Fatalf("output folder env = %v", env)
	}

	common.SetSessionCrewReader(sid, true)
	common.SetSessionFolderGuard(sid, []string{"Crew/c-1/"}, []string{folder})
	common.SetSessionRunOutputPath(sid, folder)
	policy := cliLandlockPolicyForSession(sid, "claude-code", "/runtime/chat", nil)
	want := map[string]bool{cliPolicyPath(folder): true, "/runtime/chat": true}
	if len(policy.WorkspaceWritePaths) != len(want) {
		t.Fatalf("Run-mode writes = %v, want only the run folder and the runtime", policy.WorkspaceWritePaths)
	}
	for _, path := range policy.WorkspaceWritePaths {
		if !want[path] {
			t.Fatalf("Run-mode turn may write %q (writes %v)", path, policy.WorkspaceWritePaths)
		}
	}

	if notice := crewSessionModeNotice("Crew/c-1", folder, true); !strings.Contains(notice, "Run mode") || !strings.Contains(notice, folder) || !strings.Contains(notice, crewOutputDirEnv) {
		t.Fatalf("notice does not name the run folder: %s", notice)
	}
}
