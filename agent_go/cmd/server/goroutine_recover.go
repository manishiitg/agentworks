package server

import (
	"log"
	"runtime/debug"
	"time"
)

// goRecover runs fn in its own goroutine. A panic in any goroutine stops the
// whole server, so schedule, Watcher and automation goroutines start through
// this: a panic is logged with its stack and only that piece of work ends.
// Deferred cleanup inside fn (releasing a chat, a lease) still runs first.
func goRecover(name string, fn func()) {
	go func() {
		defer recoverGoroutine(name)
		fn()
	}()
}

func recoverGoroutine(name string) {
	if r := recover(); r != nil {
		log.Printf("[PANIC] %s stopped on an internal error: %v\n%s", name, r, debug.Stack())
	}
}

// runSchedulerClock keeps the schedule clock alive: if one tick panics, the
// panic is logged and the clock starts again a second later.
func (s *SchedulerService) runSchedulerClock(ctxDone <-chan struct{}, loop func()) {
	for {
		panicked := func() (p bool) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[PANIC] schedule clock stopped on an internal error, restarting: %v\n%s", r, debug.Stack())
					p = true
				}
			}()
			loop()
			return false
		}()
		if !panicked {
			return
		}
		select {
		case <-ctxDone:
			return
		case <-time.After(time.Second):
		}
	}
}
