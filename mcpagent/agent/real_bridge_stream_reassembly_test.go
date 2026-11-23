package mcpagent

import (
	"strings"
	"testing"
)

// piRecordedTableFragments is the REAL tail of pi's clean stream, copied verbatim
// from testdata/agent-reviews/TestRealBridgeStreaming_pi.json. Every fragment is
// marked IsDelta=true by the adapter, and the splits land mid-cell — this is the
// exact shape that made a garbled table pass review.
var piRecordedTableFragments = []cleanChunk{
	{text: "I will display the", isDelta: true},
	{text: " contents of the newly created report markdown file to verify its structure.", isDelta: true},
	{text: "| Field | Value |\n|-------|-------", isDelta: true},
	{text: "|\n| build_id | BUILD_ID_3ff7ad76e877 |\n|", isDelta: true},
	{text: " status | ok |", isDelta: true},
}

// TestReassembleCleanStreamFixesDeltaGarbling is the regression guard for the
// formatting bug: joining delta fragments with "\n" injects newlines mid-row, so
// the streamed markdown table renders broken while STILL satisfying the old
// Contains("|") && Contains(buildID) assertions.
//
// The subtests below deliberately assert BOTH directions — that the old join is
// broken and the new one is not. Without the "old join is broken" half this test
// would pass against the unfixed code and prove nothing.
func TestReassembleCleanStreamFixesDeltaGarbling(t *testing.T) {
	const wantRow = "| build_id | BUILD_ID_3ff7ad76e877 |"

	t.Run("the old newline join garbles the table", func(t *testing.T) {
		var texts []string
		for _, c := range piRecordedTableFragments {
			texts = append(texts, c.text)
		}
		old := strings.Join(texts, "\n")

		// The trap, reproduced exactly: BOTH weak assertions pass on broken output,
		// and so does an anchor on the build_id row — that row happens to sit
		// wholly inside one fragment. Only the structural invariant catches it.
		if !strings.Contains(old, "|") || !strings.Contains(old, "BUILD_ID_3ff7ad76e877") {
			t.Fatal("precondition failed: the old assertions were supposed to pass on garbled output")
		}
		if !strings.Contains(old, wantRow) {
			t.Fatal("precondition failed: the build_id row was supposed to survive, which is why " +
				"a row-anchored assertion is NOT a sufficient formatting check")
		}
		bad := malformedTableLines(old)
		if len(bad) == 0 {
			t.Fatalf("expected the newline join to leave broken table row(s), found none — "+
				"the fixture no longer reproduces the bug:\n%s", old)
		}
		// The separator row is where pi's fragment boundary actually landed.
		if !strings.Contains(strings.Join(bad, "\n"), "|-------|-------") {
			t.Errorf("expected the separator row to be the broken one, got %q", bad)
		}
	})

	t.Run("verbatim delta concatenation preserves the table", func(t *testing.T) {
		got := reassembleCleanStream(piRecordedTableFragments)
		if bad := malformedTableLines(got); len(bad) > 0 {
			t.Fatalf("reassembled stream still has broken table row(s) %q:\n%s", bad, got)
		}
		if !strings.Contains(got, wantRow) {
			t.Fatalf("reassembled stream lost the intact table row %q:\n%s", wantRow, got)
		}
		if !strings.Contains(got, "|-------|-------|\n| build_id |") {
			t.Fatalf("the separator row did not rejoin cleanly:\n%s", got)
		}
	})

	// Block providers (claude: delta_content_count=0) carry whole messages per
	// chunk, so they must still be newline-separated — a verbatim concatenation
	// would run their narration sentences together.
	t.Run("block chunks stay newline separated", func(t *testing.T) {
		got := reassembleCleanStream([]cleanChunk{
			{text: "I'll read the build ID and create the report table."},
			{text: "Now I'll write the report with the build ID."},
		})
		want := "I'll read the build ID and create the report table.\nNow I'll write the report with the build ID."
		if got != want {
			t.Fatalf("block chunks were not newline separated:\ngot  %q\nwant %q", got, want)
		}
	})
}
