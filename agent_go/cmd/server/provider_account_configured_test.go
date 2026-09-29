package server

import (
	"context"
	"testing"
	"time"
)

func TestProviderAccountConfiguredFromKeysLoginsAndCache(t *testing.T) {
	calls := 0
	orig := providerAccountStatusProbe
	providerAccountStatusProbe = func(ctx context.Context, api *StreamingAPI, id string) (providerAccountStatus, bool) {
		calls++
		switch id {
		case "signed-in":
			return providerAccountStatus{State: "signed_in"}, true
		case "signed-out":
			return providerAccountStatus{State: "signed_out"}, true
		}
		return providerAccountStatus{State: "unknown"}, true
	}
	t.Cleanup(func() { providerAccountStatusProbe = orig })
	providerAccountConfiguredCache.Lock()
	providerAccountConfiguredCache.entries = map[string]providerAccountConfiguredEntry{}
	providerAccountConfiguredCache.Unlock()

	records := []storedProviderConnection{
		{ProviderConnection: ProviderConnection{ID: "key", AuthMethod: "api_key"}, Credential: "sk-x"},
		{ProviderConnection: ProviderConnection{ID: "empty-key", AuthMethod: "api_key"}},
		{ProviderConnection: ProviderConnection{ID: "signed-in", AuthMethod: "cli_login"}},
		{ProviderConnection: ProviderConnection{ID: "signed-out", AuthMethod: "cli_login"}},
		{ProviderConnection: ProviderConnection{ID: "unknown", AuthMethod: "cli_login"}},
	}
	views := make([]providerAccountView, len(records))
	for i, r := range records {
		views[i] = providerAccountView{ProviderConnection: r.ProviderConnection, Relation: "own"}
	}
	api := &StreamingAPI{}
	api.fillProviderAccountConfigured(context.Background(), views, records)
	want := map[string]*bool{"key": ptrBool(true), "empty-key": ptrBool(false), "signed-in": ptrBool(true), "signed-out": ptrBool(false), "unknown": nil}
	for _, v := range views {
		got, w := v.Configured, want[v.ID]
		if (got == nil) != (w == nil) || (got != nil && *got != *w) {
			t.Errorf("%s configured = %v, want %v", v.ID, deref(got), deref(w))
		}
	}
	if calls != 3 {
		t.Fatalf("login accounts probed %d times, want 3", calls)
	}
	// Cached: a second list does not run the CLIs again.
	api.fillProviderAccountConfigured(context.Background(), views, records)
	if calls != 3 {
		t.Fatalf("cached accounts probed again: %d", calls)
	}
	// Sign-out result replaces the cached state at once.
	rememberProviderAccountStatus("signed-in", providerAccountStatus{State: "signed_out"})
	if got, _ := cachedProviderAccountConfigured("signed-in"); got == nil || *got {
		t.Fatal("sign-out did not update the cached state")
	}
	_ = time.Second
}

func ptrBool(v bool) *bool { return &v }

func deref(v *bool) interface{} {
	if v == nil {
		return nil
	}
	return *v
}
