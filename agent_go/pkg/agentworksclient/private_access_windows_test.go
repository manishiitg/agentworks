package agentworksclient

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateConfigLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Config{Server: "https://example.com", Token: "test-credential"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got != cfg {
		t.Fatalf("saved login could not be reloaded: %v", err)
	}
	// Prove why the old Unix check failed on a correctly protected Windows file.
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 == 0 {
		t.Fatal("Windows regression requires synthetic Unix modes")
	}
	if err = CheckPrivateAccess(path, info); err != nil {
		t.Fatal(err)
	}
	cfg.Token = "replacement-test-credential"
	if err = SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	if got, err = LoadConfig(path); err != nil || got != cfg {
		t.Fatalf("replacement could not be reloaded: %v", err)
	}
	user, err := privateWindowsUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.String() + ")(A;;GR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("credentials granting Everyone access were accepted")
	}
}
