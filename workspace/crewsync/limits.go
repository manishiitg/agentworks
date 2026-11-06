package crewsync

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// One limit for every sync (owner, 6 Nov: "one sync process"): Team sync, moves and publishing move only the chunks
// the other side lacks, so a bigger Crew costs a longer first transfer, not every one. Each file keeps
// publication.MaxFileBytes.
const (
	SyncMaxFiles = publication.MaxFiles
	SyncMaxBytes = publication.MaxBytes
)

type Limits struct {
	Bytes, FileBytes int64
	Files            int
}

// SyncLimits are the sync limits. The test context's method is
// implemented only in fixtures, never by an HTTP request, environment or setting.
func SyncLimits(ctx context.Context) Limits {
	if fixture, ok := ctx.(interface{ CrewSyncTestLimits() Limits }); ok {
		return fixture.CrewSyncTestLimits()
	}
	return Limits{SyncMaxBytes, publication.MaxFileBytes, SyncMaxFiles}
}
