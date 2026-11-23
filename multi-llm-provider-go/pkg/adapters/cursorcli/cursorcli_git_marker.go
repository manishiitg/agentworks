package cursorcli

import (
	"os"
	"path/filepath"
	"sync"
)

// cursor-agent finds .cursor/mcp.json from the nearest git root, so a folder
// that is not its own repository gets a minimal .git marker for the session.
// Sessions sharing a folder share one marker: the first creates it, the last
// removes it, and only if it is still the directory we created. A folder that
// already is a git root (a real repository) is never touched.
var cursorGitMarkers = struct {
	sync.Mutex
	held map[string]*cursorGitMarkerHold
}{held: map[string]*cursorGitMarkerHold{}}

type cursorGitMarkerHold struct {
	count   int
	created os.FileInfo
}

func acquireCursorGitMarker(workingDir string) (func(), error) {
	dir := filepath.Clean(workingDir)
	cursorGitMarkers.Lock()
	defer cursorGitMarkers.Unlock()
	if hold := cursorGitMarkers.held[dir]; hold != nil {
		hold.count++
		return releaseCursorGitMarker(dir), nil
	}
	if cursorWorkingDirIsGitRoot(dir) {
		return func() {}, nil
	}
	if err := initCursorWorkspaceGitMarker(dir); err != nil {
		return nil, err
	}
	created, _ := os.Lstat(filepath.Join(dir, ".git"))
	cursorGitMarkers.held[dir] = &cursorGitMarkerHold{count: 1, created: created}
	return releaseCursorGitMarker(dir), nil
}

func releaseCursorGitMarker(dir string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			cursorGitMarkers.Lock()
			defer cursorGitMarkers.Unlock()
			hold := cursorGitMarkers.held[dir]
			if hold == nil {
				return
			}
			hold.count--
			if hold.count > 0 {
				return
			}
			delete(cursorGitMarkers.held, dir)
			gitDir := filepath.Join(dir, ".git")
			// Do not remove a replacement installed by another owner.
			if current, err := os.Lstat(gitDir); err == nil && hold.created != nil && os.SameFile(hold.created, current) {
				_ = os.RemoveAll(gitDir)
			}
		})
	}
}
