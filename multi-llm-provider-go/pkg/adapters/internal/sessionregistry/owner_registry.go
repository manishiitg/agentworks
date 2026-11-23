package sessionregistry

import (
	"strings"
	"sync"
)

// OwnerRegistry maps an app-owned session id to the provider session state
// that backs it. The registry owns only the map; callers still own per-session
// locks and provider-specific cleanup.
type OwnerRegistry[T comparable] struct {
	mu    sync.Mutex
	items map[string]T
	zero  T
}

// NewOwnerRegistry creates an empty owner-keyed registry.
func NewOwnerRegistry[T comparable]() *OwnerRegistry[T] {
	return &OwnerRegistry[T]{items: map[string]T{}}
}

// Get returns the item currently registered for owner.
func (r *OwnerRegistry[T]) Get(owner string) (T, bool) {
	if r == nil {
		var zero T
		return zero, false
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return r.zero, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[owner]
	return item, ok
}

// Set registers item for owner, replacing any previous item.
func (r *OwnerRegistry[T]) Set(owner string, item T) bool {
	if r == nil {
		return false
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[owner] = item
	return true
}

// GetOrCreate atomically returns the existing item for owner or installs the
// item produced by create. The create callback runs while the registry lock is
// held, so keep it allocation-only; do not perform IO inside it.
func (r *OwnerRegistry[T]) GetOrCreate(owner string, create func() T) (item T, created bool, ok bool) {
	if r == nil {
		return item, false, false
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || create == nil {
		return item, false, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.items[owner]; exists {
		return existing, false, true
	}
	item = create()
	r.items[owner] = item
	return item, true, true
}

// Delete removes and returns the item registered for owner.
func (r *OwnerRegistry[T]) Delete(owner string) (T, bool) {
	if r == nil {
		var zero T
		return zero, false
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return r.zero, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[owner]
	if ok {
		delete(r.items, owner)
	}
	return item, ok
}

// DeleteIf removes owner only when the current item equals expected.
func (r *OwnerRegistry[T]) DeleteIf(owner string, expected T) bool {
	if r == nil {
		return false
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.items[owner]; ok && current == expected {
		delete(r.items, owner)
		return true
	}
	return false
}

// Find returns the first item for which match returns true.
func (r *OwnerRegistry[T]) Find(match func(T) bool) (owner string, item T, ok bool) {
	if r == nil || match == nil {
		return "", item, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for owner, item := range r.items {
		if match(item) {
			return owner, item, true
		}
	}
	return "", item, false
}

// Snapshot returns all currently registered items.
func (r *OwnerRegistry[T]) Snapshot() []T {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, item)
	}
	return out
}

// Drain removes all entries and returns the items that were registered.
func (r *OwnerRegistry[T]) Drain() []T {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, 0, len(r.items))
	for _, item := range r.items {
		out = append(out, item)
	}
	r.items = map[string]T{}
	return out
}

// Replace swaps the registry contents and returns the previous contents. It is
// primarily useful for package tests that need to seed a process-global
// provider registry and then restore it.
func (r *OwnerRegistry[T]) Replace(items map[string]T) map[string]T {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old := cloneMap(r.items)
	r.items = cloneMap(items)
	return old
}

func cloneMap[T comparable](in map[string]T) map[string]T {
	out := make(map[string]T, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}
