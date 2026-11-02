//go:build !windows

package agentworksclient

// socketUnreadable reports a socket file that exists but cannot be inspected; Unix always can.
func socketUnreadable(error) bool { return false }
