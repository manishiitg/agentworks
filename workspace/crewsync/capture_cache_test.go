package crewsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-552/509 (one sync process, 6 Nov): a warm capture reads and copies nothing for unchanged files, and an edit
// costs only that file. A file that keeps changing is read again a bounded number of times, then named, never skipped:
// a move is a one-time handover.
func TestCaptureCachedReadsOnlyChangedFilesAndBoundsABusyFile(t *testing.T) {
	root := t.TempDir()
	roots := crewroot.Roots{Files: root, Resources: root}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("first "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(1100 * time.Millisecond) // past the racy window of a just-written file
	cache := Cache{}
	first, err := CaptureCached(t.Context(), roots, nil, t.TempDir(), nil, cache)
	if err != nil || first.Read != 3 || len(first.Changes) != 3 || len(first.Changes[0].Chunks) != 1 {
		t.Fatalf("first: read=%d changes=%d %v", first.Read, len(first.Changes), err)
	}
	defer first.Close()
	// Platforms without strong FileInfo identity deliberately recheck raw
	// bytes; all transfer, staging and retry assertions still apply there.
	warmRead, warmReused, editedRead, editedReused := 0, 3, 1, 2
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		warmRead, warmReused, editedRead, editedReused = 3, 0, 3, 0
	}
	warm, err := CaptureCached(t.Context(), roots, first.Files, t.TempDir(), nil, cache)
	if err != nil || warm.Read != warmRead || warm.Reused != warmReused || len(warm.Changes) != 0 {
		t.Fatalf("warm: read=%d reused=%d changes=%d %v", warm.Read, warm.Reused, len(warm.Changes), err)
	}
	defer warm.Close()
	if entries, _ := os.ReadDir(warm.StagingDir); len(entries) != 0 {
		t.Fatalf("a warm capture copied %d files", len(entries))
	}
	if err = os.WriteFile(filepath.Join(root, "b.md"), []byte("second version"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	edited, err := CaptureCached(t.Context(), roots, first.Files, t.TempDir(), nil, cache)
	if err != nil || edited.Read != editedRead || edited.Reused != editedReused || len(edited.Changes) != 1 || !strings.HasSuffix(edited.Changes[0].Path, "b.md") {
		t.Fatalf("edit: read=%d reused=%d changes=%+v %v", edited.Read, edited.Reused, edited.Changes, err)
	}
	defer edited.Close()

	// A first/fresh destination freezes bytes even when their hashes were
	// cached: report actual source reads rather than hash-cache hits.
	fresh, err := CaptureCached(t.Context(), roots, nil, t.TempDir(), nil, cache)
	if err != nil || fresh.Read != 3 || fresh.Reused != 0 || fresh.Copied != 3 {
		t.Fatalf("fresh destination reads=%d reused=%d copied=%d %v", fresh.Read, fresh.Reused, fresh.Copied, err)
	}
	defer fresh.Close()

	// Remove a cold source just after its hash/chunks return. A second
	// working-file read would fail; the one-pass private copy must survive.
	func() {
		oneRoot := t.TempDir()
		onePath := filepath.Join(oneRoot, "once.md")
		const body = "frozen in the same source pass"
		if err := os.WriteFile(onePath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		real := readCaptureFile
		defer func() { readCaptureFile = real }()
		readCaptureFile = func(c Cache, ctx context.Context, root, rel, key string, each func([]byte) error) (CachedFile, bool, error) {
			entry, read, err := real(c, ctx, root, rel, key, each)
			if err == nil {
				if err = os.Remove(onePath); err != nil {
					t.Fatal(err)
				}
			}
			return entry, read, err
		}
		one, err := CaptureCached(t.Context(), crewroot.Roots{Files: oneRoot, Resources: oneRoot}, nil, t.TempDir(), nil, Cache{})
		defer one.Close()
		if err != nil || one.Read != 1 || one.Copied != 1 || len(one.Changes) != 1 {
			t.Fatalf("cold one-pass capture: %+v %v", one, err)
		}
		raw, err := os.ReadFile(one.Changes[0].DataPath)
		if err != nil || string(raw) != body {
			t.Fatalf("private copy differs from hashed source: %q %v", raw, err)
		}
	}()

	real := readCaptureFile
	t.Cleanup(func() { readCaptureFile = real })
	changing := 0
	readCaptureFile = func(c Cache, ctx context.Context, root, rel, key string, each func([]byte) error) (CachedFile, bool, error) {
		if rel == "c.md" && changing > 0 {
			changing--
			return CachedFile{}, false, ErrFileChanged
		}
		return real(c, ctx, root, rel, key, each)
	}
	changing = captureTries - 1
	settled, err := CaptureCached(t.Context(), roots, edited.Files, t.TempDir(), nil, cache)
	if err != nil || settled.Retried != captureTries-1 {
		t.Fatalf("settled after retries: retried=%d %v", settled.Retried, err)
	}
	defer settled.Close()
	changing = captureTries
	gaveUp, err := CaptureCached(t.Context(), roots, edited.Files, t.TempDir(), nil, cache)
	defer gaveUp.Close()
	var busy *BusyFileError
	if !errors.As(err, &busy) || !strings.HasSuffix(busy.Key, "c.md") || !strings.Contains(err.Error(), "c.md keeps changing") {
		t.Fatalf("busy file: %v", err)
	}
}
