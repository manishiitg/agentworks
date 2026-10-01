//go:build linux

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
