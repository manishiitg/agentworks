package server

import (
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Admitting a turn must not strip a live Code CLI's write access (server A 2026-10-06: every write failed with "Permission
// denied" after a message to a running CLI), while a moved Crew's stale guard still becomes read-only (PLAT-442).
func TestAdmissionKeepsACurrentGuardAndPinsAStaleOne(t *testing.T) {
	const live, moved = "guard-live-code", "guard-moved-crew"
	t.Cleanup(func() { common.ClearSessionShellConfig(live); common.ClearSessionShellConfig(moved) })
	root := "_users/u/Chats/Code/projects/sde-1/"
	common.SetSessionFolderGuard(live, []string{root, "_users/u/chat_history/"}, []string{root, "_users/u/chat_history/"})
	pinReadOnlyUnlessGuardCovers(live, root)
	if cfg := common.GetSessionShellConfig(live); !slices.Contains(cfg.WritePaths, root) {
		t.Fatalf("a current guard lost its writes: %+v", cfg)
	}
	common.SetSessionFolderGuard(moved, []string{"_users/u/Chats/Work/projects/beta-1/"}, []string{"_users/u/Chats/Work/projects/beta-1/"})
	pinReadOnlyUnlessGuardCovers(moved, "Crew/beta-1/")
	if cfg := common.GetSessionShellConfig(moved); len(cfg.WritePaths) != 0 || !slices.Equal(cfg.ReadPaths, []string{"Crew/beta-1/"}) {
		t.Fatalf("a stale guard must become read-only on the verified folder: %+v", cfg)
	}
}

// handleQuery rewrites a Builder request's mode from workflow_phase to multi-agent before tools run; the Builder must
// still get authority for its Brain project tools, and nothing outside the Builder phase may (server A 2026-10-06).
func TestKnowledgeProjectBuilderAuthoritySurvivesTheModeRewrite(t *testing.T) {
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	builder := QueryRequest{AgentMode: "multi-agent", PhaseID: "workflow-builder", admittedWorkflowPhase: true}
	if !knowledgeProjectBuilderQuery(builder, claims, "s", "s", false) {
		t.Fatal("the root Builder lost its Brain project authority after the mode rewrite")
	}
	// An ordinary chat that names the Builder phase itself is not the Builder.
	for _, req := range []QueryRequest{{AgentMode: "multi-agent"}, {AgentMode: "multi-agent", PhaseID: "workflow-builder"}, {AgentMode: "multi-agent", PhaseID: "workflow-run", admittedWorkflowPhase: true}, {AgentMode: "multi-agent", PhaseID: "workflow-builder", BotPlatform: "slack", admittedWorkflowPhase: true}} {
		if knowledgeProjectBuilderQuery(req, claims, "s", "s", false) {
			t.Fatalf("only the Builder phase may configure Brain project access: %+v", req)
		}
	}
}
