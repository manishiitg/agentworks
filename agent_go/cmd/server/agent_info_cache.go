package server

import (
	"context"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const agentInfoRefreshWindow = 10 * time.Second

type agentInfoCacheKey struct {
	api             *StreamingAPI
	owner, kind, id string
}
type agentInfoCacheEntry struct {
	ready   chan struct{}
	value   any
	err     error
	expires time.Time
}

var agentInfoReadCache = struct {
	sync.Mutex
	entries map[agentInfoCacheKey]*agentInfoCacheEntry
}{entries: map[agentInfoCacheKey]*agentInfoCacheEntry{}}

// Coalesce concurrent readers of metadata, not admission. Callers still build
// their authorized rows afresh. Failed reads have a short backoff, not stale data.
func agentInfoCached[T any](ctx context.Context, key agentInfoCacheKey, load func() (T, error)) (T, error) {
	var zero T
	for {
		now := time.Now()
		cache := &agentInfoReadCache
		cache.Lock()
		if entry := cache.entries[key]; entry != nil {
			if entry.ready != nil {
				ready := entry.ready
				cache.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return zero, ctx.Err()
				}
			}
			if now.Before(entry.expires) {
				value, err := entry.value.(T), entry.err
				cache.Unlock()
				return value, err
			}
			delete(cache.entries, key)
		}
		// Bound memory across people, test API instances and expired projects.
		for k, v := range cache.entries {
			if v.ready == nil && !now.Before(v.expires) {
				delete(cache.entries, k)
			}
		}
		if len(cache.entries) >= 256 {
			for k, v := range cache.entries {
				if v.ready == nil {
					delete(cache.entries, k)
					break
				}
			}
		}
		entry := &agentInfoCacheEntry{ready: make(chan struct{})}
		cache.entries[key] = entry
		cache.Unlock()
		value, err := load()
		cache.Lock()
		entry.value, entry.err = value, err
		ttl := agentInfoRefreshWindow
		if err != nil {
			ttl = time.Second
		}
		entry.expires = time.Now().Add(ttl)
		close(entry.ready)
		entry.ready = nil
		cache.Unlock()
		return value, err
	}
}

type agentInfoDirectorySource struct {
	Document productConversationRegistryDocument
}

func (api *StreamingAPI) agentDirectorySource(ctx context.Context, owner string) (agentInfoDirectorySource, error) {
	return agentInfoCached(ctx, agentInfoCacheKey{api, owner, "directory_source", ""}, func() (agentInfoDirectorySource, error) {
		path := productConversationRegistryPath(owner)
		mu := productConversationRegistryMutex(path)
		mu.Lock()
		doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, path)
		mu.Unlock()
		if err != nil {
			return agentInfoDirectorySource{}, err
		}
		return agentInfoDirectorySource{Document: doc}, nil
	})
}

type agentInfoProject struct {
	Profile  agentprofiles.Profile
	Binding  productConversationBinding
	Manifest productProjectManifest
}

func (api *StreamingAPI) agentRuntimeProject(ctx context.Context, owner string, record ProductConversationRecord) (agentInfoProject, error) {
	return agentInfoCached(ctx, agentInfoCacheKey{api, owner, "project", record.ProfileID + "/" + record.ResourceID + "/" + record.WorkspacePath}, func() (agentInfoProject, error) {
		profile, binding, manifest, _, err := api.projectActionDispatcher().projectManifestAnyOwner(ctx, owner, record.ProfileID, record.ResourceID)
		return agentInfoProject{profile, binding, manifest}, err
	})
}

func (api *StreamingAPI) projectActionDispatcher() *ProjectActionDispatcher {
	if api.projectActions != nil {
		return api.projectActions
	}
	return NewProjectActionDispatcher(api, api.agentProfiles)
}
