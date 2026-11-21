//go:build linux

package cpuprofile

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #2989: other host accounts cannot trigger profiling or redirect its output.
// Exercise real CPU/allocs writers in the same private directory as the marker.
func TestPrivateRequestWritesProfilesAndRefusesSharedOrSymlinkMarkers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pprof")
	root, err := privateRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.WriteFile(filepath.Join(dir, "REQUEST"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if request(root) == nil {
		t.Fatal("service-owned marker refused")
	}
	if err = capture(root, "workspace", 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if request(root) == nil {
		t.Fatal("capture consumed the other service's marker")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range files {
		if filepath.Ext(entry.Name()) != ".pprof" {
			continue
		}
		f, err := root.Open(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		info, _ := f.Stat()
		if info.Mode().Perm() != 0600 {
			t.Fatalf("profile permissions: %v", info.Mode())
		}
		reader, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		f.Close()
		if err != nil || len(data) == 0 {
			t.Fatalf("invalid profile: %v", err)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("want CPU and allocation snapshots, got %d", count)
	}
	if err = os.Chmod(filepath.Join(dir, "REQUEST"), 0644); err != nil {
		t.Fatal(err)
	}
	if request(root) != nil {
		t.Fatal("nonprivate marker accepted")
	}
	if err = os.Remove(filepath.Join(dir, "REQUEST")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("workspace-cpu.pprof", filepath.Join(dir, "REQUEST")); err != nil {
		t.Fatal(err)
	}
	if request(root) != nil {
		t.Fatal("symlink marker accepted")
	}
	if err = os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if other, e := privateRoot(dir); e == nil {
		other.Close()
		t.Fatal("shared directory accepted")
	}
}
