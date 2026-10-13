package agentworksclient

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateWindowsUser() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return u.User.Sid, nil
}

func privateWindowsSID(sid, user *windows.SID) bool {
	return sid != nil && (sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

// Windows reports synthetic Unix modes. Verify its actual owner and ACL instead.
// Custom config paths remain supported when their access is equally restricted.
func CheckPrivateAccess(path string, _ os.FileInfo) error {
	user, err := privateWindowsUser()
	if err != nil {
		return fmt.Errorf("cannot verify current Windows user: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot verify Windows file access: %w", err)
	}
	if sd == nil {
		return errors.New("Windows file has no security descriptor")
	}
	defer runtime.KeepAlive(sd)
	owner, _, err := sd.Owner()
	if err != nil || !privateWindowsSID(owner, user) {
		return errors.New("Windows file is not owned by the current user, SYSTEM or Administrators")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errors.New("Windows file has unrestricted access")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, i, &ace); err != nil {
			return fmt.Errorf("cannot verify Windows access entry: %w", err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 || ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || uintptr(ace.Header.AceSize) < unsafe.Sizeof(*ace) {
			return errors.New("Windows file has an unsupported access entry")
		}
		if ace.Mask != 0 && !privateWindowsSID((*windows.SID)(unsafe.Pointer(&ace.SidStart)), user) {
			return errors.New("Windows file is accessible to other operating-system users")
		}
	}
	return nil
}

// Protect the empty temporary file before any credentials or message text enter
// it. Failed ACL updates stop the write; there is no permissive fallback.
func SecurePrivateFile(f *os.File) error {
	user, err := privateWindowsUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(f.Name(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func privateConfigAccessHint() string { return "" }
