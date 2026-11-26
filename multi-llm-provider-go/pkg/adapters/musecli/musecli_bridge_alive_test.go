package musecli

import "testing"

func TestMuseHasDescendantNamedFindsTheBridgeBelowThePane(t *testing.T) {
	procs := map[int]museProc{
		100: {ppid: 1, comm: "bash"},        // the pane
		101: {ppid: 100, comm: "muse-bin"},  // Muse
		102: {ppid: 101, comm: "mcpbridge"}, // its bridge
		200: {ppid: 1, comm: "bash"},        // another pane
		201: {ppid: 200, comm: "muse-bin"},  // another Muse
		202: {ppid: 1, comm: "mcpbridge"},   // someone else's bridge, not below this pane
		203: {ppid: 201, comm: "node"},      // unrelated child
	}
	if !museHasDescendantNamed(100, procs, "mcpbridge") {
		t.Fatal("the bridge below pane 100 must be found")
	}
	if museHasDescendantNamed(200, procs, "mcpbridge") {
		t.Fatal("pane 200 has no bridge below it: another session's bridge must not count")
	}
}

func TestMuseHasDescendantNamedHandlesCyclesAndEmptyTables(t *testing.T) {
	if museHasDescendantNamed(5, nil, "mcpbridge") {
		t.Fatal("an empty table has no bridge")
	}
	cyclic := map[int]museProc{5: {ppid: 6, comm: "a"}, 6: {ppid: 5, comm: "b"}}
	if museHasDescendantNamed(5, cyclic, "mcpbridge") {
		t.Fatal("a cycle must terminate without finding anything")
	}
}
