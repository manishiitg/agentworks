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

const providerAccountConfiguredTTL = 2 * time.Minute

// providerAccountConfiguredWait bounds how long one list request waits for
// uncached checks; slower ones still land in the cache for the next list.
var providerAccountConfiguredWait = 6 * time.Second

type providerAccountConfiguredEntry struct {
	configured *bool
	at         time.Time
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
	providerAccountConfiguredCache.entries[id] = providerAccountConfiguredEntry{configured: configured, at: time.Now()}
	providerAccountConfiguredCache.Unlock()
}

func cachedProviderAccountConfigured(id string) (*bool, bool) {
	providerAccountConfiguredCache.Lock()
	defer providerAccountConfiguredCache.Unlock()
	entry, ok := providerAccountConfiguredCache.entries[id]
	if !ok || time.Since(entry.at) > providerAccountConfiguredTTL {
		return nil, false
	}
	return entry.configured, true
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
			continue
		}
		record := byID[view.ID]
		if record == nil {
			continue
		}
		if record.AuthMethod != "cli_login" {
			configured := strings.TrimSpace(record.Credential) != ""
			view.Configured = &configured
			continue
		}
		if configured, ok := cachedProviderAccountConfigured(view.ID); ok {
			view.Configured = configured
			continue
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
			}
		}(id)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(providerAccountConfiguredWait):
	case <-ctx.Done():
	}
	for i, id := range pending {
		if configured, ok := cachedProviderAccountConfigured(id); ok {
			views[i].Configured = configured
		}
	}
}
