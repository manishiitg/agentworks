package crewroot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #2989: reuse decoding during repeated resolution without retaining deleted,
// replaced or in-place-edited ownership records. Root admission is not cached.
func TestRegistrySnapshotTracksOwnershipWritesWithoutRepeatedDecode(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	dir := filepath.Join(state, "ownership")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "projects.json")
	projects := map[string]Record{}
	for i := range 300 {
		key := fmt.Sprintf("project-%d", i)
		projects["work/"+key] = Record{Product: "work", Folder: key, ID: key, Owner: "owner"}
	}
	raw, _ := json.Marshal(map[string]any{"projects": projects})
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := records()
	if err != nil || len(first) != 300 {
		t.Fatalf("initial records: %d %v", len(first), err)
	}
	info, _ := os.Stat(file)
	if _, supported := registryChangeTime(info); supported {
		// A 300-project registry formerly decoded thousands of strings/objects per
		// resolution. Keep the warm path below one allocation per project.
		allocations := testing.AllocsPerRun(5, func() {
			if _, err := NewResolver(state); err != nil {
				t.Fatal(err)
			}
		})
		if allocations >= 300 {
			t.Fatalf("unchanged registry still allocates per project: %.0f", allocations)
		}
	}
	changed := projects["work/project-0"]
	changed.Owner = "other"
	projects["work/project-0"] = changed
	raw, _ = json.Marshal(map[string]any{"projects": projects})
	time.Sleep(2 * time.Millisecond)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	next, err := records()
	if err != nil || next["work/project-0"].Owner != "other" {
		t.Fatalf("stale ownership after same-size preserved-mtime edit: %v", err)
	}
	if first["work/project-0"].Owner != "owner" {
		t.Fatal("in-flight snapshot was mutated")
	}
	replacement := filepath.Join(dir, "replacement.json")
	if err = os.WriteFile(replacement, []byte(`{"projects":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, file); err != nil {
		t.Fatal(err)
	}
	next, err = records()
	if err != nil || len(next) != 0 {
		t.Fatalf("replaced registry retained ownership: %v", err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	next, err = records()
	if err != nil || len(next) != 0 {
		t.Fatal("deleted registry retained ownership")
	}
	if err = os.WriteFile(file, []byte(`invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = records(); err == nil {
		t.Fatal("invalid registry reused old records")
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	next, err = records()
	if err != nil || len(next) != 300 {
		t.Fatalf("recreated registry unavailable: %v", err)
	}
}
