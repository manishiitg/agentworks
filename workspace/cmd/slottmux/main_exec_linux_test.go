//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// TestBuiltFrontendRefusesAMismatchBeforeTmux builds the real slottmux binary and executes it (PLAT-451): a launch
// whose script and folder name different slots exits non-zero, says why, and never starts a tmux session (the
// launch script would leave a marker file); the app-account launch is not refused (it goes on to the system tmux,
// which may be missing on the test host: then it fails to start it, which is not the refusal).
func TestBuiltFrontendRefusesAMismatchBeforeTmux(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("slottmux execution test needs the go toolchain on PATH: " + err.Error())
	}
	bin := filepath.Join(t.TempDir(), "tmux")
	build := exec.Command(goBin, "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building slottmux: %v\n%s", err, out)
	}
	cfg, docs := frontendEnv(t)
	_, hasTmux := os.Stat(slots.TmuxPath)

	for _, tc := range cases(cfg, docs) {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ran")
			args := newSessionArgs(cfg, "claude-exec", tc.dir, tc.script)
			args[len(args)-1] = "touch " + marker + "; " + args[len(args)-1]
			cmd := exec.Command(bin, args...)
			cmd.Env = append(os.Environ(), slots.EnvConfig+"="+os.Getenv(slots.EnvConfig))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			code := 0
			if err := cmd.Run(); err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				code = ee.ExitCode()
			}
			refused := strings.Contains(stderr.String(), "SLOT_EXPLICIT_MISMATCH")
			if tc.wantExit == exitMismatch {
				if code != exitMismatch || !refused {
					t.Fatalf("exit=%d stderr=%q: want a refusal", code, stderr.String())
				}
				if _, err := os.Stat(marker); err == nil {
					t.Fatal("the launch ran despite the refusal")
				}
				return
			}
			if refused || code == exitMismatch {
				t.Fatalf("launch was refused: exit=%d stderr=%q", code, stderr.String())
			}
			if tc.wantPass && hasTmux != nil && !strings.Contains(stderr.String(), "cannot start") {
				t.Fatalf("app-account launch did not reach the system tmux: exit=%d stderr=%q", code, stderr.String())
			}
		})
	}
}
