//go:build darwin && cgo

package server

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsevents"
)

// On a Mac one FSEvents stream watches a copy's whole folder: no open file per folder or file (kqueue held one per
// watched name and ran Macs with many Team copies out of files), so the name budget does not apply.
const crewTeamTreeWatching = true

const crewTeamEventsLatency = 500 * time.Millisecond

type crewTeamTreeWatcher struct {
	es      *fsevents.EventStream
	done    chan struct{} // closed by Close: stop noting
	stopped chan struct{} // closed once the stream has stopped
}

func newCrewTeamWatcher(root string, w *crewTeamWatch) (crewTeamWatcher, error) {
	// FSEvents reports real paths (/private/var for /var); map them back onto the copy's root.
	real := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		real = r
	}
	es := &fsevents.EventStream{Paths: []string{root}, Latency: crewTeamEventsLatency, Flags: fsevents.FileEvents | fsevents.WatchRoot}
	if err := es.Start(); err != nil {
		return nil, err
	}
	t := &crewTeamTreeWatcher{es: es, done: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		for {
			select {
			case <-t.done:
				// Keep taking batches until the stream has stopped, so a delivery in progress never blocks Stop.
				for {
					select {
					case <-es.Events:
					case <-t.stopped:
						return
					}
				}
			case batch := <-es.Events:
				for _, ev := range batch {
					if ev.Flags&(fsevents.MustScanSubDirs|fsevents.UserDropped|fsevents.KernelDropped|fsevents.RootChanged) != 0 {
						w.overflow()
						continue
					}
					name := ev.Path
					if !strings.HasPrefix(name, "/") {
						name = "/" + name
					}
					if real != root && (name == real || strings.HasPrefix(name, real+"/")) {
						name = root + strings.TrimPrefix(name, real)
					}
					w.note(name, ev.Flags&(fsevents.ItemCreated|fsevents.ItemRemoved|fsevents.ItemRenamed) != 0)
				}
			}
		}
	}()
	return t, nil
}

func (t *crewTeamTreeWatcher) Add(string) error { return nil }
func (t *crewTeamTreeWatcher) Remove(string)    {}

// Close is called with the watch's lock held; the stream stops in the background so a batch waiting on that lock
// cannot block it.
func (t *crewTeamTreeWatcher) Close() error {
	close(t.done)
	go func() {
		t.es.Stop()
		close(t.stopped)
	}()
	return nil
}
