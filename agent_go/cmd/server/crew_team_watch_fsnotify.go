//go:build !windows && !(darwin && cgo)

package server

import "github.com/fsnotify/fsnotify"

// Linux (inotify: one watch per folder) and a Mac built without cgo (kqueue: one open file per watched name, so the
// name budget applies): each folder is added on its own.
const crewTeamTreeWatching = false

type crewTeamFolderWatcher struct{ fw *fsnotify.Watcher }

func newCrewTeamWatcher(_ string, w *crewTeamWatch) (crewTeamWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				w.note(ev.Name, ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0)
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
				w.overflow() // events were dropped (the system's queue overflowed)
			}
		}
	}()
	return crewTeamFolderWatcher{fw}, nil
}

func (f crewTeamFolderWatcher) Add(dir string) error { return f.fw.Add(dir) }
func (f crewTeamFolderWatcher) Remove(dir string)    { _ = f.fw.Remove(dir) }
func (f crewTeamFolderWatcher) Close() error         { return f.fw.Close() }
