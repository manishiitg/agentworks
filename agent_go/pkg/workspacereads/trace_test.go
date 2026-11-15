package workspacereads

import (
	"fmt"
	"strings"
	"testing"
)

// The one-off read trace must stay bounded and never retain path/query data.
func TestReadTraceBoundsCallerCountsAndOmitsPathsAndQueries(t *testing.T) {
	t.Setenv("WORKSPACE_READ_TRACE", "0")
	traceActive.Store(true)
	defer func() { traceActive.Store(false); traceCounts = map[string]int64{} }()
	for i := range 500 {
		traceRead(fmt.Sprintf("private/person/file%d.json?token=private-value", i))
	}
	if len(traceCounts) > 129 {
		t.Fatal("unbounded trace", len(traceCounts))
	}
	var total int64
	for key, count := range traceCounts {
		if strings.Contains(key, "private") || strings.Contains(key, "token") {
			t.Fatal("private path/query entered trace")
		}
		total += count
	}
	if total != 500 {
		t.Fatalf("lost reads: %d", total)
	}
	finishReadTrace()
	traceRead("after.json")
	if len(traceCounts) != 0 {
		t.Fatal("trace continued after its window")
	}
}
