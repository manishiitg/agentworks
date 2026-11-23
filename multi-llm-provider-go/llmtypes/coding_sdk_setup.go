package llmtypes

import (
	"context"
	"sync"
	"time"
)

var codingSDKSetup struct {
	sync.RWMutex
	prepare func(context.Context, string) error
}

// SetCodingSDKSetup lets the host prepare its transport components. Standalone
// callers keep their existing explicitly installed SDKs when no hook is set.
func SetCodingSDKSetup(prepare func(context.Context, string) error) {
	codingSDKSetup.Lock()
	codingSDKSetup.prepare = prepare
	codingSDKSetup.Unlock()
}

// PrepareCodingSDK waits only for the selected SDK, never for another provider.
// Use the existing progress stream; preparation is not prompt acceptance.
func PrepareCodingSDK(ctx context.Context, provider string, stream chan<- StreamChunk) error {
	codingSDKSetup.RLock()
	prepare := codingSDKSetup.prepare
	codingSDKSetup.RUnlock()
	if prepare == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- prepare(ctx, provider) }()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	label := "Claude"
	if provider == "cursor-cli" {
		label = "Cursor"
	}
	if stream != nil {
		select {
		case stream <- StreamChunk{Type: StreamChunkTypeReasoning, Content: "Setting up " + label + " for first use…", Metadata: map[string]any{"coding_sdk_setup": true}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
