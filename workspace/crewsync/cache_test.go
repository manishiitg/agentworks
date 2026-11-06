package crewsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// PLAT-856-552: a warm Move/Publish manifest must not open unchanged contents;
// preserved-mtime edits must still be read, and saved caches hold no file bytes.
func TestCacheReusesStableFilesAndPersistsOnlyVersionMetadata(t *testing.T) {
	root := t.TempDir()
	data := []byte("example content stays in the working file, never in the cache")
	for _, name := range []string{"item.txt", "other.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadCache(filepath.Join(root, "missing.cache"))
	if err != nil || c == nil || len(c) != 0 {
		t.Fatalf("missing derived cache: %v %+v", err, c)
	}
	var opens int
	originalOpen := openCachedFile
	openCachedFile = func(dir *os.Root, name string) (*os.File, error) {
		opens++
		return originalOpen(dir, name)
	}
	defer func() { openCachedFile = originalOpen }()
	read := func(name string) (CachedFile, bool) {
		t.Helper()
		entry, didRead, err := c.Read(t.Context(), root, name, "files/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return entry, didRead
	}
	for _, name := range []string{"item.txt", "other.txt"} {
		if _, didRead := read(name); !didRead {
			t.Fatal("new file skipped its initial read")
		}
	}
	sum := sha256.Sum256(data)
	if got := c["files/item.txt"]; got.File.Hash != hex.EncodeToString(sum[:]) || len(got.Chunks) != 1 || got.Chunks[0].ID != got.File.Hash {
		t.Fatalf("file and chunk hashes do not identify the raw bytes: %+v", got)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		// Replay an entry recorded in its ctime tick: even an equal stamp
		// must cause an actual content open until a later read settles it.
		racy := c["files/item.txt"]
		racy.ReadStarted = time.Unix(0, racy.Stamp.Ctime)
		c["files/item.txt"] = racy
		before := opens
		if _, didRead := read("item.txt"); !didRead || opens != before+1 {
			t.Fatal("racy equal stamp reused contents")
		}
		time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)) + 10*time.Millisecond)
		read("item.txt")
		read("other.txt") // settle any read recorded in the write's own second
		before = opens
		for _, name := range []string{"item.txt", "other.txt"} {
			if _, didRead := read(name); didRead {
				t.Fatalf("warm unchanged %s was read", name)
			}
		}
		if opens != before {
			t.Fatal("warm reuse still opened file contents")
		}
	}
	cachePath := filepath.Join(t.TempDir(), "manifest.cache")
	if err = c.Save(cachePath); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cachePath)
	if err != nil || bytes.Contains(raw, data) {
		t.Fatalf("cache missing or contains file content: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cachePath)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("cache permissions: %v %v", info, err)
		}
	}
	c, err = LoadCache(cachePath)
	if err != nil || len(c) != 2 || c["files/item.txt"].File.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("cache round trip: %v %+v", err, c)
	}
	info, err := os.Stat(filepath.Join(root, "item.txt"))
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'E'
	if err = os.WriteFile(filepath.Join(root, "item.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(filepath.Join(root, "item.txt"), info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	before := opens
	if entry, didRead := read("item.txt"); !didRead || entry.File.Hash == hex.EncodeToString(sum[:]) || opens != before+1 {
		t.Fatal("same-size edit preserving mtime did not read exactly that file")
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		if _, didRead := read("other.txt"); didRead {
			t.Fatal("editing one file caused the other file to be reread")
		}
	}
	// Mutate the native file immediately after open: don't cache a version
	// whose path/descriptor no longer matches its initial stamp.
	openCachedFile = func(dir *os.Root, name string) (*os.File, error) {
		f, err := originalOpen(dir, name)
		if err == nil {
			if err = os.WriteFile(filepath.Join(root, name), []byte("changed during capture"), 0600); err != nil {
				f.Close()
				return nil, err
			}
		}
		return f, err
	}
	delete(c, "files/item.txt")
	if _, _, err = c.Read(t.Context(), root, "item.txt", "files/item.txt"); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("raced read was not retryable: %v", err)
	}
	if _, exists := c["files/item.txt"]; exists {
		t.Fatal("unstable version entered cache")
	}
	if err = os.WriteFile(cachePath, []byte("broken cache"), 0600); err != nil {
		t.Fatal(err)
	}
	if recovered, err := LoadCache(cachePath); err != nil || len(recovered) != 0 {
		t.Fatalf("broken derived cache was not rebuilt: %v %+v", err, recovered)
	}
}
