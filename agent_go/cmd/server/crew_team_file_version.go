package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

var errCrewTeamFileChanged = errors.New("shared file changed while being read; retrying")

func crewTeamSameVersion(a, b chunkedFile) bool {
	return a.Hash == b.Hash && a.Size == b.Size && a.Exec == b.Exec
}

// readCrewTeamVersion binds a version to the file that was actually read,
// checking both its open descriptor and its current path before caching it.
func readCrewTeamVersion(root, rel string, op crewTeamOnPurpose, previous chunkedFile) (crewTeamLocalFile, map[string]chunkSource, error) {
	entry := crewTeamLocalFile{ReadAt: time.Now()}
	f, err := openCrewTeamFile(root, rel, op)
	if err != nil {
		return entry, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return entry, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
		return entry, nil, errCrewTeamFileChanged
	}
	entry.Stamp = crewsync.StampOf(info)
	executable := info.Mode()&0o111 != 0
	if runtime.GOOS == "windows" && previous.Hash != "" {
		executable = previous.Exec // NTFS does not represent the Unix execute bit
	}
	sources := map[string]chunkSource{}
	var offset int64
	entry.Version, err = chunkReader(f, info.Size(), executable, func(id string, data []byte) error {
		if _, ok := sources[id]; !ok {
			sources[id] = chunkSource{path: rel, offset: offset, size: int64(len(data))}
		}
		offset += int64(len(data))
		return nil
	})
	if err != nil {
		return entry, nil, fmt.Errorf("%w: %v", errCrewTeamFileChanged, err)
	}
	after, err := f.Stat()
	if err != nil {
		return entry, nil, fmt.Errorf("%w: %v", errCrewTeamFileChanged, err)
	}
	current, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return entry, nil, fmt.Errorf("%w: %v", errCrewTeamFileChanged, err)
	}
	if entry.Stamp != crewsync.StampOf(after) || entry.Stamp != crewsync.StampOf(current) || !os.SameFile(info, current) {
		return entry, nil, errCrewTeamFileChanged
	}
	return entry, sources, nil
}
