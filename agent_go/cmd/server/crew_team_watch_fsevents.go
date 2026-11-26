//go:build darwin && cgo

package server

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
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
	settled chan chan struct{}

	streamMu sync.Mutex // Flush and Stop never overlap
	stopping bool

	dropLogAt time.Time // only the event loop touches it
}

// logDrop names an event that made the watcher lose detail, at most every 10 s per folder.
func (t *crewTeamTreeWatcher) logDrop(root, name string, flags fsevents.EventFlags) {
	if time.Since(t.dropLogAt) < 10*time.Second {
		return
	}
	t.dropLogAt = time.Now()
	rel, err := filepath.Rel(root, name)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(name)
	}
	kind := "rescan_folder"
	switch {
	case flags&fsevents.RootChanged != 0:
		kind = "root_changed"
	case flags&fsevents.KernelDropped != 0:
		kind = "kernel_dropped"
	case flags&fsevents.UserDropped != 0:
		kind = "user_dropped"
	}
	log.Printf("[TREE_WATCH] event=lost_detail kind=%s path=%q", kind, filepath.ToSlash(rel))
}

func newCrewTeamWatcher(root string, w crewTreeSink) (crewTeamWatcher, error) {
	// FSEvents reports real paths (/private/var for /var); map them back onto the copy's root.
	real := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		real = r
	}
	es := &fsevents.EventStream{Paths: []string{root}, Latency: crewTeamEventsLatency, Flags: fsevents.FileEvents | fsevents.WatchRoot}
	if err := es.Start(); err != nil {
		return nil, err
	}
	t := &crewTeamTreeWatcher{es: es, done: make(chan struct{}), stopped: make(chan struct{}), settled: make(chan chan struct{})}
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
			case ack := <-t.settled:
				close(ack) // every batch before it has been noted
			case batch := <-es.Events:
				for _, ev := range batch {
					name := ev.Path
					if !strings.HasPrefix(name, "/") {
						name = "/" + name
					}
					if real != root && (name == real || strings.HasPrefix(name, real+"/")) {
						name = root + strings.TrimPrefix(name, real)
					}
					if ev.Flags&(fsevents.UserDropped|fsevents.KernelDropped|fsevents.RootChanged) != 0 {
						t.logDrop(root, name, ev.Flags)
						w.overflow()
						continue
					}
					if ev.Flags&fsevents.MustScanSubDirs != 0 {
						// macOS coalesced the changes below one folder: only that folder is unknown (#3878: treating
						// it as a drop of everything made every checkout of a busy project read again every few
						// minutes).
						t.logDrop(root, name, ev.Flags)
						if r, ok := w.(crewTreeRescanner); ok {
							r.rescan(name)
						} else {
							w.overflow()
						}
						continue
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
		t.streamMu.Lock()
		t.stopping = true
		t.es.Stop()
		t.streamMu.Unlock()
		close(t.stopped)
	}()
	return nil
}

// settle delivers the events FSEvents still holds back (its latency) and returns once they are noted, so a reply's
// capture sees an edit made just before it. Never called with the watch's lock held: noting takes it.
func (t *crewTeamTreeWatcher) settle() {
	t.streamMu.Lock()
	if t.stopping {
		t.streamMu.Unlock()
		return
	}
	t.es.Flush(true) // returns once every pending batch was handed to the loop
	t.streamMu.Unlock()
	ack := make(chan struct{})
	select {
	case t.settled <- ack:
		<-ack
	case <-t.done:
	}
}
