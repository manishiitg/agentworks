//go:build linux || darwin

package server

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func startLocalProfileSignal() func() {
	if !IsLocalMode() {
		return func() {}
	}
	dir := os.Getenv("AGENTWORKS_LOG_DIR")
	if dir == "" {
		dir = "logs"
	}
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-signals:
				heap, goroutine, err := writeLocalProfiles(dir)
				if err != nil {
					log.Printf("[LOCAL_PROFILE] failed err=%v", err)
					continue
				}
				log.Printf("[LOCAL_PROFILE] wrote heap=%s goroutine=%s", heap, goroutine)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { signal.Stop(signals); close(done) }) }
}
