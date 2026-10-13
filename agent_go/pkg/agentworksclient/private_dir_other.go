//go:build !windows

package agentworksclient

func localIPCSupported() error { return nil }

// protectPrivateDir: a Unix directory is made private by its 0700 mode.
func protectPrivateDir(string) error { return nil }
