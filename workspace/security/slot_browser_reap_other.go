//go:build !linux

package security

import "context"

// ReapSlotBrowserHelpers is Linux-only: slot accounts exist only there.
func ReapSlotBrowserHelpers(context.Context) {}
