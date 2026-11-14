package filesnapshots

import (
	"context"
	"slices"
	"time"

	"golang.org/x/sync/singleflight"
)

// Every reply starts and ends with a capture that asks each checkout in the workspace which files changed, several
// Git processes per checkout. Chats of one workspace do this at the same time, so a busy laptop ran 4 per capture x
// every capture (a log showed 4-10 s per checkout where one call takes ~20 ms on an idle machine). Two rules keep
// that bounded without changing what is reported: at most discoverySlots such reads run at once on the whole
// server, and captures that ask about the same checkout at the same moment share one read.
const (
	discoverySlots       = 6
	discoveryRepoTimeout = 10 * time.Second
)

var (
	discoveryLimit  = make(chan struct{}, discoverySlots)
	discoveryFlight singleflight.Group
)

// shareRepoRead runs read once for every overlapping call with the same key, inside the server-wide limit, and returns
// its own copy of the answer. The read gets the first caller's values but not its cancellation, and discoveryRepoTimeout
// from the moment it asks for a slot; each caller still stops waiting when its own ctx ends.
func shareRepoRead(ctx context.Context, key string, read func(context.Context) (gitTurnRepo, error)) (repo gitTurnRepo, shared bool, err error) {
	type answer struct {
		repo gitTurnRepo
		err  error
	}
	done := discoveryFlight.DoChan(key, func() (any, error) {
		run, cancel := context.WithTimeout(context.WithoutCancel(ctx), discoveryRepoTimeout)
		defer cancel()
		select {
		case discoveryLimit <- struct{}{}:
			defer func() { <-discoveryLimit }()
		case <-run.Done():
			return answer{err: run.Err()}, nil
		}
		r, e := read(run)
		return answer{r, e}, nil
	})
	select {
	case res := <-done:
		a := res.Val.(answer)
		a.repo.Dirty = slices.Clone(a.repo.Dirty)
		return a.repo, res.Shared, a.err
	case <-ctx.Done():
		return gitTurnRepo{}, false, ctx.Err()
	}
}
