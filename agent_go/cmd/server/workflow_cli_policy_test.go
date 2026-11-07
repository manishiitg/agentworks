package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-355: Workshop children had grants but no enforced launch policy,
// so native Codex became read-only. The child must get its guard, not its parent's.
func TestWorkflowChildPolicyUsesDedicatedGuard(t *testing.T) {
	docs, state := t.TempDir(), t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	store, err := clisecurity.NewStore(filepath.Join(state, "security"))
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{cliSecurityStore: store}
	oldOS, oldRunner, oldSeatbelt := cliHostOS, cliLandlockRunner, cliSeatbeltAvailable
	t.Cleanup(func() { cliHostOS, cliLandlockRunner, cliSeatbeltAvailable = oldOS, oldRunner, oldSeatbelt })
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	for _, workflow := range []string{"Workflow/example", "Crew/example"} {
		t.Run(workflow, func(t *testing.T) {
			session := t.Name()
			output := workflow + "/runs/run-one/execution/step-one"
			if workflow == "Crew/example" {
				if err := os.MkdirAll(filepath.Join(docs, "Crew/example"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(docs, "_system/crew-data/example"), 0700); err != nil {
					t.Fatal(err)
				}
				registry := filepath.Join(state, "ownership")
				if err := os.MkdirAll(registry, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(registry, "projects.json"), []byte(`{"projects":{"example":{"product":"work","folder":"example","owner_id":"owner","project_id":"example"}}}`), 0600); err != nil {
					t.Fatal(err)
				}
				if _, found, err := crewStorageRoots(workflow); err != nil || !found {
					t.Fatalf("missing Crew binding: found=%v error=%v", found, err)
				}
			}
			common.SetSessionFolderGuard(session, []string{workflow}, []string{output})
			common.SetSessionWorkflowPath(session, workflow)
			common.SetSessionFolderGuardBlockedPaths(session, []string{workflow + "/db/db.sqlite"})
			common.SetSessionFolderGuardBlockedWritePaths(session, []string{workflow + "/planning"})
			t.Cleanup(func() { common.ClearSessionShellConfig(session) })
			for _, tc := range []struct {
				host string
				lock bool
				want string
			}{{"darwin", true, "seatbelt"}, {"linux", true, "landlock"}, {"linux", false, "bridge"}, {"windows", false, "bridge"}} {
				t.Run(tc.host+"-"+tc.want, func(t *testing.T) {
					cliHostOS = tc.host
					cliSeatbeltAvailable = func() bool { return tc.lock }
					cliLandlockRunner = func() (string, bool) { return "/launcher", tc.lock }
					p, err := api.resolveWorkflowCLIPolicy(ctx, session, "codex-cli", codingAgentWorkspaceWorkingDir(workflow))
					if err != nil {
						t.Fatal(err)
					}
					if tc.want == "bridge" {
						if p != nil {
							t.Fatal("unconfined child received native policy")
						}
						return
					}
					if p.Mode != llmtypes.CLISecurityModeIsolated || p.PrivateHome == "" || (tc.want == "seatbelt") != p.Seatbelt || (tc.want == "landlock" && p.LandlockRunner != "/launcher") {
						t.Fatalf("missing confinement: %#v", p)
					}
					if !slices.Contains(p.WorkspaceWritePaths, codingAgentWorkspaceWorkingDir(output)) || slices.Contains(p.WorkspaceWritePaths, docs) || slices.Contains(p.WorkspaceWritePaths, filepath.Join(docs, workflow)) {
						t.Fatalf("child write grant widened: %v", p.WorkspaceWritePaths)
					}
					if !slices.Contains(p.BlockedPaths, codingAgentWorkspaceWorkingDir(workflow+"/db/db.sqlite")) || !slices.Contains(p.BlockedWritePaths, codingAgentWorkspaceWorkingDir(workflow+"/planning")) {
						t.Fatalf("blocked paths dropped: %#v", p)
					}
					if !p.RunAs.Declared || p.RunAs.User != "" || p.RunAs.Root != codingAgentWorkspaceWorkingDir(workflow) {
						t.Fatalf("child borrowed parent slot: %#v", p.RunAs)
					}
				})
			}
		})
	}
}
