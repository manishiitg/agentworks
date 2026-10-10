package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
)

func TestPythonRelayToolStopsWithAgentOrBridgeRequest(t *testing.T) {
	for _, cancelAgent := range []bool{true, false} {
		agentCtx, stopAgent := context.WithCancel(context.Background())
		toolCtx, stopTool := context.WithCancel(context.Background())
		entered, finished := make(chan struct{}), make(chan error, 1)
		go func() {
			_, err := callBoundPythonRelayTool(agentCtx, toolCtx, func(ctx context.Context, _ string, _ map[string]interface{}) (string, error) {
				close(entered)
				<-ctx.Done()
				return "", ctx.Err()
			}, "lookup", nil)
			finished <- err
		}()
		<-entered
		if cancelAgent {
			stopAgent()
		} else {
			stopTool()
		}
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("callback did not preserve cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("tool outlived its agent or HTTP request")
		}
		stopAgent()
		stopTool()
	}
}

func TestPythonRelayRestartCannotAdmitUnjournaledCapabilities(t *testing.T) {
	for _, call := range []relaypython.Call{
		{Recovery: "restart", MCP: []relaypython.MCP{{Server: "external"}}},
		{Recovery: "restart", Skills: []string{"external-actions"}},
		{Recovery: "restart", Kind: "mcp", Server: "external", Tool: "send"},
	} {
		api := &StreamingAPI{}
		_, err := api.callPythonRelayAgent(context.Background(), nil, "", "", call, nil)
		if err == nil || err.Error() != "restart recovery requires journaled Python tools only" {
			t.Fatalf("unjournaled capability reached agent initialization: %v", err)
		}
	}
}
