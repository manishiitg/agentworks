// Package keyedlock gives one mutex per key (a path, a schedule, a Watcher)
// and forgets a key once nobody holds or waits on it, so a lock map keyed by
// things that come and go does not grow for the life of the server.
package keyedlock

import "sync"

// Map hands out per-key lockers. The zero value is ready to use.
type Map struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

// Get returns a locker for key. Use it for one Lock/Unlock pair at a time.
func (k *Map) Get(key string) *Locker { return &Locker{m: k, key: key} }

// Len is the number of keys currently held or waited on.
func (k *Map) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.m)
}

func (k *Map) acquire(key string) *entry {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]*entry{}
	}
	e := k.m[key]
	if e == nil {
		e = &entry{}
		k.m[key] = e
	}
	e.refs++
	return e
}

func (k *Map) release(key string, e *entry) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e.refs--
	if e.refs == 0 && k.m[key] == e {
		delete(k.m, key)
	}
}

// Locker is one hold on a key's mutex.
type Locker struct {
	m   *Map
	key string
	e   *entry
}

func (l *Locker) Lock() {
	e := l.m.acquire(l.key)
	e.mu.Lock()
	l.e = e
}

// TryLock takes the key's mutex only if it is free.
func (l *Locker) TryLock() bool {
	e := l.m.acquire(l.key)
	if !e.mu.TryLock() {
		l.m.release(l.key, e)
		return false
	}
	l.e = e
	return true
}

func (l *Locker) Unlock() {
	e := l.e
	l.e = nil
	e.mu.Unlock()
	l.m.release(l.key, e)
}
