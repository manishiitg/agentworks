//go:build linux

package security

import "testing"

func TestLandlockDoesNotGrantTheSharedTmp(t *testing.T) {
	for _, path := range landlockSystemWritePaths() {
		if path == "/tmp" {
			t.Fatal("sandboxed commands may write the shared /tmp")
		}
	}
}
