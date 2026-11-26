package musecli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMuseSubagentUsageIsReportedOnceAndOnlyAfterTheFloor(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subagent", "abc", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o755); err != nil {
		t.Fatal(err)
	}
	floor := time.Now()
	row := func(seq int, at time.Time, in int) string {
		return fmt.Sprintf(`{"sequence":%d,"recorded_at":%d,"payload":{"kind":"run","event":{"kind":"goal_usage_attribution","record":{"quantity":{"reported":true,"input_tokens":%d,"output_tokens":5}}}}}`+"\n", seq, at.UnixMicro(), in)
	}
	model := `{"sequence":1,"recorded_at":1,"payload_type":"run.model.configured","payload":{"kind":"run_model","record":{"model_id":"muse-spark-1.3-contributor"}}}` + "\n"
	body := model + row(1, floor.Add(-time.Hour), 999) + row(2, floor.Add(time.Second), 100) + row(3, floor.Add(2*time.Second), 50)
	if err := os.WriteFile(sub, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := museSubagentUsage(parent, floor)
	if len(got) != 1 || got[0].ID != "abc" || got[0].Model != "muse-spark-1.3-contributor" || got[0].Usage.InputTokens != 150 || got[0].Usage.OutputTokens != 10 {
		t.Fatalf("first read = %+v, want one subagent with rows 2-3 (150 in / 10 out), the row before the floor skipped", got)
	}
	if again := museSubagentUsage(parent, floor); len(again) != 0 {
		t.Fatalf("second read = %+v, want nothing: rows are reported once", again)
	}
}
