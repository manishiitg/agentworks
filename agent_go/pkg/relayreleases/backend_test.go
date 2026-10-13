package relayreleases

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
)

func TestRelayUnknownBackendCannotReadOrPublish(t *testing.T) {
	t.Setenv("AGENTWORKS_RELAY_BACKEND", "remote")
	ctx := context.Background()
	p := relayapi.ReleasePorts{Read: func(context.Context, string) (string, bool, error) {
		t.Fatal("backend failure reached storage")
		return "", false, nil
	}}
	if _, _, err := Read(ctx, p, "Workflow/test", "v1"); err == nil {
		t.Fatal("read accepted unknown backend")
	}
	if _, err := Publish(ctx, p, "Workflow/test"); err == nil {
		t.Fatal("publish accepted unknown backend")
	}
}
