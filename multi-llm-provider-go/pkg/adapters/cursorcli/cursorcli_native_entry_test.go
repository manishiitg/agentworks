package cursorcli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// PLAT-856-442: the official Bash bootstrap must not apply the tool-shell
// credential restrictions to Cursor itself. Custom wrappers are not rewritten.
func TestCursorOfficialBootstrapRunsNodeBeforeTheToolShellLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "must-not-reach-version-probe")
	t.Setenv("NODE_OPTIONS", "must-not-reach-version-probe")
	root := filepath.Join(home, ".local", "share", "cursor-agent", "versions", "test-version")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	launcher := filepath.Join(root, "cursor-agent")
	node, entry := filepath.Join(root, "node"), filepath.Join(root, "index.js")
	shim := "#!/usr/bin/env bash\nNODE_BIN=\"$SCRIPT_DIR/node\"\nexec -a \"$0\" \"$NODE_BIN\" \"$SCRIPT_DIR/index.js\" \"$@\"\n"
	for path, value := range map[string]string{launcher: shim, node: "#!/bin/sh\n[ -z \"$ANTHROPIC_API_KEY$NODE_OPTIONS\" ] || exit 71\nexit 0\n", entry: "// fixture"} {
		if err := os.WriteFile(path, []byte(value), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "cursor-agent")
	if err := os.Symlink(launcher, link); err != nil {
		t.Fatal(err)
	}
	args := []string{link, "--resume", "11111111-1111-4111-8111-111111111111", "--workspace", t.TempDir()}
	got, err := cursorNativeEntry(args, nil)
	want := append([]string{"/usr/bin/env", "CURSOR_INVOKED_AS=cursor-agent", node, "--use-system-ca", entry}, args[1:]...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("native entry = %q, %v", got, err)
	}
	if got, err = cursorNativeEntry(args, []string{root}); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatal("agent-writable installation bypassed the shell layer")
	}
	if got, err = cursorNativeEntry(args, []string{entry}); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatal("agent-writable entry file bypassed the shell layer")
	}
	if err := os.Chmod(entry, 0666); err != nil {
		t.Fatal(err)
	}
	if got, err = cursorNativeEntry(args, nil); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatal("world-writable entry bypassed the shell layer")
	}
	if err := os.Chmod(entry, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "index.js")
	if err := os.WriteFile(outside, []byte("// outside installation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := cursorNativeEntry(args, nil); err == nil {
		t.Fatal("entry link outside managed installation was accepted")
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("// fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(t.TempDir(), "cursor-agent")
	if err := os.WriteFile(unmanaged, []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err = cursorNativeEntry([]string{unmanaged}, nil); err != nil || !reflect.DeepEqual(got, []string{unmanaged}) {
		t.Fatal("unmanaged lookalike bypassed the shell layer")
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err = cursorNativeEntry(args, nil)
	want = append([]string{"/usr/bin/env", "CURSOR_INVOKED_AS=cursor-agent", node, entry}, args[1:]...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("older Node entry = %q, %v", got, err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := cursorNativeEntry(args, nil); err == nil {
		t.Fatal("incomplete official installation was accepted")
	}
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec custom-cursor \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err = cursorNativeEntry(args, nil); err != nil || !reflect.DeepEqual(got, args) {
		t.Fatal("custom launcher was rewritten")
	}
}

// Primary and supplementary slot membership both make a group-writable
// install untrusted, even if the application is its owner (PLAT-856-442).
func TestCursorManagedInstallGroupExcludesOtherUsers(t *testing.T) {
	users := "service:x:990:984::/home/service:/bin/sh\nslot01:x:1001:1001::/home/slot01:/bin/sh"
	for _, tc := range []struct {
		group, users string
		want         bool
	}{
		{"service:x:984:", users, true},
		{"service:x:984:service", users, true},
		{"service:x:984:slot01", users, false},
		{"service:x:984:", users + "\nslot02:x:1002:984::/home/slot02:/bin/sh", false},
		{"service:x:984:", "", false},
	} {
		if got := cursorPrivateInstallGroup(tc.group, tc.users, "990", "984"); got != tc.want {
			t.Fatalf("group=%q users=%q: trusted=%v", tc.group, tc.users, got)
		}
	}
}
