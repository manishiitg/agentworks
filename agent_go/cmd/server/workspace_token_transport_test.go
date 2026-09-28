package server

import (
	"net/http"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
)

// The server's startup hook must leave http.DefaultTransport an
// *http.Transport: whatsmeow.NewClient asserts that type, and a wrapper made
// the agent panic at startup on RTS (2026-09-26).
func TestWorkspaceTokenHookKeepsDefaultTransportForWhatsApp(t *testing.T) {
	if _, ok := http.DefaultTransport.(*http.Transport); !ok {
		t.Fatalf("DefaultTransport is %T", http.DefaultTransport)
	}
	if client := whatsmeow.NewClient(&store.Device{}, nil); client == nil {
		t.Fatal("whatsmeow client not created")
	}
}
