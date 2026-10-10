package handlers

import (
	"fmt"
	"strings"
	"testing"
)

func TestApplyDiffPatchFlexible(t *testing.T) {
	tests := []struct {
		name           string
		currentContent string
		diffContent    string
		expectedError  bool
		expectedResult string
	}{
		{
			name: "Traditional unified diff",
			currentContent: `# Todo List

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
+**Patch Test**: This was added via unified diff.

 ## Objective
`,
			expectedError: false,
			expectedResult: `# Todo List
**Patch Test**: This was added via unified diff.

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
		},
		{
			name:           "Line ending normalization test (CRLF to LF)",
			currentContent: "# Todo List\r\n\r\n## Objective\r\n- Complete project analysis",
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
+**CRLF Test**: Added with normalized line endings

 ## Objective
`,
			expectedError: false,
			expectedResult: `# Todo List
**CRLF Test**: Added with normalized line endings

## Objective
- Complete project analysis`,
		},
		{
			name: "Diff without newline ending is normalized",
			currentContent: `# Todo List

## Objective`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,2 +1,3 @@
 # Todo List
+**No Newline**: This should be normalized`,
			expectedError: false,
			expectedResult: `# Todo List
**No Newline**: This should be normalized

## Objective`,
		},
		{
			name: "Diff with proper newline ending",
			currentContent: `# Todo List

## Objective`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,2 +1,3 @@
 # Todo List
+**With Newline**: This should work

## Objective
`,
			expectedError: false,
			expectedResult: `# Todo List
**With Newline**: This should work

## Objective`,
		},
		{
			name: "Missing diff headers (should fail validation)",
			currentContent: `# Todo List

## Objective`,
			diffContent: `@@ -1,2 +1,3 @@
 # Todo List
+**No Headers**: This should fail

## Objective
`,
			expectedError: true, // Should fail validation due to missing headers
		},
		{
			name: "Context mismatch (should fail patch)",
			currentContent: `# Todo List

## Objective
- Complete project analysis`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
+**Context Mismatch**: This should fail

## Different Content
`,
			expectedError: true, // Should fail due to context mismatch
		},
		{
			name: "Simplified diff format with exact context",
			currentContent: `# Todo List

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ ... @@
 # Todo List
+**Simplified Patch**: This was added via simplified diff.

 ## Objective
`,
			expectedError: false,
			expectedResult: `# Todo List
**Simplified Patch**: This was added via simplified diff.

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
		},
		{
			name: "Simplified addition with unique full-file context",
			currentContent: `# Todo List

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ ... @@
 # Todo List
+**Single Addition**: This is a test.

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research
`,
			expectedError: false,
			expectedResult: `# Todo List
**Single Addition**: This is a test.

## Objective
- Complete project analysis
- Generate comprehensive report

## Notes
- Leverages tavily-search for comprehensive research`,
		},
		{
			name: "Empty diff (should fail validation)",
			currentContent: `# Todo List

## Objective
- Complete project analysis`,
			diffContent:   "",
			expectedError: true, // Empty diff should fail validation
		},
		{
			name: "Malformed diff",
			currentContent: `# Todo List

## Objective`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,2 +1,3 @@
 # Todo List
+**Malformed**: This should fail
## Objective
`,
			expectedError: true,
		},
		{
			name: "Ambiguous non-contiguous bullet context is rejected",
			currentContent: `# Todo List

## Objective
- Complete project analysis`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
- Complete project analysis
+- Test patch: Added via AI agent
`,
			expectedError: true,
		},
		{
			name: "Duplicate exact fallback context is rejected",
			currentContent: `## Section
same value

## Section
same value`,
			diffContent: `--- a/todo.md
+++ b/todo.md
@@ -100,2 +100,3 @@
 ## Section
 same value
+new value
`,
			expectedError: true,
		},
		{
			name: "Real agent-generated diff pattern (should be auto-corrected)",
			currentContent: `## Notes
- Each todo builds on previous research to create comprehensive analysis
- Success criteria are measurable and tied to specific deliverables
- Dependencies ensure logical progression of analysis depth`,
			diffContent: `--- a/Tasks/Workflow-Testing/todo.md
+++ b/Tasks/Workflow-Testing/todo.md
@@ -200,3 +200,4 @@ - Each todo builds on previous research to create comprehensive analysis
 - Success criteria are measurable and tied to specific deliverables
 - Dependencies ensure logical progression of analysis depth
+- Test patch: Added via diff tool
`,
			expectedError: false, // Should be auto-corrected and succeed
			expectedResult: `## Notes
- Each todo builds on previous research to create comprehensive analysis
- Success criteria are measurable and tied to specific deliverables
- Dependencies ensure logical progression of analysis depth
- Test patch: Added via diff tool`,
		},
		{
			name: "Ambiguous invalid line references are rejected",
			currentContent: `## Notes
- Each todo builds on previous research to create comprehensive analysis
- Success criteria are measurable and tied to specific deliverables
- Dependencies ensure logical progression of analysis depth
-Updated for testing.`,
			diffContent: `--- a/Tasks/Workflow-Testing/todo.md
+++ b/Tasks/Workflow-Testing/todo.md
@@ -last,2 +last-1,1 @@
 - Dependencies ensure logical progression of analysis depth
-Updated for testing.`,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyDiffPatchFlexible(tt.currentContent, tt.diffContent)

			if tt.expectedError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.expectedResult != "" && result != tt.expectedResult {
				t.Errorf("Result mismatch.\nExpected:\n%s\n\nGot:\n%s", tt.expectedResult, result)
			}
		})
	}
}

// TestNormalizeLineEndings tests the line ending normalization function
func TestNormalizeLineEndings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "LF endings (no change)",
			input:    "line1\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "CRLF endings",
			input:    "line1\r\nline2\r\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "CR endings",
			input:    "line1\rline2\rline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "Mixed endings",
			input:    "line1\nline2\r\nline3\r",
			expected: "line1\nline2\nline3\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLineEndings(tt.input)
			if result != tt.expected {
				t.Errorf("Expected: %q, Got: %q", tt.expected, result)
			}
		})
	}
}

// TestValidateDiffFormat tests the diff format validation function
func TestValidateDiffFormat(t *testing.T) {
	tests := []struct {
		name        string
		diffContent string
		expectError bool
	}{
		{
			name: "Valid diff format",
			diffContent: `--- a/file.md
+++ b/file.md
@@ -1,3 +1,4 @@
 # Header
+New line
` + " \n" + ` ## Section
`,
			expectError: false,
		},
		{
			name:        "Empty diff",
			diffContent: "",
			expectError: true,
		},
		{
			name: "Missing headers",
			diffContent: `@@ -1,2 +1,3 @@
 # Header
+New line`,
			expectError: true,
		},
		{
			name: "Missing hunk headers",
			diffContent: `--- a/file.md
+++ b/file.md
 # Header
+New line`,
			expectError: true,
		},
		{
			name: "Diff without newline ending",
			diffContent: `--- a/file.md
+++ b/file.md
@@ -1,2 +1,3 @@
 # Header
+New line`,
			expectError: true,
		},
		{
			name: "Too short diff",
			diffContent: `--- a/file.md
+++ b/file.md`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDiffFormat(tt.diffContent)
			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestCorrectAgentGeneratedDiff tests the automatic correction of agent-generated diffs
func TestCorrectAgentGeneratedDiff(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Correct contiguous bullet context with an explicit anchor",
			input: `--- a/todo.md
+++ b/todo.md
@@ -3,2 +3,3 @@
 ## Objective
- Complete project analysis
+- Test patch: Added via AI agent
`,
			expected: `--- a/todo.md
+++ b/todo.md
@@ -3,2 +3,3 @@
 ## Objective
 - Complete project analysis
+- Test patch: Added via AI agent
`,
		},
		{
			name: "Do not correct non-contiguous bullet context",
			input: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
- Complete project analysis
+- Test patch: Added via AI agent
`,
			expected: `--- a/todo.md
+++ b/todo.md
@@ -1,2 +1,2 @@
 # Todo List
- Complete project analysis
+- Test patch: Added via AI agent
`,
		},
		{
			name: "No changes needed for correct diff",
			input: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
+**New addition**: Added via unified diff.
` + " \n" + ` ## Objective
`,
			expected: `--- a/todo.md
+++ b/todo.md
@@ -1,3 +1,4 @@
 # Todo List
+**New addition**: Added via unified diff.
` + " \n" + ` ## Objective
`,
		},
		{
			name: "Multiple contiguous bullet context corrections",
			input: `--- a/todo.md
+++ b/todo.md
@@ -3,3 +3,4 @@
 ## Objective
- Complete project analysis
- Generate comprehensive report
+- Test patch: Added via AI agent
`,
			expected: `--- a/todo.md
+++ b/todo.md
@@ -3,3 +3,4 @@
 ## Objective
 - Complete project analysis
 - Generate comprehensive report
+- Test patch: Added via AI agent
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a simple current content for testing
			currentContent := `# Todo List

## Objective
- Complete project analysis
- Generate comprehensive report`

			result := correctAgentGeneratedDiff(tt.input, currentContent)
			if result != tt.expected {
				t.Errorf("Expected:\n%s\n\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

// Tests for removed functions have been removed since we're using the simplified approach

// PLAT-023: on 2026-08-04, a stale-context patch failure against a 150KB file
// cost 4 extra tool calls to recover — one to learn the match failed, more to
// re-read the file and locate the real content by hand. The fallback matcher
// already scans every candidate position computing mismatches; these tests
// prove the closest position's real content now travels with the failure
// instead of being discarded, and that it is actually enough to fix the diff
// in one corrected retry — the acceptance boundary, not just "the failure is
// well-formed."
func largeFixtureFileContent(lines int) string {
	rows := make([]string, lines)
	for i := 0; i < lines; i++ {
		rows[i] = fmt.Sprintf("line-%05d: some representative content for row %d", i, i)
	}
	return strings.Join(rows, "\n")
}

func TestApplyAgentGeneratedDiffFallbackReportsBoundedContextOnLargeFileMismatch(t *testing.T) {
	const totalLines = 3000
	const targetLine = 2100 // deep enough into a large file that "just re-read it" is expensive

	rows := strings.Split(largeFixtureFileContent(totalLines), "\n")
	// The file's real content has drifted since the agent last read it: the
	// target line changed; its neighbors did not. A single-line hunk cannot
	// distinguish "close" from "anywhere else" — every non-matching position
	// ties at exactly one mismatch — so the hunk carries two lines of real
	// surrounding context on each side. Only the window anchored at
	// targetLine-2 can match that surrounding context at all; everywhere else
	// in the file, all five lines differ (the fixture's content is unique per
	// row), which is the actual signal a "closest match" ranking needs.
	original := rows[targetLine]
	current := strings.Replace(original, fmt.Sprintf("row %d", targetLine), fmt.Sprintf("row %d v2", targetLine), 1)
	rows[targetLine] = current
	currentContent := strings.Join(rows, "\n")

	hunkWithStaleTarget := func(target string) string {
		return strings.Join([]string{rows[targetLine-2], rows[targetLine-1], target, rows[targetLine+1], rows[targetLine+2]}, "\n ")
	}
	staleDiff := fmt.Sprintf("--- a/large.txt\n+++ b/large.txt\n@@ -1,5 +1,5 @@\n %s\n+replaced\n", hunkWithStaleTarget(original))

	_, err := applyAgentGeneratedDiffFallback(currentContent, staleDiff)
	if err == nil {
		t.Fatal("stale context was accepted instead of rejected — this must never silently apply an ambiguous hunk")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Closest match: 1 of 5") {
		t.Fatalf("hint does not report the true per-candidate mismatch count (must not be capped at the zero-tolerance threshold): %v", err)
	}
	if !strings.Contains(msg, current) {
		t.Fatalf("hint does not contain the file's actual current content at the near-miss position:\n%s", msg)
	}
	if !strings.Contains(msg, fmt.Sprintf("file line %d", targetLine-1)) {
		t.Fatalf("hint does not name the correct 1-based file line (%d):\n%s", targetLine-1, msg)
	}
	// Bounded: this file is 3000 lines; the hint must not become a second copy
	// of it.
	if len(msg) > 4000 {
		t.Fatalf("hint is not bounded: %d bytes", len(msg))
	}

	// The actual acceptance boundary: the hint must contain enough to fix the
	// diff in one corrected retry, not just explain the failure.
	correctedDiff := fmt.Sprintf("--- a/large.txt\n+++ b/large.txt\n@@ -1,5 +1,5 @@\n %s\n+replaced\n", hunkWithStaleTarget(current))
	result, err := applyAgentGeneratedDiffFallback(currentContent, correctedDiff)
	if err != nil {
		t.Fatalf("corrected diff built from the hint's own reported content still failed: %v", err)
	}
	if !strings.Contains(result, "replaced") {
		t.Fatalf("corrected retry did not apply the intended change")
	}
}

func TestApplyAgentGeneratedDiffFallbackHintStaysBoundedOnAPathologicallyLongLine(t *testing.T) {
	rows := strings.Split(largeFixtureFileContent(50), "\n")
	rows[10] = strings.Repeat("x", 10000) // one absurdly long current line
	currentContent := strings.Join(rows, "\n")

	staleDiff := "--- a/f.txt\n+++ b/f.txt\n@@ -1,1 +1,1 @@\n-this line does not exist anywhere in the file\n+replacement\n"

	_, err := applyAgentGeneratedDiffFallback(currentContent, staleDiff)
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	if len(err.Error()) > 4000 {
		t.Fatalf("one long line blew up the hint instead of being truncated: %d bytes", len(err.Error()))
	}
}

func TestApplyAgentGeneratedDiffFallbackDoesNotRecommendAnArbitraryTiedNearMatch(t *testing.T) {
	currentContent := strings.Join([]string{
		"first section",
		"shared before",
		"current value A",
		"shared after",
		"separator",
		"second section",
		"shared before",
		"current value B",
		"shared after",
	}, "\n")
	staleDiff := strings.Join([]string{
		"--- a/f.txt",
		"+++ b/f.txt",
		"@@ -1,3 +1,3 @@",
		" shared before",
		"-stale value",
		"+replacement",
		" shared after",
		"",
	}, "\n")

	_, err := applyAgentGeneratedDiffFallback(currentContent, staleDiff)
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 locations are equally close") {
		t.Fatalf("tied nearest matches were not reported as ambiguous: %s", msg)
	}
	if strings.Contains(msg, "Current content there:") {
		t.Fatalf("an arbitrary tied location was presented as the closest retry target: %s", msg)
	}
}

// TestCountContentLines locks down the two edge cases plain
// len(strings.Split(content, "\n")) gets wrong: an empty file is 0 lines
// (not 1), and a trailing newline must not count as an extra blank line.
// Getting this wrong made verifyDiffApplied reject a legitimate single-line
// file-creation diff as corrupted (see TestDiffPatchCreationWithControllingTTY).
func TestCountContentLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty file", "", 0},
		{"one line, no trailing newline", "hello", 1},
		{"one line, with trailing newline", "hello\n", 1},
		{"three lines, with trailing newline", "a\nb\nc\n", 3},
		{"three lines, no trailing newline", "a\nb\nc", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countContentLines(tt.content); got != tt.want {
				t.Errorf("countContentLines(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

// TestVerifyDiffAppliedCatchesSilentPartialApply reproduces the shape of the
// customer-login live finding: a two-hunk patch where the first hunk (net +1
// line) applied correctly, but the second hunk's own changes were silently
// dropped and an unrelated trailing line was deleted too (net -1) — so the
// file's real net change (0) does not match what the diff's own +/- lines
// claim (+1). The specific internal mechanism that produced this isn't
// reproducible on demand (see PLAT-19x); this test proves the safety net
// catches the failure SHAPE regardless of which internal path caused it.
func TestVerifyDiffAppliedCatchesSilentPartialApply(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,4 @@\n line1\n+added-line\n line2\n line3\n"
	// Simulates the corrupted result: the addition landed, but an unrelated
	// trailing line was silently dropped, so the net change is 0, not +1.
	corruptedResult := "line1\nadded-line\nline2\n"

	err := verifyDiffApplied(original, diff, corruptedResult)
	if err == nil {
		t.Fatal("expected verifyDiffApplied to reject a result whose line-count change does not match the diff's claim")
	}
	if !strings.Contains(err.Error(), "unexpected line-count change") {
		t.Errorf("error should name the mismatch, got: %v", err)
	}
}

// TestVerifyDiffAppliedAcceptsACorrectApply guards against false positives:
// a normal, correctly-applied multi-hunk patch must pass verification.
func TestVerifyDiffAppliedAcceptsACorrectApply(t *testing.T) {
	original := "line1\nline2\nline3\nline4\nline5\n"
	diff := "--- a/f.txt\n+++ b/f.txt\n@@ -1,2 +1,3 @@\n line1\n+added-line\n line2\n@@ -4,2 +5,2 @@\n-line4\n+changed-line4\n line5\n"
	correctResult := "line1\nadded-line\nline2\nline3\nchanged-line4\nline5\n"

	if err := verifyDiffApplied(original, diff, correctResult); err != nil {
		t.Fatalf("verifyDiffApplied rejected a correctly-applied patch: %v", err)
	}
}

// TestDiffClaimedLineDeltaCountsBodyLinesNotHeaders proves the delta is
// computed from the actual +/- body lines, not the (sometimes wrong,
// LLM-supplied) @@ header counts — the same signal correctAgentGeneratedDiff
// already trusts over the header.
func TestDiffClaimedLineDeltaCountsBodyLinesNotHeaders(t *testing.T) {
	// Header claims +1,+1 (net 0) but the body has one addition, no removals.
	diff := "--- a/f.txt\n+++ b/f.txt\n@@ -1,1 +1,1 @@\n line1\n+added-line\n"
	if got := diffClaimedLineDelta(diff); got != 1 {
		t.Fatalf("diffClaimedLineDelta = %d, want 1 (from the body's +/- lines, ignoring the header)", got)
	}
}

func TestApplyAgentGeneratedDiffFallbackOmitsHintWhenFileIsShorterThanTheHunk(t *testing.T) {
	currentContent := "only one line"
	diffContent := "--- a/f.txt\n+++ b/f.txt\n@@ -1,3 +1,3 @@\n line a\n line b\n line c\n"

	_, err := applyAgentGeneratedDiffFallback(currentContent, diffContent)
	if err == nil {
		t.Fatal("expected an error when the file is shorter than the hunk")
	}
	if strings.Contains(err.Error(), "Closest match:") {
		t.Fatalf("hint claims a closest match that cannot exist: %v", err)
	}
}

// PLAT-201: harness:tool:diff_patch_workspace_file:unicode-context-mismatch
// theorized "silent normalization or transport-level mangling of non-ASCII
// characters" between the read path and the diff-apply path. Neither
// applyDiffPatchFlexible nor its fallback matcher do any Unicode folding --
// context comparison is a plain strings.TrimSpace + == check -- so this
// proves the apply path itself does not mangle anything: byte-exact
// Unicode context always applies, and ASCII-folded context (em dash -> "-",
// curly quotes -> straight quotes, arrow -> "->") is correctly rejected as a
// genuine mismatch, not silently corrupted. The real-world failure this
// finding reproduced -- a large hand-copied hunk failing while a
// difflib-generated diff from the same file succeeded -- is consistent with
// an agent silently ASCII-folding punctuation while retyping text, a known
// LLM generation habit, not a platform bug.
func TestApplyDiffPatchDoesNotMangleUnicodePunctuation(t *testing.T) {
	original := "Line one\n" +
		"The system uses a request—response cycle\n" +
		"Flow: input → output → done\n" +
		"She said “ok” and left\n" +
		"Line five\n"

	t.Run("byte-exact unicode context applies cleanly", func(t *testing.T) {
		diff := "--- a/notes.md\n+++ b/notes.md\n@@ -1,5 +1,5 @@\n" +
			" Line one\n" +
			" The system uses a request—response cycle\n" +
			"-Flow: input → output → done\n" +
			"+Flow: input → output → DONE\n" +
			" She said “ok” and left\n" +
			" Line five\n"
		out, err := ApplyDiffPatchDirect(original, diff)
		if err != nil {
			t.Fatalf("byte-exact unicode context should apply cleanly, got: %v", err)
		}
		if !strings.Contains(out, "Flow: input → output → DONE") {
			t.Fatalf("patch did not apply the expected change:\n%s", out)
		}
	})

	t.Run("ascii-folded context is rejected as a mismatch, not silently applied", func(t *testing.T) {
		// Same logical edit, but every non-ASCII punctuation mark has been
		// folded to its ASCII look-alike -- exactly what an LLM commonly
		// does when regenerating text by hand instead of copying it exactly.
		diff := "--- a/notes.md\n+++ b/notes.md\n@@ -1,5 +1,5 @@\n" +
			" Line one\n" +
			" The system uses a request-response cycle\n" +
			"-Flow: input -> output -> done\n" +
			"+Flow: input -> output -> DONE\n" +
			" She said \"ok\" and left\n" +
			" Line five\n"
		out, err := ApplyDiffPatchDirect(original, diff)
		if err == nil {
			t.Fatalf("ASCII-folded (non-byte-exact) context should be rejected as a mismatch, got success with output=%q", out)
		}
		if !strings.Contains(err.Error(), "could not find matching context lines") {
			t.Fatalf("expected a context-mismatch error, got: %v", err)
		}
	})
}

// PLAT-202: harness:diff_patch_workspace_file (2026-08-23, a JSON registry
// file) reported the same failure class PLAT-192 later fixed for a
// different file: applied:true while hunk 2 of a two-hunk diff silently
// failed to update its target scalar fields AND deleted an unrelated
// trailing context line ("title") with no replacement. This predates
// PLAT-192's own harness finding by about a day and matches its description
// almost exactly -- the same underlying bug class, observed twice.
//
// This does not re-derive the internal mechanism (PLAT-192 already
// documented that as unconfirmed); it reproduces the INCIDENT'S OWN
// reported before/after content directly and confirms PLAT-192's
// verifyDiffApplied safety net -- already shipped -- catches this exact
// shape: the diff claims a net +1 line delta (one covers[] append, one
// 4-for-4 scalar replacement), but the reported broken result only changed
// by +0 (scalars left stale, title line vanished).
func TestVerifyDiffAppliedCatchesTheReportedJSONRegistryIncidentShape(t *testing.T) {
	original := `      "covers": [
        "existing-thing"
      ],
      "last_updated": "2026-08-06T12:30:25Z",
      "last_updated_by": "old-agent",
      "section_count": 19,
      "size_bytes": 15588,
      "title": "page-playbook-detail"
`
	diff := "--- a/_index.json\n+++ b/_index.json\n" +
		"@@ -1,7 +1,8 @@\n" +
		"       \"covers\": [\n" +
		"         \"existing-thing\",\n" +
		"+        \"new-thing\"\n" +
		"       ],\n" +
		"-      \"last_updated\": \"2026-08-06T12:30:25Z\",\n" +
		"-      \"last_updated_by\": \"old-agent\",\n" +
		"-      \"section_count\": 19,\n" +
		"-      \"size_bytes\": 15588,\n" +
		"+      \"last_updated\": \"2026-08-23T09:19:02Z\",\n" +
		"+      \"last_updated_by\": \"new-agent\",\n" +
		"+      \"section_count\": 20,\n" +
		"+      \"size_bytes\": 16783,\n" +
		"       \"title\": \"page-playbook-detail\"\n"

	// As reported: the covers[] append applied, but the scalar fields were
	// left at their OLD values and the trailing title line was dropped with
	// no replacement.
	reportedBrokenResult := `      "covers": [
        "existing-thing",
        "new-thing"
      ],
      "last_updated": "2026-08-06T12:30:25Z",
      "last_updated_by": "old-agent",
      "section_count": 19,
      "size_bytes": 15588,
`
	err := verifyDiffApplied(original, diff, reportedBrokenResult)
	if err == nil {
		t.Fatal("verifyDiffApplied did not catch the reported silent-partial-apply shape")
	}
	if !strings.Contains(err.Error(), "unexpected line-count change") {
		t.Fatalf("expected the line-count-mismatch error, got: %v", err)
	}
}
