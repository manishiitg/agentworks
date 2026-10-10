//go:build linux

package slots

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testConfig(t *testing.T) (ExecConfig, string) {
	t.Helper()
	root := t.TempDir()
	return ExecConfig{AllowedExec: []string{"/bin/sh", "/bin/echo"}, AllowedCwd: []string{root}}, root
}

func TestValidateAllowsOnlyListedProgramsAndFolders(t *testing.T) {
	cfg, root := testConfig(t)
	inside := filepath.Join(root, "work")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Validate(ExecRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: inside, Env: []string{"A=1"}}); err != nil {
		t.Fatalf("a listed program in an allowed folder was refused: %v", err)
	}
	outside := t.TempDir()
	cases := map[string]ExecRequest{
		"program not listed":    {Argv: []string{"/bin/cat", "x"}, Cwd: inside},
		"relative program":      {Argv: []string{"sh", "-c", "id"}, Cwd: inside},
		"unclean program":       {Argv: []string{"/bin/../bin/sh"}, Cwd: inside},
		"no program":            {Argv: nil, Cwd: inside},
		"folder outside":        {Argv: []string{"/bin/sh"}, Cwd: outside},
		"relative folder":       {Argv: []string{"/bin/sh"}, Cwd: "work"},
		"traversal out":         {Argv: []string{"/bin/sh"}, Cwd: inside + "/../../.."},
		"malformed environment": {Argv: []string{"/bin/sh"}, Cwd: inside, Env: []string{"NOEQUALS"}},
	}
	for name, req := range cases {
		if _, err := cfg.Validate(req); err == nil {
			t.Fatalf("%s: must be refused", name)
		}
	}
}

func TestValidateAcceptsAGlobForReleaseFolders(t *testing.T) {
	root := t.TempDir()
	cfg := ExecConfig{AllowedExec: []string{"/srv/agents/releases/*/bin/video-studio-landlock-runner"}, AllowedCwd: []string{root}}
	ok := ExecRequest{Argv: []string{"/srv/agents/releases/agents-1/bin/video-studio-landlock-runner", "--config", "x"}, Cwd: root}
	if _, err := cfg.Validate(ok); err != nil {
		t.Fatalf("a release path matching the pattern was refused: %v", err)
	}
	for _, bad := range []string{
		"/srv/agents/releases/a/b/bin/video-studio-landlock-runner",
		"/srv/agents/releases/agents-1/bin/other",
		"/tmp/releases/x/bin/video-studio-landlock-runner",
	} {
		if _, err := cfg.Validate(ExecRequest{Argv: []string{bad}, Cwd: root}); err == nil {
			t.Fatalf("%s must be refused", bad)
		}
	}
}

func TestValidateFollowsSymlinksBeforeChecking(t *testing.T) {
	cfg, root := testConfig(t)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.Validate(ExecRequest{Argv: []string{"/bin/sh"}, Cwd: link}); err == nil {
		t.Fatal("a symlink out of the allowed folders must be refused")
	}
}

func runWith(t *testing.T, cfg ExecConfig, body string) (code int, out, errOut string) {
	t.Helper()
	dir := t.TempDir()
	stdout, _ := os.Create(filepath.Join(dir, "out"))
	stderr, _ := os.Create(filepath.Join(dir, "err"))
	defer stdout.Close()
	defer stderr.Close()
	code = RunExec(strings.NewReader(body), stdout, stderr, cfg)
	o, _ := os.ReadFile(stdout.Name())
	e, _ := os.ReadFile(stderr.Name())
	return code, string(o), string(e)
}

func TestRunExecRunsWithTheRequestsEnvironmentFolderAndExitCode(t *testing.T) {
	cfg, root := testConfig(t)
	t.Setenv("PLATFORM_ONLY_SECRET", "must-not-leak")
	body := `{"argv":["/bin/sh","-c","echo $WHO in $(pwd); echo secret=[$PLATFORM_ONLY_SECRET]; exit 7"],"cwd":"` + root + `","env":["WHO=slot","PATH=/usr/bin:/bin"]}`
	code, out, errOut := runWith(t, cfg, body)
	if code != 7 {
		t.Fatalf("exit code %d, want 7 (stderr %q)", code, errOut)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(out, "slot in "+resolved) || !strings.Contains(out, "secret=[]") {
		t.Fatalf("environment or folder wrong: %q", out)
	}
}

func TestRunExecRefusesWhatTheAllowListDoesNot(t *testing.T) {
	cfg, root := testConfig(t)
	code, _, errOut := runWith(t, cfg, `{"argv":["/bin/cat","/etc/passwd"],"cwd":"`+root+`","env":[]}`)
	if code != 126 || !strings.Contains(errOut, "not an allowed program") {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	if code, _, _ := runWith(t, cfg, `not json`); code != 125 {
		t.Fatalf("a malformed request must be refused, got %d", code)
	}
}

func TestRunExecHandsThePolicyOnFd3(t *testing.T) {
	cfg, root := testConfig(t)
	body := `{"argv":["/bin/sh","-c","cat <&3"],"cwd":"` + root + `","env":["PATH=/usr/bin:/bin"],"fd3":"{\"policy\":true}"}`
	code, out, errOut := runWith(t, cfg, body)
	if code != 0 || out != `{"policy":true}` {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}

func TestRunExecMakesNewFilesPrivateToTheSlotAndItsGroup(t *testing.T) {
	cfg, root := testConfig(t)
	body := `{"argv":["/bin/sh","-c","echo x > made.txt"],"cwd":"` + root + `","env":["PATH=/usr/bin:/bin"]}`
	if code, _, errOut := runWith(t, cfg, body); code != 0 {
		t.Fatalf("code %d err %q", code, errOut)
	}
	info, err := os.Stat(filepath.Join(root, "made.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o660 {
		t.Fatalf("new file mode %o, want 660 (no access for other accounts)", mode)
	}
}

func TestValidateTmuxIsOnlyForTheSlotsOwnSocket(t *testing.T) {
	// The current account is not a slot, so tmux must be refused outright.
	root := t.TempDir()
	cfg := ExecConfig{AllowedExec: []string{TmuxPath}, AllowedCwd: []string{root}, SlotRunRoot: root}
	req := ExecRequest{Argv: []string{TmuxPath, "-S", SlotSocket(root, "slot01"), "new-session", "-d"}, Cwd: root}
	if _, err := cfg.Validate(req); err == nil {
		t.Fatal("tmux must be refused for an account that is not a slot")
	}
}

func TestRunExecFileIsOnlyForASlotsOwnRunFolder(t *testing.T) {
	cfg, root := testConfig(t)
	cfg.SlotRunRoot = root
	devnull, _ := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	defer devnull.Close()
	// This test account is not a slot, so a request file must be refused whatever it names.
	if code := RunExecFile(filepath.Join(root, "x.json"), devnull, devnull, devnull, cfg); code != 126 {
		t.Fatalf("a non-slot account must be refused, got %d", code)
	}
}

func TestValidateChmodIsOnlyForAnAccountThatIsASlot(t *testing.T) {
	root := t.TempDir()
	cfg := ExecConfig{AllowedExec: []string{ChmodPath}, AllowedCwd: []string{root}, SlotRunRoot: root}
	req := ExecRequest{Argv: []string{ChmodPath, "660", SlotSocket(root, "slot01")}, Cwd: root}
	if _, err := cfg.Validate(req); err == nil {
		t.Fatal("chmod must be refused for an account that is not a slot")
	}
}

// A stop signal must reach everything the program started, not only the program: the platform cannot signal a
// slot's processes itself, so this is the only way a timeout or a cancel stops a slotted command.
func TestRunExecStopSignalReachesTheWholeProcessGroup(t *testing.T) {
	cfg, root := testConfig(t)
	marker := filepath.Join(root, "child.pid")
	body := `{"argv":["/bin/sh","-c","sleep 300 & echo $! > ` + marker + `; wait"],"cwd":"` + root + `","env":["PATH=/usr/bin:/bin"]}`
	done := make(chan int, 1)
	go func() {
		code, _, _ := runWith(t, cfg, body)
		done <- code
	}()
	var childPID string
	for i := 0; i < 50 && childPID == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		if data, err := os.ReadFile(marker); err == nil {
			childPID = strings.TrimSpace(string(data))
		}
	}
	if childPID == "" {
		t.Fatal("the program did not start its child")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil { // handled by RunExec's signal forwarding
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(StopGrace + 4*time.Second):
		t.Fatal("the program did not stop after the signal")
	}
	pid, _ := strconv.Atoi(childPID)
	deadline := time.Now().Add(StopGrace + 2*time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the program's child %d is still running after the stop", pid)
}

// PLAT-805: a program that leaves on SIGTERM can leave behind a child that ignores it (a script with a SIGTERM trap,
// and the Chrome it started). The grace-period SIGKILL used to be a timer inside this process, which exits as soon as
// the program does, so such a child ran on for hours (RTS, 2026-10-10). After a stop the whole group must be gone
// by the time RunExec returns.
func TestRunExecStopKillsAChildThatIgnoresTheSignal(t *testing.T) {
	cfg, root := testConfig(t)
	marker := filepath.Join(root, "stubborn.pid")
	// The inner shell ignores SIGTERM and execs sleep, which keeps ignoring it; the outer shell dies on SIGTERM.
	body := `{"argv":["/bin/sh","-c","/bin/sh -c 'trap \"\" TERM; echo $$ > ` + marker + `; exec sleep 3609' & wait"],"cwd":"` + root + `","env":["PATH=/usr/bin:/bin"]}`
	done := make(chan int, 1)
	go func() {
		code, _, _ := runWith(t, cfg, body)
		done <- code
	}()
	var childPID string
	for i := 0; i < 50 && childPID == ""; i++ {
		time.Sleep(100 * time.Millisecond)
		if data, err := os.ReadFile(marker); err == nil {
			childPID = strings.TrimSpace(string(data))
		}
	}
	if childPID == "" {
		t.Fatal("the program did not start its child")
	}
	pid, _ := strconv.Atoi(childPID)
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil { // handled by RunExec's signal forwarding
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(StopGrace + 6*time.Second):
		t.Fatal("the program did not stop after the signal")
	}
	// RunExec has returned: nothing of the group may be left, with no further grace.
	if syscall.Kill(pid, 0) == nil {
		t.Fatalf("the child %d that ignores SIGTERM is still running after RunExec returned", pid)
	}
}
