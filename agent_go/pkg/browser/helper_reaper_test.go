package browser

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// PLAT-662: the orphan rule (a daemon whose PID no .pid file records) and the
// stale-file rule, on the layout seen live on 2026-10-07.
func TestPlanHelperReapOrphansEndedChatsAndStaleFiles(t *testing.T) {
	root := t.TempDir()
	chatDir := filepath.Join(root, "o", "s1d71178cd313dab3")
	if err := os.MkdirAll(chatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, age time.Duration) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Now().Add(-age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	const shared = "shared-cdp-9222"
	const endedChat = "session-1d71178cd313dab3--browser"
	const liveChat = "session-08a29141854ed63e--browser"
	write(filepath.Join(root, shared+".pid"), "202", 2*24*time.Hour)
	write(filepath.Join(root, shared+".config"), "{}", 2*24*time.Hour)
	write(filepath.Join(chatDir, endedChat+".pid"), "54519\n", time.Hour)
	write(filepath.Join(root, liveChat+".pid"), "73731", time.Hour)
	write(filepath.Join(root, "ext-old.config"), "{}", 2*24*time.Hour)
	write(filepath.Join(root, "ext-old.target"), "x", 2*24*time.Hour)
	write(filepath.Join(root, "ext-new.config"), "{}", time.Hour)
	// An old session whose .pid still names a live process keeps its files.
	write(filepath.Join(root, "other.pid"), "4242", 3*24*time.Hour)
	write(filepath.Join(root, "other.sock"), "", 3*24*time.Hour)

	daemons := []helperDaemon{
		{PID: 202, Age: time.Hour, Session: shared, SocketDir: root, CDP: "http://localhost:9222"},
		{PID: 21123, Age: 4 * time.Hour, Session: shared, SocketDir: root, CDP: "http://localhost:9222"},
		{PID: 81157, Age: 3 * time.Hour, Session: shared, SocketDir: root, CDP: "http://localhost:9222"},
		{PID: 999, Age: 30 * time.Second, Session: shared, SocketDir: root}, // still starting
		{PID: 54519, Age: 2 * time.Hour, Session: endedChat, SocketDir: chatDir, CDP: "ws://127.0.0.1:1/cdp/x"},
		{PID: 73731, Age: 2 * time.Hour, Session: liveChat, SocketDir: root, CDP: "ws://127.0.0.1:1/cdp/y"},
		{PID: 555, Age: time.Hour, Session: "elsewhere", SocketDir: filepath.Join(root, "missing")},
	}
	live := func(s string) bool { return s == liveChat }
	plan := planHelperReap(daemons, []string{root}, live, "", time.Now(), func(pid int) bool { return pid == 4242 })

	var orphans []int
	for _, d := range plan.Orphans {
		orphans = append(orphans, d.PID)
	}
	sort.Ints(orphans)
	if len(orphans) != 2 || orphans[0] != 21123 || orphans[1] != 81157 {
		t.Fatalf("orphans = %v, want [21123 81157]", orphans)
	}
	if len(plan.Ended) != 1 || plan.Ended[0].Session != endedChat {
		t.Fatalf("ended = %+v, want only %s (never the shared session or a live chat)", plan.Ended, endedChat)
	}
	sort.Strings(plan.StaleFiles)
	want := []string{filepath.Join(root, "ext-old.config"), filepath.Join(root, "ext-old.target")}
	if len(plan.StaleFiles) != 2 || plan.StaleFiles[0] != want[0] || plan.StaleFiles[1] != want[1] {
		t.Fatalf("stale files = %v, want %v", plan.StaleFiles, want)
	}

	// Without chat liveness from the server, no chat helper is closed.
	if p := planHelperReap(daemons, []string{root}, nil, "", time.Now(), func(int) bool { return false }); len(p.Ended) != 0 {
		t.Fatalf("ended without liveness = %+v", p.Ended)
	}
}
