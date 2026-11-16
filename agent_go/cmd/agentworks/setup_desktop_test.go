package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// #2867: missing/failing providers must not stop setup of the other app;
// turning off removes hooks (including for an uninstalled app), never trust.
func TestDesktopSetupIsIndependentAndTurnOffRemovesHooks(t *testing.T) {
	var calls []string
	setup := func(_ context.Context, p string, hooks bool) error {
		calls = append(calls, p)
		if !hooks && p == "claude" {
			return errors.New("unavailable")
		}
		return nil
	}
	apps := setupDesktopApps(context.Background(), false, func(p string) bool { return p == "codex" }, func(string) bool { return false }, setup)
	if !reflect.DeepEqual(calls, []string{"codex"}) || apps[0].Outcome != "not_installed" || apps[1].Outcome != "installed" {
		t.Fatalf("apps=%v calls=%v", apps, calls)
	}
	calls = nil
	apps = setupDesktopApps(context.Background(), true, func(p string) bool { return p == "codex" }, func(p string) bool { return p == "claude" }, func(ctx context.Context, p string, hooks bool) error {
		if hooks {
			t.Fatal("Turn off requested full hooks")
		}
		return setup(ctx, p, hooks)
	})
	if !reflect.DeepEqual(calls, []string{"claude", "codex"}) || apps[0].Outcome != "failed" || apps[1].Outcome != "removed" {
		t.Fatalf("apps=%v calls=%v", apps, calls)
	}
}
