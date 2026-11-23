package llmtypes

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCodingSDKSetupProgressAndCancellation(t *testing.T) {
	defer SetCodingSDKSetup(nil)
	wait := make(chan struct{})
	SetCodingSDKSetup(func(ctx context.Context, provider string) error {
		if provider != "cursor-cli" {
			t.Errorf("wrong provider: %s", provider)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
			return nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	stream := make(chan StreamChunk, 1)
	done := make(chan error, 1)
	go func() { done <- PrepareCodingSDK(ctx, "cursor-cli", stream) }()
	select {
	case chunk := <-stream:
		if chunk.Type != StreamChunkTypeReasoning || chunk.Content != "Setting up Cursor for first use…" {
			t.Fatalf("unexpected setup progress: %+v", chunk)
		}
	case <-ctx.Done():
		t.Fatal("no setup progress")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	close(wait)
}
