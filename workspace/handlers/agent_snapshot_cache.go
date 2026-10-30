package handlers

import (
	"crypto/sha256"
	"fmt"
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
	cache := &agentSnapshotFileCache
	if cache.path == path && sameAgentSnapshotFile(info, cache.info) {
		return cache.content, cache.tag, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	tag := fmt.Sprintf("\"%x\"", sha256.Sum256(content))
	// Cache only bytes read while the file metadata stayed stable. Concurrent
	// replacement must cause the next request to read again, never validate
	// old bytes using the replacement's metadata.
	after, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if sameAgentSnapshotFile(info, after) {
		cache.path, cache.info, cache.content, cache.tag = path, after, content, tag
	} else {
		cache.path, cache.info, cache.content, cache.tag = "", nil, nil, ""
	}
	return content, tag, nil
}
