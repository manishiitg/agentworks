package claudecode

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func resetClaudeTranscriptResolverForTest(t *testing.T) {
	t.Helper()
	claudeTranscriptPathCache.Lock()
	claudeTranscriptPathCache.paths = make(map[string]string)
	claudeTranscriptPathCache.Unlock()
	originalGlob := claudeTranscriptGlob
	originalOpen := claudeTranscriptOpen
	t.Cleanup(func() {
		claudeTranscriptPathCache.Lock()
		claudeTranscriptPathCache.paths = make(map[string]string)
		claudeTranscriptPathCache.Unlock()
		claudeTranscriptGlob = originalGlob
		claudeTranscriptOpen = originalOpen
	})
}

func transcriptPathForWorkingDir(t *testing.T, home, workingDir, sessionID string) string {
	t.Helper()
	paths := claudeTranscriptWorkingDirCandidates(home, workingDir, sessionID)
	if len(paths) == 0 {
		t.Fatalf("no transcript candidate for working dir %q", workingDir)
	}
	if err := os.MkdirAll(filepath.Dir(paths[0]), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return paths[0]
}

func TestFreshestClaudeTranscriptForWorkDir(t *testing.T) {
	// Exercises the live no-replay test's transcript discovery against a
	// synthetic HOME, so a bug in that plumbing fails deterministically
	// rather than on the next live run.
	home := t.TempDir()
	t.Setenv("HOME", home)
	workDir := filepath.Join(home, "ws", "proj")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldPath := transcriptPathForWorkingDir(t, home, workDir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	newPath := transcriptPathForWorkingDir(t, home, workDir, "ffffffff-1111-2222-3333-444444444444")
	if err := os.WriteFile(oldPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write new: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	now := time.Now()
	if err := os.Chtimes(oldPath, past, past); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}
	if err := os.Chtimes(newPath, now, now); err != nil {
		t.Fatalf("Chtimes new: %v", err)
	}
	if got := freshestClaudeTranscriptForWorkDir(t, workDir); got != newPath {
		t.Fatalf("freshest = %s, want %s", got, newPath)
	}
}

func TestResolveClaudeTranscriptPathUsesWorkingDirectoryWithoutGlobalScan(t *testing.T) {
	resetClaudeTranscriptResolverForTest(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "11111111-2222-3333-4444-555566667777"
	workingDir := filepath.Join(t.TempDir(), "project.with_underlines")
	path := transcriptPathForWorkingDir(t, home, workingDir, sessionID)
	appendLine(t, path, "{}\n")

	var globCalls atomic.Int32
	claudeTranscriptGlob = func(pattern string) ([]string, error) {
		globCalls.Add(1)
		return filepath.Glob(pattern)
	}

	for i := 0; i < 2; i++ {
		got, err := resolveClaudeTranscriptPath(sessionID, workingDir, true)
		if err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
		if got != path {
			t.Fatalf("resolve %d = %q, want %q", i, got, path)
		}
	}
	// PLAT-856-442: moving this nativeID to another cwd must not keep the
	// cached source journal and miss the destination's committed end_turn.
	destination := filepath.Join(t.TempDir(), "destination")
	target := transcriptPathForWorkingDir(t, home, destination, sessionID)
	appendLine(t, target, "{}\n")
	got, e := resolveClaudeTranscriptPath(sessionID, destination, true)
	if e != nil || got != target {
		t.Fatalf("relocated nativeID used source: %s %v", got, e)
	}
	got, e = resolveClaudeTranscriptPath(sessionID, workingDir, true)
	if e != nil || got != path {
		t.Fatal("return lost source mapping")
	}
	if got := globCalls.Load(); got != 0 {
		t.Fatalf("global glob calls = %d, want 0 for known working directory", got)
	}
}

func TestOpenClaudeTranscriptInvalidatesMissingCachedPath(t *testing.T) {
	resetClaudeTranscriptResolverForTest(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	const sessionID = "44444444-5555-6666-7777-888899990000"
	legacyOne := filepath.Join(home, ".claude", "projects", "legacy-one", sessionID+".jsonl")
	legacyTwo := filepath.Join(home, ".claude", "projects", "legacy-two", sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(legacyOne), 0o755); err != nil {
		t.Fatalf("MkdirAll legacy one: %v", err)
	}
	appendLine(t, legacyOne, "{}\n")

	var globCalls atomic.Int32
	claudeTranscriptGlob = func(pattern string) ([]string, error) {
		globCalls.Add(1)
		return filepath.Glob(pattern)
	}
	path, err := resolveClaudeTranscriptPath(sessionID, "", true)
	if err != nil || path != legacyOne {
		t.Fatalf("initial resolve path=%q err=%v", path, err)
	}
	if err := os.Remove(legacyOne); err != nil {
		t.Fatalf("remove legacy one: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyTwo), 0o755); err != nil {
		t.Fatalf("MkdirAll legacy two: %v", err)
	}
	appendLine(t, legacyTwo, "{}\n")

	f, reopenedPath, err := openClaudeTranscript(sessionID, "")
	if err != nil {
		t.Fatalf("reopen after replacement: %v", err)
	}
	defer f.Close()
	if reopenedPath != legacyTwo {
		t.Fatalf("reopened path = %q, want %q", reopenedPath, legacyTwo)
	}
	if got := globCalls.Load(); got != 2 {
		t.Fatalf("global glob calls = %d, want initial discovery plus one re-resolution", got)
	}
}
