package server

// Team sync moves content-defined chunks (owner, 5 Nov, PLAT-856-535): a file is split into chunks by its content
// (restic/chunker, one fixed polynomial, so the same bytes always cut the same way), each chunk is stored once in the
// Team's chunk store by its sha256, and a file version is its ordered chunk list. A round sends only the chunks the
// server lacks and receives only the chunks the copy lacks, in requests of at most a few MB; a change of a few bytes in
// a large file moves one or two chunks, and history costs only new chunks. One path for every file size.

import (
	"path/filepath"
)

const (
	// crewTeamCommitBatch bounds the files in one commit.
	crewTeamCommitBatch = 2000
	// The Team's own limits, not Publish's one-upload limit (owner, 6 Nov): sync moves only changed chunks after
	// the first copy, so a bigger folder costs a longer first download, not every round. Each file keeps
	// publication.MaxFileBytes.
	crewTeamMaxFiles       = 50000
	crewTeamMaxBytes int64 = 4 << 30
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
