package handlers

import (
	"os"
	"reflect"
	"sync"
)

// This is a byte cache, not an authority cache. GetDocument still resolves the
// caller's path and stats it on every request before reaching this function.
var agentSnapshotFileCache struct {
	sync.Mutex
	path    string
	info    os.FileInfo
	content []byte
	tag     string
	parts   map[string]agentMailboxPart
}

func sameAgentSnapshotFile(a, b os.FileInfo) bool {
	// Include the OS change time, inode and mode in addition to size/mtime.
	// In-place edits restoring mtime must invalidate too. An atime change may
	// cause an extra read, which is harmless. Never cache on unknown metadata.
	return a != nil && b != nil && a.Sys() != nil && b.Sys() != nil &&
		os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime() == b.ModTime() &&
		reflect.DeepEqual(a.Sys(), b.Sys())
}

func readAgentSnapshotFile(path string, info os.FileInfo) ([]byte, string, error) {
	agentSnapshotFileCache.Lock()
	defer agentSnapshotFileCache.Unlock()
	return loadAgentMailbox(path, info)
}
