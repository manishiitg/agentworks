package server

import (
	"strings"
	"testing"
)

// These tests pin the information content of Pulse write-path rejections. The
// Fixer is the only stage that mutates state, so a rejection it cannot act on
// costs a completed fix that never gets recorded.
func assertRejectionContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a rejection, got nil")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection %q is missing %q", err.Error(), want)
		}
	}
}
