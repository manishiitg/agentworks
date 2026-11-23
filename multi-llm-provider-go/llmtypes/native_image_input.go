package llmtypes

import (
	"context"
	"io"
	"sync/atomic"
)

// NativeImageInput is a delivery-local capability installed by the host after
// admitting an attachment. Open rechecks access and prepares one image. It is
// not a caller-supplied path, a saved message part or a base64 history field.
// The reader's Close releases its prepared file/buffers. Preparation must decode one image at a time; prepared file readers may coexist
// until the single RPC. Close all readers immediately after that RPC.
type NativeImageInput struct {
	ID      string
	Skipped func(reason string)
	Open    func(context.Context) (reader io.ReadCloser, mediaType string, bytes int64, err error)
}

type nativeImageInputScope struct {
	sessionID string
	inputs    []NativeImageInput
	claimed   *atomic.Bool
}
type nativeImageInputKey struct{}

func WithNativeImageInputs(ctx context.Context, sessionID string, inputs []NativeImageInput) context.Context {
	return context.WithValue(ctx, nativeImageInputKey{}, nativeImageInputScope{sessionID: sessionID, inputs: append([]NativeImageInput(nil), inputs...), claimed: &atomic.Bool{}})
}

// Delegated child contexts inherit values, never the parent's image grant.
func NativeImageInputsForSession(ctx context.Context, sessionID string) []NativeImageInput {
	scope, ok := ctx.Value(nativeImageInputKey{}).(nativeImageInputScope)
	if !ok || sessionID == "" || scope.sessionID != sessionID {
		return nil
	}
	return append([]NativeImageInput(nil), scope.inputs...)
}

// One delivery context may be reused by workflow/model retries. Images belong
// only to its first native request; a host-queued delivery installs fresh refs.
func ClaimNativeImageInputsForSession(ctx context.Context, sessionID string) []NativeImageInput {
	scope, ok := ctx.Value(nativeImageInputKey{}).(nativeImageInputScope)
	if !ok || sessionID == "" || scope.sessionID != sessionID || !scope.claimed.CompareAndSwap(false, true) {
		return nil
	}
	return append([]NativeImageInput(nil), scope.inputs...)
}
