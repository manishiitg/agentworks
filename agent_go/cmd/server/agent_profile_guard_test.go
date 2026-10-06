package server

import (
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Admitting a turn must not strip a live Code CLI's write access (RTS 2026-10-06: every write failed with "Permission
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
