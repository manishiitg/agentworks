package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

func TestClosedSessionLeavesNoEmptySocketFolder(t *testing.T) {
	session := "agents--project-0123456789abcdef--browser"
	dir := browserconfig.SocketDirForSession(session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skip("cannot create the browser socket folder here")
	}
	browserconfig.RemoveEmptySessionSocketDirs(session)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("empty socket folder %s was left behind", dir)
	}
	// A folder that still holds a file (a live daemon's socket) must stay.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	keep := filepath.Join(dir, session+".sock")
	if err := os.WriteFile(keep, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	browserconfig.RemoveEmptySessionSocketDirs(session)
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("socket folder with a live socket was removed: %v", err)
	}
}

// fakeWorkspace answers /api/execute with whatever respond returns for the command, and records every command it saw.
type fakeWorkspace struct {
	mu       sync.Mutex
	commands []string
	respond  func(command string, nth int) ShellExecuteResponse
	server   *httptest.Server
}

func newFakeWorkspace(t *testing.T, respond func(command string, nth int) ShellExecuteResponse) *fakeWorkspace {
	t.Helper()
	fake := &fakeWorkspace{respond: respond}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ShellExecuteRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		fake.mu.Lock()
		fake.commands = append(fake.commands, req.Command)
		nth := 0
		for _, seen := range fake.commands {
			if sameVerb(seen, req.Command) {
				nth++
			}
		}
		fake.mu.Unlock()
		_ = json.NewEncoder(w).Encode(APIResponse{Success: true, Data: fake.respond(req.Command, nth)})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func sameVerb(a, b string) bool { return verbOf(a) == verbOf(b) }

// verbOf is the agent-browser subcommand: the word after `--session <name>` in the quoted command.
func verbOf(command string) string {
	fields := strings.Fields(command)
	for i, field := range fields {
		if strings.Trim(field, "'") == "--session" && i+2 < len(fields) {
			return strings.Trim(fields[i+2], "'")
		}
	}
	return command
}

func (f *fakeWorkspace) count(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, command := range f.commands {
		if verbOf(command) == verb {
			n++
		}
	}
	return n
}

func stuckTestExecutor(t *testing.T, fake *fakeWorkspace) *Executor {
	t.Setenv(EnvAgentBrowserCDPEnabled, "false")
	previous := stuckSessionRetryPause
	stuckSessionRetryPause = time.Millisecond
	t.Cleanup(func() { stuckSessionRetryPause = previous })
	return NewExecutor(NewClient(fake.server.URL))
}

func TestOpenOnAStuckSessionClosesAndRetriesOnce(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		if verbOf(command) == "open" && nth == 1 {
			return ShellExecuteResponse{ExitCode: 1, Stdout: `{"success":false,"error":"Operation timed out. The page may still be loading or the element may not exist."}`}
		}
		return ShellExecuteResponse{ExitCode: 0, Stdout: `{"success":true}`}
	})
	out, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "open", "args": []string{"https://example.com"}, "session": "stuck-open-session"})
	if err != nil || !strings.Contains(out, "success") {
		t.Fatalf("open should recover after one retry, got %q %v", out, err)
	}
	if got := fake.count("open"); got != 2 {
		t.Fatalf("open ran %d times, want 2 (original + one retry)", got)
	}
}

func TestStuckSessionStillFailingGivesAClearError(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		return ShellExecuteResponse{ExitCode: 1, Stdout: `{"success":false,"error":"Operation timed out."}`}
	})
	_, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "open", "args": []string{"https://example.com"}, "session": "stuck-twice-session"})
	if err == nil || !strings.Contains(err.Error(), "BROWSER_STUCK") || !strings.Contains(err.Error(), "retried once") {
		t.Fatalf("want a BROWSER_STUCK error naming the single retry, got %v", err)
	}
	if got := fake.count("open"); got != 2 {
		t.Fatalf("open ran %d times, want exactly 2", got)
	}
}

func TestStuckSideEffectingCommandIsNotRetried(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		return ShellExecuteResponse{ExitCode: 1, Stdout: `{"success":false,"error":"Operation timed out."}`}
	})
	_, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "click", "args": []string{"#buy"}, "session": "stuck-click-session"})
	if err == nil || !strings.Contains(err.Error(), "BROWSER_STUCK") || !strings.Contains(err.Error(), "not retried") {
		t.Fatalf("click must report, not retry: %v", err)
	}
	if got := fake.count("click"); got != 1 {
		t.Fatalf("click ran %d times, want 1", got)
	}
}

func TestFullPageScreenshotOfAVeryTallPageIsRefused(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		if verbOf(command) == "eval" {
			return ShellExecuteResponse{ExitCode: 0, Stdout: `{"success":true,"data":{"result":"600000"},"error":null}`}
		}
		return ShellExecuteResponse{ExitCode: 0, Stdout: `{"success":true}`}
	})
	_, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "screenshot", "args": []string{"--full"}, "session": "tall-session"})
	if err == nil || !strings.Contains(err.Error(), "SCREENSHOT_TOO_TALL") {
		t.Fatalf("want SCREENSHOT_TOO_TALL, got %v", err)
	}
	if got := fake.count("screenshot"); got != 0 {
		t.Fatalf("screenshot reached the browser %d times", got)
	}
}

func TestFullPageScreenshotOfAnOrdinaryPageProceeds(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		if verbOf(command) == "eval" {
			return ShellExecuteResponse{ExitCode: 0, Stdout: `{"success":true,"data":{"result":"3200"},"error":null}`}
		}
		return ShellExecuteResponse{ExitCode: 0, Stdout: `{"success":true}`}
	})
	if _, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "screenshot", "args": []string{"--full"}, "session": "ordinary-session"}); err != nil {
		t.Fatal(err)
	}
	if got := fake.count("screenshot"); got != 1 {
		t.Fatalf("screenshot ran %d times, want 1", got)
	}
}

func TestUnwritableScreenshotFolderDoesNotKillAHealthyBrowser(t *testing.T) {
	fake := newFakeWorkspace(t, func(command string, nth int) ShellExecuteResponse {
		return ShellExecuteResponse{ExitCode: 1, Stdout: `{"success":false,"error":"Failed to save screenshot to /srv/x/.agent-browser/tmp/screenshots/s.png: No such file or directory (os error 2)"}`}
	})
	_, err := stuckTestExecutor(t, fake).HandleAgentBrowser(context.Background(), map[string]interface{}{
		"command": "screenshot", "args": []string{}, "session": "unwritable-session"})
	if err == nil || !strings.Contains(err.Error(), "Failed to save screenshot") {
		t.Fatalf("want the save error back, got %v", err)
	}
	if got := fake.count("screenshot"); got != 1 {
		t.Fatalf("screenshot ran %d times: a save error must not tear the session down and retry", got)
	}
}

func TestIsStuckBrowserError(t *testing.T) {
	for _, msg := range []string{"Operation timed out. The page may still be loading", "command timed out after 30s", "Target crashed", "CDP command timed out: Runtime.evaluate"} {
		if !isStuckBrowserError(stuckMsg(msg)) {
			t.Errorf("%q should count as stuck", msg)
		}
	}
	for _, msg := range []string{"element not found", "net::ERR_NAME_NOT_RESOLVED"} {
		if isStuckBrowserError(stuckMsg(msg)) {
			t.Errorf("%q must not count as stuck", msg)
		}
	}
}

type stuckMsg string

func (e stuckMsg) Error() string { return string(e) }
