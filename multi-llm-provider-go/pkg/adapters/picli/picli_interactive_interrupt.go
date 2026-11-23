package picli

import (
	"context"
)

// SendPiInteractiveControlKey maps interrupt gestures to native clear_queue and abort.
func SendPiInteractiveControlKey(ctx context.Context, ownerSessionID, key string) error {
	return interruptPiRPC(ctx, ownerSessionID, key)
}
