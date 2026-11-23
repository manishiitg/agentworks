//go:build windows

package musecli

import "os"

// ownedByCurrentUser: Windows temp folders are already per user.
func ownedByCurrentUser(os.FileInfo) bool { return true }
