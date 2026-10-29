package agentworksclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The bridge must serve and answer on Windows over its private local socket: the directory is created
// with an owner-only access list before the socket is bound, a second listener is refused, and a
// call from the same user gets through.
func TestWindowsLocalBridgeServesAndRefusesASecondListener(t *testing.T) {
	// A local socket path is limited to about 104 characters, and t.TempDir() includes the long test name.
	dir, err := os.MkdirTemp("", "aw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "bridge")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeLocalBridge(ctx, root, LocalBridgeOptions{Version: "windows-test"}) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the bridge did not stop")
		}
	}()
	socket := filepath.Join(root, "bridge.sock")
	for i := 0; i < 100; i++ {
		select {
		case err := <-done:
			t.Fatalf("the bridge stopped at start: %v", err)
		default:
		}
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	info, err := os.Lstat(root)
	if err != nil || CheckPrivateAccess(root, info) != nil {
		t.Fatalf("the bridge directory is not owner-only: %v", err)
	}
	callCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	reply, err := CallLocalBridge(callCtx, root, LocalBridgeRequest{Action: "ping"})
	if err != nil || reply.Version != "windows-test" {
		t.Fatalf("ping = %+v, %v", reply, err)
	}
	if err = ServeLocalBridge(ctx, root); err == nil || !strings.Contains(err.Error(), "already has a listener") {
		t.Fatalf("a second listener = %v", err)
	}

	// PLAT-856-222: an actual Windows READ_CONTROL denial was reduced to
	// "unsafe directory", hiding the failing step needed to diagnose hooks.
	user, err := privateWindowsUser()
	if err != nil {
		t.Fatal(err)
	}
	original, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	originalACL, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	// OWNER RIGHTS removes the owner's implicit READ_CONTROL grant; keep
	// WRITE_DAC allowed so the original private ACL can always be restored.
	denied, err := windows.SecurityDescriptorFromString("D:P(D;;RC;;;OW)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	deniedACL, _, err := denied.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, deniedACL, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, originalACL, nil); err != nil {
			t.Errorf("restore directory access: %v", err)
		}
	}()
	_, err = CallLocalBridge(callCtx, root, LocalBridgeRequest{Action: "ping"})
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || !strings.Contains(err.Error(), "unsafe local bridge directory") || !strings.Contains(err.Error(), "read owner and DACL (win32=5)") {
		t.Fatalf("directory access denial lost its cause: %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), user.String()) {
		t.Fatalf("directory access denial disclosed a path or SID: %v", err)
	}
}
