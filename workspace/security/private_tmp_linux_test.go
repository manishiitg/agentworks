//go:build linux

package security

import "testing"

func TestLandlockDoesNotGrantTheSharedTmp(t *testing.T) {
	for _, path := range landlockSystemWritePaths(false) {
		if path == "/tmp" {
			t.Fatal("sandboxed commands may write the shared /tmp")
		}
	}
}

func TestPrivateTmpKeepsOnlyOutermostGrantsUnderTmp(t *testing.T) {
	got := privateTmpKeepPaths(LandlockPolicy{
		ReadPaths:  []string{"/data/crew", "/tmp/aws-1/c-2", "/tmp"},
		WritePaths: []string{"/tmp/aws-1/c-2/home", "/data/crew/.sandbox-cache"},
		WorkDir:    "/tmp/aws-1/c-2",
	})
	want := []string{"/tmp/aws-1/c-2", browserSocketDir}
	if len(got) != len(want) {
		t.Fatalf("keep paths = %v, want %v", got, want)
	}
	for i := range want {
		found := false
		for _, g := range got {
			found = found || g == want[i]
		}
		if !found {
			t.Fatalf("keep paths = %v, want %v", got, want)
		}
	}
}
