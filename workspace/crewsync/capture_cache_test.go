package crewsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	warm, err := CaptureCached(t.Context(), roots, first.Files, t.TempDir(), nil, cache)
	if err != nil || warm.Read != 0 || warm.Reused != 3 || len(warm.Changes) != 0 {
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
	if err != nil || edited.Read != 1 || edited.Reused != 2 || len(edited.Changes) != 1 || !strings.HasSuffix(edited.Changes[0].Path, "b.md") {
		t.Fatalf("edit: read=%d reused=%d changes=%+v %v", edited.Read, edited.Reused, edited.Changes, err)
	}
	defer edited.Close()

	real := readCaptureFile
	t.Cleanup(func() { readCaptureFile = real })
	changing := 0
	readCaptureFile = func(c Cache, ctx context.Context, root, rel, key string) (CachedFile, bool, error) {
		if rel == "c.md" && changing > 0 {
			changing--
			return CachedFile{}, false, ErrFileChanged
		}
		return real(c, ctx, root, rel, key)
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
