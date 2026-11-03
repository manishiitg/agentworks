package agentworksclient

import "os"

// EnsurePrivateDirectory creates app-private state using Unix modes or the
// current Windows user's inheritable ACL, rejecting redirected directories.
// On a server with slots a Crew's private data is shared with its owner's slot
// group (setgid, group rwx, nothing for others); that folder is accepted as it
// is, never tightened, so the slot keeps its access (PLAT-856-509).
func EnsurePrivateDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil && info.IsDir() && slotGroupDir(info) {
		return nil
	}
	return privateLiveDir(path)
}
