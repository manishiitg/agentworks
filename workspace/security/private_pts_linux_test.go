//go:build linux

package security

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Run against the actual launcher, not an in-process ruleset: namespace
// creation, private devpts, capability removal and exec are all exercised.
func TestPrivatePTYLauncherLiveIsolation(t *testing.T) {
	runner := os.Getenv("LANDLOCK_PTS_TEST_RUNNER")
	if runner == "" {
		t.Skip("set LANDLOCK_PTS_TEST_RUNNER to the built launcher on Linux")
	}
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(master)
	n, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	peer := fmt.Sprintf("/dev/pts/%d", n)
	dir := t.TempDir()
	run := func(code string, forgedChild bool) (string, error) {
		t.Helper()
		config := filepath.Join(dir, "policy.json")
		raw, _ := json.Marshal(LandlockPolicy{WorkDir: dir, WritePaths: []string{dir}, PrivatePTS: true})
		if err := os.WriteFile(config, raw, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), runner, "--config", config, "--", "/usr/bin/python3", "-c", code)
		if forgedChild {
			cmd.Env = append(os.Environ(), privatePTSChildEnv+"=1")
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	code := fmt.Sprintf(`import os,pty,ctypes
assert not os.path.exists(%q), "host terminal leaked"
m,s=pty.openpty()
os.write(s,b"PRIVATE_PTY_OK\n")
assert os.read(m,100).strip()==b"PRIVATE_PTY_OK"
class Header(ctypes.Structure): _fields_=[("version",ctypes.c_uint32),("pid",ctypes.c_int)]
class Data(ctypes.Structure): _fields_=[("effective",ctypes.c_uint32),("permitted",ctypes.c_uint32),("inheritable",ctypes.c_uint32)]
h=Header(0x20080522,0);d=(Data*2)()
assert ctypes.CDLL(None).capget(ctypes.byref(h),d)==0
assert all(x.effective==x.permitted==x.inheritable==0 for x in d), "mount capabilities leaked"
print("PRIVATE_PTY_OK; HOST_PTY_HIDDEN; CAPABILITIES_ZERO")`, peer)
	if out, err := run(code, false); err != nil || !strings.Contains(out, "CAPABILITIES_ZERO") {
		t.Fatalf("private PTY: %s %v", out, err)
	}
	if out, err := run("raise SystemExit(17)", false); err == nil {
		t.Fatalf("command exit code lost: %s", out)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
		t.Fatalf("exit: %s %v", out, err)
	}
	// A forged re-exec marker must not grant access to the host terminals.
	if out, err := run("print('UNCONFINED_COMMAND_RAN')", true); err == nil || strings.Contains(out, "UNCONFINED_COMMAND_RAN") {
		t.Fatalf("namespace failure did not fail closed: %s %v", out, err)
	}
}
