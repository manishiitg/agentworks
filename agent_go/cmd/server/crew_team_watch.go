package server

import (
	"encoding/json"
	"log"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Team sync watches each copy's folders instead of reading every shared file every round (owner, 6 Nov: Team sync
// lighter). A round reads only the folders where something changed (crewTeamScanChanged); a full scan still runs at
// the start, every few minutes, after the computer wakes, when what the share reads changes, and after a failed round,
// so a missed event is caught. A copy that cannot be watched (too many names, or the system refused) has full scans
// only, less often than the old every-round walk.
//
// How a copy is watched depends on the system (crewTeamTreeWatching): on a Mac (FSEvents) and on Windows
// (ReadDirectoryChangesW over the subtree) one watcher covers the copy's whole folder, with no handle per folder or
// file, so no name budget applies. Elsewhere each folder is added to fsnotify; on a Mac built without cgo that is
// kqueue, which holds one open file per watched name, hence the global name budget.

const (
	crewTeamFullScanEvery  = 5 * time.Minute
	crewTeamUnwatchedEvery = 30 * time.Second // full scans of a copy with no watcher
	// A gap this long between rounds: the computer slept, events may be lost. Above the once-a-minute check of a network
	// with a live relay (crewTeamCheckAfter): at one minute every paced round read as a wake and scanned everything.
	crewTeamWokeAfter       = 3 * time.Minute
	crewTeamWakeDelay       = time.Second      // a burst of writes starts one round
	crewTeamForgetWatchIdle = 10 * time.Minute // a copy no round asked for: its watcher closes
	// Global budget across every Team copy for per-folder watchers (kqueue opens each watched name).
	crewTeamWatchNames = 4000
)

// crewTeamWake starts the next round early, once a burst of changes has settled.
var crewTeamWake = make(chan struct{}, 1)

func crewTeamWakeSoon() {
	select {
	case crewTeamWake <- struct{}{}:
	default:
	}
}

// crewTeamWatcher reports changes under one copy's folder to its crewTeamWatch (note, overflow). A tree watcher
// covers the whole folder from the start and ignores Add/Remove; a per-folder watcher needs each folder added.
type crewTeamWatcher interface {
	Add(dir string) error
	Remove(dir string)
	Close() error
}

// crewTeamWatches: copy record folder -> its watch.
var crewTeamWatches sync.Map
var crewTeamWatchBudget atomic.Int64
var crewTeamFallbackCopies atomic.Int64

// Caller holds the watch lock. Reservations cover directory entries plus roots.
func (w *crewTeamWatch) reserveNames(want int64) bool {
	for {
		old := crewTeamWatchBudget.Load()
		next := old - w.names + want
		if next > crewTeamWatchNames {
			return false
		}
		if crewTeamWatchBudget.CompareAndSwap(old, next) {
			w.names = want
			return true
		}
	}
}

// Changed scans replace counts, rather than repeatedly charging the same entries.
func (w *crewTeamWatch) countedNames(full bool, counts map[string]int) int64 {
	if full || w.dirNames == nil {
		w.dirNames = map[string]int{}
	}
	for dir, count := range counts {
		if count == 0 {
			for prior := range w.dirNames {
				if prior != dir && (dir == "" || strings.HasPrefix(prior, dir+"/")) {
					delete(w.dirNames, prior)
				}
			}
		}
		w.dirNames[dir] = count
	}
	total := int64(len(w.dirNames))
	for _, count := range w.dirNames {
		total += int64(count)
	}
	return total
}

func (w *crewTeamWatch) releaseNames() {
	crewTeamWatchBudget.Add(-w.names)
	w.names = 0
}

type crewTeamWatch struct {
	mu       sync.Mutex
	names    int64
	dirNames map[string]int
	root     string
	key      string // what the share reads, at the last full scan
	w        crewTeamWatcher
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
	mounts, _ := json.Marshal(b.Mounts)
	return strings.Join([]string{b.Root, strings.Join(folders, "\x00"),
		strings.Join(sortedOnPurpose(b.onPurpose()), "\x00"), strings.Join(b.StopSyncing, "\x00"), string(mounts)}, "\x01")
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
	if w.closed || (w.off && !plan.full) {
		return
	}

	if !crewTeamTreeWatching {
		wantNames := w.countedNames(plan.full, scan.dirNames)
		if !w.reserveNames(wantNames) {
			if !w.off {
				log.Printf("[CREW_TEAM_LOCAL] project=%s event=watch_off reason=global_name_budget names=%d reserved=%d limit=%d", project, wantNames, crewTeamWatchBudget.Load(), crewTeamWatchNames)
			}
			w.stop()
			return
		}
	}

	if w.off {
		w.off = false
		crewTeamFallbackCopies.Add(-1)
	}
	if w.w == nil {
		fw, e := newCrewTeamWatcher(w.root, w)
		if e != nil {
			log.Printf("[CREW_TEAM_LOCAL] project=%s event=watch_off reason=%q", project, e)
			w.stop()
			return
		}
		w.w, w.dirs = fw, map[string]bool{}
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
		if !plan.full && !crewTeamTreeWatching {
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
				w.w.Remove(filepath.Join(w.root, filepath.FromSlash(rel)))
				delete(w.dirs, rel)
			}
		}
	}
}

// overflow: the system dropped events, so the next round reads everything.
func (w *crewTeamWatch) overflow() {
	w.mu.Lock()
	w.full = true
	w.mu.Unlock()
	crewTeamWakeSoon()
}

// note records a change at name (a full path under the copy); structural is a create, remove or rename.
func (w *crewTeamWatch) note(name string, structural bool) {
	rel, err := filepath.Rel(w.root, name)
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
		w.changed[rel] = w.changed[rel] || structural
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(crewTeamWakeDelay, crewTeamWakeSoon)
	} else {
		w.timer.Reset(crewTeamWakeDelay)
	}
}

// stop turns watching off for this copy: full scans only, at crewTeamUnwatchedEvery. Called with mu held.
func (w *crewTeamWatch) stop() {
	if !w.off && !w.closed {
		crewTeamFallbackCopies.Add(1)
	}
	w.releaseNames()
	if w.w != nil {
		_ = w.w.Close()
	}
	w.w, w.dirs, w.off, w.changed = nil, nil, true, nil
}

func (w *crewTeamWatch) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.off {
		crewTeamFallbackCopies.Add(-1)
	}
	w.releaseNames()
	if w.w != nil {
		_ = w.w.Close()
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.w, w.dirs, w.closed = nil, nil, true
}
