package handlers

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// #3035: a detached writer keeps its descriptor open; rotation must retain its
// inode while bounding the two archives, and never modify linked project data.
func TestCrewTemporaryLogRotationKeepsWriterAndRefusesLinkedFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	path := filepath.Join(dir, "server.log")
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	initial, err := writer.Stat()
	if err != nil {
		t.Fatal(err)
	}
	const limit = 1024
	for round := byte(1); round <= 3; round++ {
		raw := bytes.Repeat([]byte{round}, 64<<10)
		if _, err = writer.Write(raw); err != nil {
			t.Fatal(err)
		}
		if _, err = rotateCrewTemporaryLog(root, "server.log", limit); err != nil {
			t.Fatal(err)
		}
		current, err := os.Stat(path)
		if err != nil || !os.SameFile(initial, current) || current.Size() != 0 {
			t.Fatalf("writer inode changed: %v", err)
		}
		archived, err := os.ReadFile(path + ".1")
		if err != nil || !bytes.Equal(archived, raw[len(raw)-limit:]) {
			t.Fatalf("latest bounded tail lost: %v", err)
		}
		info, _ := os.Stat(path + ".1")
		if info.Mode().Perm() != 0600 {
			t.Fatal("archive permissions changed")
		}
		if round > 1 {
			old, err := os.ReadFile(path + ".2")
			if err != nil || !bytes.Equal(old, bytes.Repeat([]byte{round - 1}, limit)) {
				t.Fatalf("second archive incorrect: %v", err)
			}
		}
	}
	if _, err = os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal("more than two rotations retained")
	}
	if _, err = writer.WriteString("still running\n"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "still running\n" {
		t.Fatal("running writer lost output")
	}
	// A `cmd > x.log` writer (no O_APPEND) keeps its offset after truncation,
	// leaving a sparse file of the old apparent size. It must not be rotated
	// again: that wrote ~limit bytes of zeros into the archives every sweep.
	plain, err := os.OpenFile(filepath.Join(dir, "plain.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if _, err = plain.Write(bytes.Repeat([]byte{'p'}, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err = rotateCrewTemporaryLog(root, "plain.log", 64<<10); err != nil {
		t.Fatal(err)
	}
	if _, err = plain.WriteString("after\n"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "plain.log")); st.Size() <= 64<<10 {
		t.Fatal("expected a sparse file with the old apparent size")
	}
	if n, err := rotateCrewTemporaryLog(root, "plain.log", 64<<10); err != nil || n >= 0 {
		t.Fatalf("sparse log rotated again: freed=%d err=%v", n, err)
	}
	outside := filepath.Join(t.TempDir(), "project.txt")
	before := bytes.Repeat([]byte("private"), 1024)
	if err = os.WriteFile(outside, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(dir, "symlink.log")); err != nil {
		t.Fatal(err)
	}
	if n, err := rotateCrewTemporaryLog(root, "symlink.log", limit); err != nil || n >= 0 {
		t.Fatal("symlink log was rotated", err)
	}
	if err = os.Link(outside, filepath.Join(dir, "hardlink.log")); err != nil {
		t.Fatal(err)
	}
	if _, err = rotateCrewTemporaryLog(root, "hardlink.log", limit); err == nil {
		t.Fatal("hard-linked data accepted as log")
	}
	if raw, _ := os.ReadFile(outside); !bytes.Equal(raw, before) {
		t.Fatal("linked project data changed")
	}
}

// The periodic walk must not crawl package/compiler caches or rotate their
// files, and local mode must not apply server housekeeping to a person's Mac.
func TestCrewTemporaryLogSweepSkipsHeavyTreesAndLocalMode(t *testing.T) {
	saved := crewTemporaryLogBytes
	crewTemporaryLogBytes = 64 << 10
	defer func() { crewTemporaryLogBytes = saved }()
	docs := t.TempDir()
	root := filepath.Join(docs, "Crew", "example", ".tmp")
	writeLarge := func(relative string) string {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		// Real bytes: a sparse file has no allocated data and is never rotated.
		if _, err = f.Write(bytes.Repeat([]byte("x"), int(crewTemporaryLogBytes)+1)); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ordinary := writeLarge("server.log")
	skipped := []string{"node_modules/example/large.log", "node-compile-cache/large.log", "node-compile-cache-v1/large.log", ".git/large.log", ".cache/large.log", "__pycache__/large.log", "ordinary-data.txt"}
	for _, path := range skipped {
		writeLarge(path)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	sweepCrewTemporaryLogs(docs)
	if _, err := os.Stat(ordinary + ".1"); !os.IsNotExist(err) {
		t.Fatal("local log rotated", err)
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	sweepCrewTemporaryLogs(docs)
	if st, err := os.Stat(ordinary); err != nil || st.Size() != 0 {
		t.Fatal("ordinary temporary log not rotated", err)
	}
	if st, err := os.Stat(ordinary + ".1"); err != nil || st.Size() != crewTemporaryLogBytes {
		t.Fatal("bounded archive missing", err)
	}
	for _, path := range skipped {
		st, err := os.Stat(filepath.Join(root, path))
		if err != nil || st.Size() != crewTemporaryLogBytes+1 {
			t.Fatalf("heavy tree/data changed: %s %v", path, err)
		}
	}
}
