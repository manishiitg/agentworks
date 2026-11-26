package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Your whole Mac is the existing exact home write grant, not an alias, shell
// HOME, ordinary home subfolder, or read-only attachment.
func TestStrictMacProcessAccessRequiresWholeHomeGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, c := range []struct {
		name          string
		reads, writes []string
		allow         bool
	}{
		{"none", nil, nil, false}, {"read_only", []string{home}, nil, false},
		{"child_folder", nil, []string{filepath.Join(home, "project")}, false},
		{"whole_mac", nil, []string{home}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := (&Isolator{BaseDir: t.TempDir(), StrictAllowlist: true, ReadPaths: c.reads, WritePaths: c.writes}).generateStrictSandboxProfile()
			if strings.Contains(p, "(allow process-info*)") != c.allow || strings.Contains(p, "(allow signal)\n") != c.allow || strings.Contains(p, "(allow signal (target self))") == c.allow {
				t.Fatal("whole-home process admission mismatch")
			}
		})
	}
}

// One real contract: generated strict profiles and ExecuteIsolated, not a
// hand-written policy. Logs counts only, never process arguments or names.
// Run on an unconfined macOS test host: a nested sandbox cannot widen its parent.
func TestMacWholeHomeProcessSandboxContract(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("requires macOS Seatbelt")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	helper := exec.Command("/bin/sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = helper.Process.Kill(); _ = helper.Wait() }()
	for _, whole := range []bool{false, true} {
		iso := &Isolator{BaseDir: work, WorkDir: work, StrictAllowlist: true, PrivateScratch: true, ReadPaths: []string{work}, WritePaths: []string{work}}
		if whole {
			iso.WritePaths = append(iso.WritePaths, home)
		}
		run := func(command string) ([]byte, error) {
			cmd, cleanup, err := iso.ExecuteIsolated(t.Context(), command, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			return cmd.CombinedOutput()
		}
		out, psErr := run("/bin/ps -ax -o pid=")
		listed := psErr == nil && strings.Contains(" "+strings.Join(strings.Fields(string(out)), " ")+" ", " "+strconv.Itoa(helper.Process.Pid)+" ")
		_, signalErr := run("/bin/kill -0 " + strconv.Itoa(helper.Process.Pid))
		t.Logf("[MAC_PROCESS_ACCESS] whole_mac=%t action=contract listed=%t signal_probe=%t", whole, listed, signalErr == nil)
		if listed != whole || (signalErr == nil) != whole {
			t.Fatalf("process contract mismatch whole_mac=%t ps_error=%v signal_error=%v", whole, psErr, signalErr)
		}
	}
}
