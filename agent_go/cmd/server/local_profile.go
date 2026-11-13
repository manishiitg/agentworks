package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

// Profiles are private diagnostics, never served by an HTTP route. Do not log
// their contents: stacks may identify private files or application work.
func writeLocalProfiles(dir string) (string, string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", "", err
	}
	base := filepath.Join(dir, "pprof-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	write := func(path string, profile func(io.Writer) error) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = profile(f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	heap, goroutine := base+".heap", base+".goroutine"
	// No forced GC or memory-limit change: capture the current runtime profile.
	if err := write(heap, pprof.WriteHeapProfile); err != nil {
		return "", "", fmt.Errorf("heap: %w", err)
	}
	if err := write(goroutine, func(w io.Writer) error { return pprof.Lookup("goroutine").WriteTo(w, 0) }); err != nil {
		return heap, "", fmt.Errorf("goroutine: %w", err)
	}
	return heap, goroutine, nil
}
