package server

// Team sync moves content-defined chunks (owner, 5 Nov, PLAT-856-535): a file is split into chunks by its content
// (restic/chunker, one fixed polynomial, so the same bytes always cut the same way), each chunk is stored once in the
// Team's chunk store by its sha256, and a file version is its ordered chunk list. A round sends only the chunks the
// server lacks and receives only the chunks the copy lacks, in requests of at most a few MB; a change of a few bytes in
// a large file moves one or two chunks, and history costs only new chunks. One path for every file size.

import (
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

const (
	// crewTeamCommitBatch bounds the files in one commit.
	crewTeamCommitBatch = 2000
	// The one sync limit (crewsync.SyncMaxFiles / SyncMaxBytes); each file keeps publication.MaxFileBytes.
	crewTeamMaxFiles = crewsync.SyncMaxFiles
	crewTeamMaxBytes = crewsync.SyncMaxBytes
	// crewTeamSyncFormat names the chunked layout: a Team's content and every copy's binding start fresh with it.
	crewTeamSyncFormat = "chunks-1"
)

// The server's chunk store: <content>/chunks/<id[:2]>/<id>, written once, verified on write and on read.
func crewTeamChunkStore(project string) (string, error) {
	d, e := crewTeamContentDir(project)
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "chunks"), nil
}
