package sessionregistry

import "testing"

func TestOwnerRegistryGetOrCreateInstallsOnce(t *testing.T) {
	registry := NewOwnerRegistry[*int]()
	firstValue := 1
	secondValue := 2

	first, created, ok := registry.GetOrCreate(" owner ", func() *int {
		return &firstValue
	})
	if !ok || !created || first == nil || *first != 1 {
		t.Fatalf("first get-or-create = item=%v created=%v ok=%v", first, created, ok)
	}

	second, created, ok := registry.GetOrCreate("owner", func() *int {
		return &secondValue
	})
	if !ok || created || second != first {
		t.Fatalf("second get-or-create = item=%v created=%v ok=%v, want existing %v", second, created, ok, first)
	}
}

func TestOwnerRegistryDeleteIfRequiresCurrentItem(t *testing.T) {
	registry := NewOwnerRegistry[string]()
	registry.Set("owner", "tmux-a")

	if registry.DeleteIf("owner", "tmux-b") {
		t.Fatal("DeleteIf removed mismatched item")
	}
	if got, ok := registry.Get("owner"); !ok || got != "tmux-a" {
		t.Fatalf("after mismatched delete, got %q ok=%v", got, ok)
	}
	if !registry.DeleteIf("owner", "tmux-a") {
		t.Fatal("DeleteIf did not remove matching item")
	}
	if got, ok := registry.Get("owner"); ok || got != "" {
		t.Fatalf("after matched delete, got %q ok=%v", got, ok)
	}
}

func TestOwnerRegistryDrainClearsEntries(t *testing.T) {
	registry := NewOwnerRegistry[string]()
	registry.Set("a", "tmux-a")
	registry.Set("b", "tmux-b")

	items := registry.Drain()
	if len(items) != 2 {
		t.Fatalf("Drain returned %d items, want 2", len(items))
	}
	if items := registry.Snapshot(); len(items) != 0 {
		t.Fatalf("Snapshot after Drain returned %d items, want 0", len(items))
	}
}

func TestOwnerRegistryReplaceReturnsPreviousContents(t *testing.T) {
	registry := NewOwnerRegistry[string]()
	registry.Set("old", "tmux-old")

	previous := registry.Replace(map[string]string{" new ": "tmux-new"})
	if previous["old"] != "tmux-old" {
		t.Fatalf("previous[old] = %q, want tmux-old", previous["old"])
	}
	if got, ok := registry.Get("new"); !ok || got != "tmux-new" {
		t.Fatalf("new registry item = %q ok=%v, want tmux-new/true", got, ok)
	}
	if _, ok := registry.Get("old"); ok {
		t.Fatal("old item should not remain after Replace")
	}
}
