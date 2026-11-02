package crewsync

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type Limits struct {
	Bytes, FileBytes int64
	Files            int
}

// SyncLimits keeps shipping limits unchanged. The test context's method is
// implemented only in fixtures, never by an HTTP request, environment or setting.
func SyncLimits(ctx context.Context) Limits {
	if fixture, ok := ctx.(interface{ CrewSyncTestLimits() Limits }); ok {
		return fixture.CrewSyncTestLimits()
	}
	return Limits{publication.MaxBytes, publication.MaxFileBytes, publication.MaxFiles}
}
