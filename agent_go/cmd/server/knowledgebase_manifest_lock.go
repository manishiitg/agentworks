package server

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"golang.org/x/sys/unix"
)

// Serialize the CAS check and rename with ordinary server manifest updates.
func knowledgeManifestLock(path string) (func(), error) {
	f, err := os.OpenFile(path+".kb-lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, &knowledgebase.Error{Code: "VERSION_CONFLICT", Message: "Project configuration is being updated; retry.", Retryable: true}
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}

// Ordinary manifest saves tolerate a short competing server write. Keep the
// CAS callers' nonblocking primitive and check their revisions after locking.
func knowledgeManifestLockWithWait(ctx context.Context, path string) (func(), error) {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		unlock, err := knowledgeManifestLock(path)
		if err == nil {
			if cancelled := ctx.Err(); cancelled != nil {
				unlock()
				return nil, cancelled
			}
			return unlock, nil
		}
		var conflict *knowledgebase.Error
		if !errors.As(err, &conflict) || conflict.Code != "VERSION_CONFLICT" {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, err
		case <-retry.C:
		}
	}
}
