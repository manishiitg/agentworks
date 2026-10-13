package relaypython

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaybackend"
)

func Run(ctx context.Context, cfg Config) error {
	f, err := relaybackend.Resolve()
	if err != nil {
		return err
	}
	if f != nil {
		return f.Run(ctx, cfg)
	}
	return runBuiltin(ctx, cfg)
}

func SourceInspectorForBackend() (string, error) {
	f, err := relaybackend.Resolve()
	if err != nil {
		return "", err
	}
	if f != nil {
		return f.SourceInspector, nil
	}
	return SourceInspector, nil
}
