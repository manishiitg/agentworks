// Package sessionlease owns the activity deadline for retained coding-agent
// sessions. It deliberately knows nothing about tmux or provider registries:
// adapters arm it on accepted activity and supply their own expiry cleanup.
package sessionlease

import (
	"sync"
	"time"
)

// Lease is a reusable, concurrency-safe activity deadline. Its zero value is
// ready to use. A generation prevents an already-queued timer callback from
// expiring a lease that newer activity has re-armed.
type Lease struct {
	mu           sync.Mutex
	timer        *time.Timer
	generation   uint64
	stopped      bool
	lastActivity time.Time
}

// Arm records activity and replaces the current deadline. It returns false
// after Stop, allowing callers to reject input racing final teardown.
func (l *Lease) Arm(timeout time.Duration, onExpire func()) bool {
	if l == nil || timeout <= 0 || onExpire == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return false
	}
	if l.timer != nil {
		l.timer.Stop()
	}
	l.lastActivity = time.Now()
	l.generation++
	generation := l.generation
	l.timer = time.AfterFunc(timeout, func() {
		l.expire(generation, onExpire)
	})
	return true
}

// Pause invalidates the current deadline without permanently stopping the
// lease. Providers call it while an ordinary turn owns the session; Release
// arms a fresh idle deadline afterward.
func (l *Lease) Pause() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.generation++
}

// Stop permanently invalidates the lease and every queued callback.
func (l *Lease) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.stopped = true
	l.generation++
}

// LastActivity returns when Arm most recently accepted activity.
func (l *Lease) LastActivity() time.Time {
	if l == nil {
		return time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastActivity
}

func (l *Lease) expire(generation uint64, onExpire func()) {
	l.mu.Lock()
	if l.stopped || generation != l.generation {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	l.generation++
	l.timer = nil
	l.mu.Unlock()
	onExpire()
}
