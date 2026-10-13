package relaypython

import (
	"context"
	"strings"
	"testing"
)

func TestRelayUnknownBackendCannotExecuteOrInspect(t *testing.T) {
	t.Setenv("AGENTWORKS_RELAY_BACKEND", "remote")
	if err := Run(context.Background(), Config{}); err == nil || !strings.Contains(err.Error(), "unknown Relay backend") {
		t.Fatal(err)
	}
	if _, err := SourceInspectorForBackend(); err == nil {
		t.Fatal("inspector bypassed backend failure")
	}
}
