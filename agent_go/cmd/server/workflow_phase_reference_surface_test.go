package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestReferenceSurfaceAttachesOutsideWorkshopSessionGuard pins PLAT-119.
//
// Every Pulse step opens with "load builder-reference and follow it exactly".
// That bundle used to be attached inside `if workshopSession != nil`, together
// with workshop TOOL registration. Workshop creation is deliberately skipped
// when the session is already stopped ("aborting workshop creation to prevent
// orphaned executions") — which is exactly the state Pulse's finalizer runs in
// after a coding-agent terminal dies. salesoutreach 2026-08-17: workshop
// creation aborted at 18:26:32, Gate therefore had no procedure, improvised,
// and produced a verdict indistinguishable from a real pass. The only reason it
// was ever noticed is that the model volunteered it.
//
// Tools may legitimately be unavailable. The procedure describing how the agent
// should behave must not disappear with them, so the attach must sit OUTSIDE
// that guard. This asserts the structural property rather than the call's
// presence, because the call was present the whole time — just unreachable.
func TestReferenceSurfaceAttachesOutsideWorkshopSessionGuard(t *testing.T) {
	source, err := os.ReadFile("workflow_phase_tools.go")
	if err != nil {
		t.Fatalf("read workflow_phase_tools.go: %v", err)
	}
	lines := strings.Split(string(source), "\n")

	indentOf := func(line string) int { return len(line) - len(strings.TrimLeft(line, "\t")) }

	guardLine, guardIndent := -1, 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "if workshopSession != nil {" {
			guardLine, guardIndent = i, indentOf(line)
			break
		}
	}
	if guardLine < 0 {
		t.Fatal("workshopSession guard not found — this test must be re-pointed at its replacement, not deleted")
	}

	// The guard body ends at the first line closing it at the same indent.
	guardEnd := -1
	for i := guardLine + 1; i < len(lines); i++ {
		if indentOf(lines[i]) == guardIndent && strings.TrimSpace(lines[i]) == "}" {
			guardEnd = i
			break
		}
	}
	if guardEnd < 0 {
		t.Fatal("could not find the end of the workshopSession guard")
	}

	attach := regexp.MustCompile(`guidance\.Attach(?:Configured)?ReferenceSurface(?:WithMCP)?\(`)
	var attachLines []int
	for i, line := range lines {
		if attach.MatchString(line) {
			attachLines = append(attachLines, i)
		}
	}
	if len(attachLines) == 0 {
		t.Fatal("AttachReferenceSurface is no longer called: Autopilot steps would silently run without their procedures")
	}
	for _, at := range attachLines {
		if at > guardLine && at < guardEnd {
			t.Fatalf("AttachReferenceSurface is inside the workshopSession guard (line %d, guard %d-%d): a stopped "+
				"session skips workshop creation, so Gate/Review+Fix/Finalize would lose builder-reference and improvise",
				at+1, guardLine+1, guardEnd+1)
		}
	}
}
