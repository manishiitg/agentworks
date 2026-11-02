package agentworksclient

// EnsurePrivateDirectory creates app-private state using Unix modes or the
// current Windows user's inheritable ACL, rejecting redirected directories.
func EnsurePrivateDirectory(path string) error { return privateLiveDir(path) }
