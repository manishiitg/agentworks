//go:build windows

// Package fileowner answers "who owns this file" for the adapters' safety checks.
package fileowner

import (
	"os"

	"golang.org/x/sys/windows"
)

// IDs has no Windows meaning (no Unix account or group ids); callers fall back.
func IDs(os.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }

// OwnedByMe reports whether the file at path is owned by this process's Windows
// account (owner SID equals the token user's SID).
func OwnedByMe(path string, _ os.FileInfo) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return false
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	return owner.Equals(me.User.Sid)
}
