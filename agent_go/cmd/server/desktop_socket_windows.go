package server

import "os"

// Split local desktop launching is supported on Mac/Linux only.
func desktopSocketOwnedByUser(info os.FileInfo) bool { return false }
