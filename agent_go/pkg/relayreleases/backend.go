package relayreleases

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relayapi"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaybackend"
)

func selected() (relayapi.Releases, error) {
	f, err := relaybackend.Resolve()
	if err != nil {
		return nil, err
	}
	if f != nil {
		return f.Releases, nil
	}
	return Builtin(), nil
}
func Read(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	engine, err := selected()
	if err != nil {
		return nil, "", err
	}
	return engine.Read(ctx, p, workspace, version)
}
func Active(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, string, error) {
	engine, err := selected()
	if err != nil {
		return nil, "", err
	}
	return engine.Active(ctx, p, workspace)
}
func Resolve(ctx context.Context, p relayapi.ReleasePorts, workspace, version string) (*relayapi.Release, string, error) {
	engine, err := selected()
	if err != nil {
		return nil, "", err
	}
	return engine.Resolve(ctx, p, workspace, version)
}
func List(ctx context.Context, p relayapi.ReleasePorts, workspace string) ([]relayapi.Release, error) {
	engine, err := selected()
	if err != nil {
		return nil, err
	}
	return engine.List(ctx, p, workspace)
}
func Verify(ctx context.Context, p relayapi.ReleasePorts, release *relayapi.Release, workspace string) error {
	engine, err := selected()
	if err != nil {
		return err
	}
	return engine.Verify(ctx, p, release, workspace)
}
func Publish(ctx context.Context, p relayapi.ReleasePorts, workspace string) (*relayapi.Release, error) {
	engine, err := selected()
	if err != nil {
		return nil, err
	}
	return engine.Publish(ctx, p, workspace)
}
