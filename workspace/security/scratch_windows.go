//go:build windows

package security

import "os"

// Windows has no command sandbox (owner decision, 2026-10-09), so there is no per-command scratch area to allocate; a
// plain temporary folder is returned for callers that still ask for one.
func allocateScratch() (string, func(), error) {
	path, err := os.MkdirTemp("", "aw-")
	if err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(path) }, nil
}
