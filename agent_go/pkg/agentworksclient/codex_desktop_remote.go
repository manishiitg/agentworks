package agentworksclient

import "strings"

// IsCodexDesktopRemote selects the same native desktop protocol on its Unix
// socket or the Windows app's one local named pipe. The adapter authenticates
// the actual server before any request is sent.
func IsCodexDesktopRemote(remote string) bool {
	return strings.HasPrefix(remote, "desktop+unix://") || remote == "desktop+npipe://codex-ipc"
}
