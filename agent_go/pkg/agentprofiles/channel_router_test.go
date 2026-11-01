package agentprofiles

import (
	"context"
	"errors"
	"testing"
)

// A product's channel router answers only for the profile it was registered
// on, sees tokens lower-cased, and is registered once.
func TestChannelRouterResolvesOnlyForItsProfile(t *testing.T) {
	registry := NewRegistry()
	err := registry.RegisterChannelRouter("example-app", func(_ context.Context, userID, token string) (ChannelProfileRoute, bool, error) {
		if userID != "user-1" {
			t.Fatalf("userID = %q, want user-1", userID)
		}
		switch token {
		case "child":
			return ChannelProfileRoute{ProfileID: "example-app-child", ConversationKey: "fractions"}, true, nil
		case "broken":
			return ChannelProfileRoute{}, true, errors.New("not now")
		}
		return ChannelProfileRoute{}, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	route, ok, err := registry.ResolveChannelRoute(context.Background(), "example-app", "user-1", " Child ")
	if err != nil || !ok || route.ProfileID != "example-app-child" || route.ConversationKey != "fractions" {
		t.Fatalf("@child = (%+v, %v, %v)", route, ok, err)
	}
	if _, ok, err := registry.ResolveChannelRoute(context.Background(), "example-app", "user-1", "report"); ok || err != nil {
		t.Fatalf("unknown token = (ok=%v, err=%v), want not the product's", ok, err)
	}
	if _, ok, err := registry.ResolveChannelRoute(context.Background(), "example-app", "user-1", "broken"); !ok || err == nil {
		t.Fatalf("token the product cannot serve = (ok=%v, err=%v), want ok with an error", ok, err)
	}
	if _, ok, err := registry.ResolveChannelRoute(context.Background(), "example-product", "user-1", "child"); ok || err != nil {
		t.Fatalf("another profile = (ok=%v, err=%v), want no router", ok, err)
	}

	if err := registry.RegisterChannelRouter("example-app", func(context.Context, string, string) (ChannelProfileRoute, bool, error) {
		return ChannelProfileRoute{}, false, nil
	}); err == nil {
		t.Fatal("second router for the same profile registered, want an error")
	}
	if err := registry.RegisterChannelRouter("not a profile id!", nil); err == nil {
		t.Fatal("invalid registration accepted")
	}
}
