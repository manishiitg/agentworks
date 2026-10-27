package workflowfiles

import "testing"

// PLAT-856-145: translating _files must preserve connection exclusions, even
// for lifecycle tree walks which authorize descendants after normalization.
func TestCrewFileSubtreeRetainsAllowAndExclusionRules(t *testing.T) {
	g := &FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"_files/src"}, BlockedPaths: []string{"_files/src/private"}, BlockedWritePaths: []string{"_files/src/generated"}, ReadOnlyPaths: []string{"_files/src/vendor"}}
	sub := g.Subtree("_files")
	for _, p := range []string{"src/main.go", "src/private/key.txt", "src/generated/api.go", "src/vendor/lib.go", "other.txt"} {
		for _, write := range []bool{false, true} {
			if sub.Allows(p, write) != g.Allows("_files/"+p, write) {
				t.Fatalf("changed grant for %s write=%v", p, write)
			}
		}
	}
	if err := sub.Validate(); err != nil {
		t.Fatal(err)
	}
	if (&FolderGuard{BlockedPaths: []string{"_files"}}).Subtree("_files").Allows("anything", false) {
		t.Fatal("lost whole-subtree denial")
	}
}
