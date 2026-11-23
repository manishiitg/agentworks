package musecli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func museUsageRow(seq int, run string, in, out int) string {
	return `{"sequence":` + strconv.Itoa(seq) + `,"payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"owner":{"run_id":"` + run + `"},"quantity":{"reported":true,"input_tokens":` + strconv.Itoa(in) + `,"output_tokens":` + strconv.Itoa(out) + `,"cached_tokens":0}}}}}`
}

func museRegisterTestTerminal(t *testing.T, owner string, lines ...string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(log, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := musePersistentKey(owner)
	if err != nil {
		t.Fatal(err)
	}
	musePersistentPool.Lock()
	musePersistentPool.m[key] = &musePersistentSession{logPath: log, usageFloorSet: true}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, key)
		musePersistentPool.Unlock()
	})
	return log
}

// Runs of a Muse terminal after the Run's own are reported once; the Run's own rows stay with the Run.
func TestMuseRetainedUsageReportsEachRowOnceAndLeavesTheRunsOwn(t *testing.T) {
	const owner = "code:project:test-a"
	log := museRegisterTestTerminal(t, owner, museUsageRow(10, "run-first", 100, 10), museUsageRow(20, "run-steer-1", 200, 20), museUsageRow(30, "run-steer-2", 300, 30))
	if _, ok := readMuseTranscriptUsage(log, "run-first"); !ok { // the bounded Run reads its own run id first
		t.Fatal("the Run could not read its own run")
	}
	got := ReadRetainedTurnUsage(owner, "")
	if len(got) != 2 || !strings.HasPrefix(got[0].ID, "run-steer-1#") || !strings.HasPrefix(got[1].ID, "run-steer-2#") || got[0].Usage.InputTokens != 200 || got[1].Usage.OutputTokens != 30 {
		t.Fatalf("expected the two steer runs once, got %+v", got)
	}
	if again := ReadRetainedTurnUsage(owner, ""); len(again) != 0 {
		t.Fatalf("a second read repeated runs: %+v", again)
	}
}

// A live input settles as a retained turn while the Run is still running: the retained read claims the rows written so
// far, and the Run later takes only the rows that came after, so nothing is counted twice and nothing is lost.
func TestMuseRetainedReadBeforeTheRunEndsDoesNotDoubleCount(t *testing.T) {
	const owner = "code:project:test-b"
	log := museRegisterTestTerminal(t, owner, museUsageRow(10, "run-main", 100, 10), museUsageRow(20, "run-main", 150, 15))
	first := ReadRetainedTurnUsage(owner, "")
	if len(first) != 1 || first[0].Usage.InputTokens != 250 {
		t.Fatalf("the retained read should claim the two rows written so far, got %+v", first)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(museUsageRow(30, "run-main", 400, 40) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	usage, ok := readMuseTranscriptUsage(log, "run-main")
	if !ok || usage.InputTokens != 400 || usage.OutputTokens != 40 {
		t.Fatalf("the Run must take only the row after the retained read, got %+v ok=%t", usage, ok)
	}
}
