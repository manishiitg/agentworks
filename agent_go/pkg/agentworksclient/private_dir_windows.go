package agentworksclient

import (
	"errors"

	"golang.org/x/sys/windows"
)

// The first Windows build with AF_UNIX sockets (Windows 10 version 1803).
const minWindowsBuildForUnixSockets = 17063

// localIPCSupported refuses clearly on a Windows too old for the bridge's local sockets.
func localIPCSupported() error {
	if windows.RtlGetVersion().BuildNumber < minWindowsBuildForUnixSockets {
		return errors.New("the local delivery bridge needs Windows 10 version 1803 or newer")
	}
	return nil
}

// protectPrivateDir gives a new directory an owner-only access list that its files inherit. A socket
// file has no Unix mode on Windows, so this list on the directory is what keeps other users out of
// the bridge; it is set before the socket is bound.
func protectPrivateDir(path string) error {
	user, err := privateWindowsUser()
	if err != nil {
		return err
	}
	// Never re-protect a folder that belongs to someone else.
	owner, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if sid, _, e := owner.Owner(); e != nil || !privateWindowsSID(sid, user) {
		return errors.New("the folder is not owned by your account, SYSTEM or Administrators")
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
