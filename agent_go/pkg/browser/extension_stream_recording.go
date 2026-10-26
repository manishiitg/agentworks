package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

// Stream enable shares the already-connected browser daemon. It does not call
// the native recorder or create another CDP client/encoder (PLAT-691).
func (e *Executor) executeExtensionRecording(ctx context.Context, client *browserrelay.Client, opts *ExecuteOptions, plan *browserArtifactPlan, values []string, endpoint, owner, targetID string) (string, bool, error) {
	if plan == nil || len(values) == 0 {
		return "", false, fmt.Errorf("Recording requires its scoped artifact")
	}
	nativeOpts := *opts
	nativeOpts.ArtifactTransfer = nil
	native := func(command string, args ...string) (string, error) {
		cli := []string{"--session", client.Session(), command}
		cli = append(cli, args...)
		cli = append(cli, "--cdp", endpoint, "--pin-tab", "--json")
		return e.Client.ExecuteCommand(ctx, cli, &nativeOpts)
	}
	if values[0] == "start" {
		fps := 15
		for i := 2; i < len(values); i++ {
			if values[i] == "--fps" && i+1 < len(values) {
				fps, _ = strconv.Atoi(values[i+1])
				i++
				continue
			}
			if strings.HasPrefix(values[i], "http://") || strings.HasPrefix(values[i], "https://") || values[i] == "about:blank" {
				if _, err := native("open", values[i]); err != nil {
					return "", false, err
				}
			}
		}
		raw, err := native("stream", "status")
		if err != nil {
			return "", false, err
		}
		var status struct {
			Success bool `json:"success"`
			Data    struct {
				Enabled bool `json:"enabled"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &status) != nil || !status.Success {
			return "", false, fmt.Errorf("Could not read this browser's stream status")
		}
		startedStream := !status.Data.Enabled
		if startedStream {
			if _, err = native("stream", "enable"); err != nil {
				return "", false, err
			}
		}
		// Log requested/effective options, never the private endpoint or navigation URL.
		log.Printf("[BROWSER_RECORDING] action=request_start owner=%q browser_session=%q target=%q fps=%d output=%q", owner, client.Session(), targetID, fps, plan.RequestedPath)
		output, err := e.Client.RecordStream(ctx, "start", fps, opts)
		if err != nil && startedStream {
			_, _ = native("stream", "disable")
		}
		return output, startedStream, err
	}
	lease, _ := getBrowserArtifactLease(plan.LeaseKey)
	output, err := e.Client.RecordStream(ctx, "stop", 0, opts)
	if lease.StreamEnabledByRecording {
		if _, cleanupErr := native("stream", "disable"); cleanupErr != nil {
			log.Printf("[BROWSER_RECORDING] action=stream_cleanup_failed owner=%q browser_session=%q", owner, client.Session())
		}
	}
	return output, false, err
}
