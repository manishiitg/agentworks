package musecli

import (
	"path/filepath"
	"testing"
)

func TestMuseLandlockGrantsWriteTheEndpointLeaseFolder(t *testing.T) {
	data := t.TempDir()
	_, write := museLandlockGrants(nil, []string{"XDG_DATA_HOME=" + data})
	want := filepath.Join(data, "muse", "runtime")
	for _, path := range write {
		if path == want {
			return
		}
	}
	t.Fatalf("write grants %v lack %s", write, want)
}
