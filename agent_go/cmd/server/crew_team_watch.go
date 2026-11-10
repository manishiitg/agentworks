package server

import (
	"log"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Team sync watches each copy's folders instead of reading every shared file every round (owner, 6 Nov: Team sync
// lighter). A round reads only the folders where something changed (crewTeamScanChanged); a full scan still runs at
// the start, every few minutes, after the computer wakes, when what the share reads changes, and after a failed round,
// so a missed event is caught. A copy that cannot be watched (too many names, or the system refused) has full scans
// only, less often than the old every-round walk.

const (
	crewTeamFullScanEvery  = 5 * time.Minute
	crewTeamUnwatchedEvery = 30 * time.Second // full scans of a copy with no watcher
	// A gap this long between rounds: the computer slept, events may be lost. Above the once-a-minute check of a network
	// with a live relay (crewTeamCheckAfter): at one minute every paced round read as a wake and scanned everything.
	crewTeamWokeAfter       = 3 * time.Minute
	crewTeamWakeDelay       = time.Second      // a burst of writes starts one round
	crewTeamForgetWatchIdle = 10 * time.Minute // a copy no round asked for: its watcher closes
	// crewTeamWatchNames bounds the names in watched folders: a Mac's watcher holds one open file per name.
	crewTeamWatchNames = 20000
)

// crewTeamWake starts the next round early, once a burst of changes has settled.
var crewTeamWake = make(chan struct{}, 1)

func crewTeamWakeSoon() {
	select {
	case crewTeamWake <- struct{}{}:
	default:
	}
}

// crewTeamWatches: copy record folder -> its watch.
var crewTeamWatches sync.Map

type crewTeamWatch struct {
	mu       sync.Mutex
	root     string
	key      string // what the share reads, at the last full scan
	w        *fsnotify.Watcher
	off      bool // not watched: full scans only
	closed   bool // forgotten: never watches again
	dirs     map[string]bool
	changed  map[string]bool // path -> read all it holds
	full     bool
	rules    bool
	lastFull time.Time
	lastTake time.Time
	timer    *time.Timer
}

// crewTeamScanPlan is what one round reads: everything (full), the changed paths, or nothing (idle).
type crewTeamScanPlan struct {
	full    bool
	changed map[string]bool
	rules   bool
	watched int
}

func (p crewTeamScanPlan) idle() bool { return !p.full && len(p.changed) == 0 }

func (p crewTeamScanPlan) mode() string {
	switch {
	case p.full:
		return "full"
	case len(p.changed) > 0:
		return "changed"
	}
	return "idle"
}

// crewTeamScanKey is what decides which files a copy's scan reads: a change of it needs a full scan.
func crewTeamScanKey(b crewTeamLocal, share crewTeamFolder) string {
	folders := append([]string(nil), share.Folders...)
	return strings.Join([]string{b.Root, map[bool]string{true: "whole"}[share.Whole], strings.Join(folders, "\x00"),
		strings.Join(sortedOnPurpose(b.onPurpose()), "\x00"), strings.Join(b.StopSyncing, "\x00")}, "\x01")
}

// crewTeamWatchFor is the watch of the copy recorded in dir; it also closes the watches no round asked for lately.
func crewTeamWatchFor(dir, root string) *crewTeamWatch {
	now := time.Now()
	crewTeamWatches.Range(func(k, v any) bool {
		if w := v.(*crewTeamWatch); k != dir && w.idleSince(now) > crewTeamForgetWatchIdle {
			crewTeamWatches.Delete(k)
			w.close()
		}
		return true
	})
	v, _ := crewTeamWatches.LoadOrStore(dir, &crewTeamWatch{root: root, full: true, lastTake: now})
	w := v.(*crewTeamWatch)
	if w.root != root { // the copy moved: start again
		w.close()
		w = &crewTeamWatch{root: root, full: true, lastTake: now}
		crewTeamWatches.Store(dir, w)
	}
	return w
}

func (w *crewTeamWatch) idleSince(now time.Time) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return now.Sub(w.lastTake)
}

// take is this round's plan; the changes it hands out are this round's to read.
func (w *crewTeamWatch) take(key string) crewTeamScanPlan {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	woke := !w.lastTake.IsZero() && now.Sub(w.lastTake) > crewTeamWokeAfter
	w.lastTake = now
	every := crewTeamFullScanEvery
	if w.w == nil {
		every = crewTeamUnwatchedEvery
	}
	if w.full || woke || key != w.key || now.Sub(w.lastFull) > every {
		w.full, w.changed = false, nil
		return crewTeamScanPlan{full: true, rules: w.rules, watched: len(w.dirs)}
	}
	plan := crewTeamScanPlan{changed: w.changed, rules: w.rules, watched: len(w.dirs)}
	w.changed = nil
	return plan
}

// done records how the round's scan went: a failed round reads everything next time; a full scan sets what is watched.
func (w *crewTeamWatch) done(project, key string, plan crewTeamScanPlan, scan crewTeamScanResult, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil || scan.respell {
		w.full = true
		if scan.respell {
			crewTeamWakeSoon()
		}
		return
	}
	if plan.idle() {
		return
	}
	if plan.full {
		w.key, w.lastFull, w.rules = key, time.Now(), scan.rules
	}
	if w.off || w.closed {
		return
	}
	if plan.full && scan.names > crewTeamWatchNames {
		log.Printf("[CREW_TEAM_LOCAL] project=%s event=watch_off reason=too_many_names names=%d limit=%d", project, scan.names, crewTeamWatchNames)
		w.stop()
		return
	}
	if w.w == nil {
		fw, e := fsnotify.NewWatcher()
		if e != nil {
			log.Printf("[CREW_TEAM_LOCAL] project=%s event=watch_off reason=%q", project, e)
			w.off = true
			return
		}
		w.w, w.dirs = fw, map[string]bool{}
		go w.listen(fw)
	}
	want := map[string]bool{}
	for _, rel := range scan.dirs {
		want[rel] = true
		if w.dirs[rel] {
			continue
		}
		if e := w.w.Add(filepath.Join(w.root, filepath.FromSlash(rel))); e != nil {
			log.Printf("[CREW_TEAM_LOCAL] project=%s event=watch_off reason=%q watched=%d", project, e, len(w.dirs))
			w.stop()
			return
		}
		w.dirs[rel] = true
		if !plan.full {
			// A new folder: what was made in it before its watch began is read once more next round.
			if w.changed == nil {
				w.changed = map[string]bool{}
			}
			w.changed[rel] = true
		}
	}
	if plan.full { // folders gone or no longer shared
		for rel := range w.dirs {
			if !want[rel] {
				_ = w.w.Remove(filepath.Join(w.root, filepath.FromSlash(rel)))
				delete(w.dirs, rel)
			}
		}
	}
}

// listen notes each change under the copy's folder and starts a round once the burst settles.
func (w *crewTeamWatch) listen(fw *fsnotify.Watcher) {
	for {
		select {
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			w.note(ev)
		case _, ok := <-fw.Errors:
			if !ok {
				return
			}
			// Events were dropped (the system's queue overflowed): read everything next round.
			w.mu.Lock()
			w.full = true
			w.mu.Unlock()
			crewTeamWakeSoon()
		}
	}
}

func (w *crewTeamWatch) note(ev fsnotify.Event) {
	rel, err := filepath.Rel(w.root, ev.Name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if path.Base(rel) == ".gitignore" {
		w.full = true // what is ignored may have changed anywhere below it
	} else {
		if w.changed == nil {
			w.changed = map[string]bool{}
		}
		w.changed[rel] = w.changed[rel] || ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(crewTeamWakeDelay, crewTeamWakeSoon)
	} else {
		w.timer.Reset(crewTeamWakeDelay)
	}
}

// stop turns watching off for this copy: full scans only, at crewTeamUnwatchedEvery. Called with mu held.
func (w *crewTeamWatch) stop() {
	if w.w != nil {
		_ = w.w.Close()
	}
	w.w, w.dirs, w.off, w.changed = nil, nil, true, nil
}

func (w *crewTeamWatch) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.w != nil {
		_ = w.w.Close()
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.w, w.dirs, w.closed = nil, nil, true
}
