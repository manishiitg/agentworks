package mcpagent

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestPruneAgentPromptLogSessionsKeepsBoundedLatestSessions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LOG_AGENT_PROMPTS_MAX_SESSIONS", "3")

	root := agentPromptLogRoot()
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	sessions := []string{"session-a", "session-b", "session-c", "session-d", "session-e"}
	for i, name := range sessions {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(name), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
		ts := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(dir, ts, ts); err != nil {
			t.Fatalf("Chtimes(%s): %v", dir, err)
		}
	}

	pruneAgentPromptLogSessions("session-b")

	got := readPromptSessionDirs(t, root)
	want := []string{"session-b", "session-d", "session-e"}
	if len(got) != len(want) {
		t.Fatalf("remaining dirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining dirs = %v, want %v", got, want)
		}
	}
}

func TestPruneAgentPromptLogSessionsCanBeDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LOG_AGENT_PROMPTS_MAX_SESSIONS", "0")

	root := agentPromptLogRoot()
	for _, name := range []string{"session-a", "session-b"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}

	pruneAgentPromptLogSessions("")

	got := readPromptSessionDirs(t, root)
	want := []string{"session-a", "session-b"}
	if len(got) != len(want) {
		t.Fatalf("remaining dirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining dirs = %v, want %v", got, want)
		}
	}
}

func readPromptSessionDirs(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}
