package clisandbox

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// SeatbeltGrantedDirs are the folders a Seatbelt-confined chat is granted,
// other than its working folder, as existing directories. A CLI with its own
// "working directories" rule (Claude's dontAsk mode, Cursor's workspace) treats
// a granted folder reached through a link as outside the workspace and refuses
// or asks before the sandbox decides, so each CLI is told about them (--add-dir).
// Under Landlock the CLI's home is private and gets them through its settings
// instead; nothing is returned there.
func SeatbeltGrantedDirs(policy *llmtypes.CLISecurityPolicy, workingDir string) []string {
	if !policy.SeatbeltEnforced() {
		return nil
	}
	var dirs []string
	seen := map[string]bool{filepath.Clean(workingDir): true}
	for _, list := range [][]string{policy.WorkspaceWritePaths, policy.HostWritePaths, policy.WorkspaceReadPaths, policy.HostReadPaths} {
		for _, dir := range list {
			dir = filepath.Clean(strings.TrimSpace(dir))
			if dir == "." || seen[dir] {
				continue
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}
