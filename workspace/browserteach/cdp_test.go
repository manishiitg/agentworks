package browserteach

import (
	"context"
	"testing"
)

func TestRecorderEndpointCannotDialOtherHosts(t *testing.T) {
	for _, endpoint := range []string{"ws://example.com:9222/devtools/browser/id", "http://127.0.0.1:9222/", "ws://user:secret@127.0.0.1:9222/"} {
		if _, err := Connect(context.Background(), endpoint); err == nil {
			t.Fatalf("Accepted %s", endpoint)
		}
	}
}
func TestRecordedURLDropsAuthenticationMaterial(t *testing.T) {
	got := SanitizeURL("https://name:password@example.com/reports?token=secret#private")
	if got != "https://example.com/reports" {
		t.Fatal(got)
	}
}
