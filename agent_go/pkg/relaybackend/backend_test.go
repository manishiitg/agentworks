package relaybackend

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
)

func TestRelayBackendRejectsInvalidFactories(t *testing.T) {
	for _, f := range []*relayapi.Factory{nil, {}, {ID: "relays", Version: relayapi.Version + 1}, {ID: "relays", Version: relayapi.Version}} {
		if _, err := validateFactory(f); err == nil {
			t.Fatal("invalid private factory admitted")
		}
	}
}

func TestRelayBackendUnknownModeFails(t *testing.T) {
	t.Setenv("AGENTWORKS_RELAY_BACKEND", "remote")
	if _, err := Resolve(); err == nil || !strings.Contains(err.Error(), "unknown Relay backend") {
		t.Fatal(err)
	}
}

func TestRelayBackendValidatesEntireProductBeforeRunning(t *testing.T) {
	called := false
	f := &relayapi.Factory{ID: "relays", Version: relayapi.Version, Files: fstest.MapFS{}, SourceInspector: "inspector", Run: func(context.Context, relayapi.Config) error { called = true; return nil }}
	if _, err := validateFactory(f); err == nil {
		t.Fatal("missing release engine admitted")
	}
	if called {
		t.Fatal("validation ran authored code")
	}
}
