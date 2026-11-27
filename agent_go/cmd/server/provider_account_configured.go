package server

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Whether an account is actually set up (signed in, or has a key), so the UI
// offers "Set up" instead of "Use" for one that is not. A login-based user
// account is checked in its own HOME with the same status command as the
// account's Status button; results are cached briefly so listing accounts
// does not start a CLI every time. Unknown never blocks: it reports nil.

const (
	providerAccountConfiguredTTL      = 2 * time.Minute
	providerAccountConfiguredStaleTTL = 30 * time.Minute
	providerAccountConfiguredErrorTTL = 60 * time.Second
)

// providerAccountConfiguredWait bounds how long an explicit ?refresh=true list
// request waits for uncached checks; normal loads return immediately and warm
// the cache in the background (#4394).
var providerAccountConfiguredWait = 6 * time.Second

type providerAccountConfiguredWaitKey struct{}

func withProviderAccountConfiguredWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, providerAccountConfiguredWaitKey{}, true)
}

func providerAccountConfiguredShouldWait(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	wait, _ := ctx.Value(providerAccountConfiguredWaitKey{}).(bool)
	return wait
}

type providerAccountConfiguredEntry struct {
	configured *bool
	identity   string
	at         time.Time
	ttl        time.Duration
}

var providerAccountConfiguredCache = struct {
	sync.Mutex
	entries  map[string]providerAccountConfiguredEntry
	inFlight map[string]bool
}{entries: map[string]providerAccountConfiguredEntry{}, inFlight: map[string]bool{}}

// rememberProviderAccountStatus records a status result (from a list check,
// the Status button or sign-out) for the account.
func rememberProviderAccountStatus(id string, status providerAccountStatus) {
	var configured *bool
	switch status.State {
	case "signed_in":
		yes := true
		configured = &yes
	case "signed_out", "key_rejected":
		no := false
		configured = &no
	}
	providerAccountConfiguredCache.Lock()
	providerAccountConfiguredCache.entries[id] = providerAccountConfiguredEntry{
		configured: configured,
		identity:   status.Identity,
		at:         time.Now(),
		ttl:        providerAccountConfiguredTTL,
	}
	providerAccountConfiguredCache.Unlock()
}

func rememberProviderAccountProbeFailure(id string) {
	providerAccountConfiguredCache.Lock()
	// Keep any existing non-expired stale value if present; otherwise cache a
	// short unknown entry so a broken/slow CLI does not re-probe every request.
	if prev, ok := providerAccountConfiguredCache.entries[id]; ok && time.Since(prev.at) <= providerAccountConfiguredStaleTTL {
		prev.at = time.Now()
		prev.ttl = providerAccountConfiguredErrorTTL
		providerAccountConfiguredCache.entries[id] = prev
	} else {
		providerAccountConfiguredCache.entries[id] = providerAccountConfiguredEntry{
			at:  time.Now(),
			ttl: providerAccountConfiguredErrorTTL,
		}
	}
	providerAccountConfiguredCache.Unlock()
}

func cachedProviderAccountConfigured(id string) (*bool, bool) {
	entry, ok := cachedProviderAccountEntry(id)
	return entry.configured, ok
}

func cachedProviderAccountEntry(id string) (providerAccountConfiguredEntry, bool) {
	entry, fresh, _ := lookupProviderAccountEntry(id)
	if !fresh {
		return providerAccountConfiguredEntry{}, false
	}
	return entry, true
}

func lookupProviderAccountEntry(id string) (entry providerAccountConfiguredEntry, fresh, stale bool) {
	providerAccountConfiguredCache.Lock()
	defer providerAccountConfiguredCache.Unlock()
	entry, ok := providerAccountConfiguredCache.entries[id]
	if !ok {
		return providerAccountConfiguredEntry{}, false, false
	}
	ttl := entry.ttl
	if ttl <= 0 {
		ttl = providerAccountConfiguredTTL
	}
	age := time.Since(entry.at)
	if age <= ttl {
		return entry, true, false
	}
	if age <= providerAccountConfiguredStaleTTL {
		return entry, false, true
	}
	return providerAccountConfiguredEntry{}, false, false
}

// providerAccountStatusProbe runs one account's status check; swappable in
// tests.
var providerAccountStatusProbe = func(ctx context.Context, api *StreamingAPI, id string) (providerAccountStatus, bool) {
	target, err := api.resolveProviderAccountTarget(ctx, id, "")
	if err != nil {
		return providerAccountStatus{}, false
	}
	return checkProviderAccountStatus(ctx, target, false), true
}

// fillProviderAccountConfigured sets Configured on every view.
func (api *StreamingAPI) fillProviderAccountConfigured(ctx context.Context, views []providerAccountView, records []storedProviderConnection) {
	byID := make(map[string]*storedProviderConnection, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
	}
	keys := MergedProviderAPIKeys(ctx)
	var wg sync.WaitGroup
	pending := map[int]string{}
	for i := range views {
		view := &views[i]
		if view.Relation == "server" {
			configured, _ := providerAuthConfigured(view.Provider, keys)
			view.Configured = &configured
			// Whose login it is, from the same status check as the
			// Status button (a key has no identity to show).
			if configured {
				if entry, fresh, stale := lookupProviderAccountEntry(view.ID); fresh {
					view.Identity = entry.identity
				} else {
					if stale {
						view.Identity = entry.identity
					}
					pending[i] = view.ID
				}
			}
			continue
		}
		record := byID[view.ID]
		if record == nil {
			continue
		}
		if record.AuthMethod == providerAuthServerLogin {
			status := serverLoginStatus(*record)
			configured := status.State == "signed_in"
			view.Configured, view.Identity = &configured, status.Identity
			continue
		}
		if record.AuthMethod != "cli_login" {
			configured := strings.TrimSpace(record.Credential) != ""
			view.Configured = &configured
			continue
		}
		if entry, fresh, stale := lookupProviderAccountEntry(view.ID); fresh {
			view.Configured, view.Identity = entry.configured, entry.identity
			continue
		} else if stale {
			view.Configured, view.Identity = entry.configured, entry.identity
		}
		pending[i] = view.ID
	}
	if len(pending) == 0 {
		return
	}
	for _, id := range pending {
		providerAccountConfiguredCache.Lock()
		busy := providerAccountConfiguredCache.inFlight[id]
		providerAccountConfiguredCache.inFlight[id] = true
		providerAccountConfiguredCache.Unlock()
		if busy {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() {
				providerAccountConfiguredCache.Lock()
				delete(providerAccountConfiguredCache.inFlight, id)
				providerAccountConfiguredCache.Unlock()
			}()
			probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), providerStatusTimeout+5*time.Second)
			defer cancel()
			if status, ok := providerAccountStatusProbe(probeCtx, api, id); ok {
				rememberProviderAccountStatus(id, status)
			} else {
				rememberProviderAccountProbeFailure(id)
			}
		}(id)
	}
	if !providerAccountConfiguredShouldWait(ctx) {
		return
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(providerAccountConfiguredWait):
	case <-ctx.Done():
	}
	for i, id := range pending {
		entry, ok := cachedProviderAccountEntry(id)
		if !ok {
			continue
		}
		views[i].Identity = entry.identity
		// A server account keeps the configured state from its probe above.
		if views[i].Relation != "server" {
			views[i].Configured = entry.configured
		}
	}
}
