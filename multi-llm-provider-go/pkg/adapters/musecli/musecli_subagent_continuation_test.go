package musecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func museChainFixture(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	museRowSpawn     = `{"sequence":84,"payload_type":"subagent.control.spawn_accepted","payload":{"kind":"subagent_control","record":{"kind":"spawn_accepted","parent_run_id":"R1","subagent_id":"S1"}}}`
	museRowQueued    = `{"sequence":124,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R1","event":{"kind":"inbox_item_queued","source":{"source":"subagent_result","subagent_id":"S1"}},"source_run_record_id":"I1"}}`
	museRowTermR1    = `{"sequence":150,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R1","event":{"kind":"terminal","terminal":"completed"}}}`
	museRowDrainR2   = `{"sequence":165,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R2","event":{"kind":"inbox_item_drained","item_id":"I1","drain_target_run_stream":{"kind":"run","id":"R2"}}}}`
	museRowTermR2    = `{"sequence":200,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R2","event":{"kind":"terminal","terminal":"completed"}}}`
	museRowDrainR1   = `{"sequence":141,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R1","event":{"kind":"inbox_item_drained","item_id":"I1","drain_target_run_stream":{"kind":"run","id":"R1"}}}}`
	museRowTermR1b   = `{"sequence":266,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R1","event":{"kind":"terminal","terminal":"completed"}}}`
	museRowUnrelTerm = `{"sequence":90,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"R0","event":{"kind":"terminal","terminal":"completed"}}}`
)

func museChainState(t *testing.T, rows ...string) (bool, string) {
	t.Helper()
	c := newMuseSubagentChain("R1", 10)
	if err := c.advance(museChainFixture(t, rows...)); err != nil {
		t.Fatal(err)
	}
	pending, final, err := c.state()
	if err != nil {
		t.Fatal(err)
	}
	return pending, final
}

func TestMuseSubagentChain(t *testing.T) {
	if p, f := museChainState(t, museRowTermR1); p || f != "R1" {
		t.Fatalf("no subagent: pending=%v final=%q, want R1", p, f)
	}
	if p, f := museChainState(t, museRowSpawn, museRowQueued, museRowDrainR1, museRowTermR1b); p || f != "R1" {
		t.Fatalf("waited subagent: pending=%v final=%q, want R1", p, f)
	}
	if p, _ := museChainState(t, museRowSpawn, museRowQueued, museRowTermR1); !p {
		t.Fatal("background subagent not yet drained must be pending")
	}
	if p, _ := museChainState(t, museRowSpawn, museRowQueued, museRowTermR1, museRowDrainR2); !p {
		t.Fatal("result drained into a still-running run must be pending")
	}
	if p, f := museChainState(t, museRowUnrelTerm, museRowSpawn, museRowQueued, museRowTermR1, museRowDrainR2, museRowTermR2); p || f != "R2" {
		t.Fatalf("background subagent answered: pending=%v final=%q, want R2", p, f)
	}
}
