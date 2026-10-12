package server

import "testing"

func TestProductURLAuthReturn(t *testing.T) {
	for _, path := range []string{"/bullpen/connect", "/code/projects/example/dashboard", "/brain?folder=Guides%2FGetting+started", "/workflows/example"} {
		if got := safeProductAuthReturn(path); got != path {
			t.Errorf("safe return %q: got %q", path, got)
		}
	}
	for _, path := range []string{"https://example.com/code", "//example.com/code", "/%2fexample.com/code", "/%252fexample.com/code", "/%5cexample.com/code", "/code/../login", "/code/%252e%252e/login", "/login?next=/code", "/auth/callback", "/code/%0d%0aLocation:x"} {
		if got := safeProductAuthReturn(path); got != "" {
			t.Errorf("unsafe return %q admitted as %q", path, got)
		}
	}
	storeState("product-return-test", "example", "https://example.com/auth/callback", "/bullpen/connect")
	entry, ok := validateAndConsumeState("product-return-test")
	if !ok || entry.ReturnTo != "/bullpen/connect" {
		t.Fatal("OAuth state lost its server-held destination")
	}
	if _, ok := validateAndConsumeState("product-return-test"); ok {
		t.Fatal("OAuth return state replayed")
	}
}
