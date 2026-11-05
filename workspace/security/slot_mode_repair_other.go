//go:build !linux

package security

import "context"

// RepairSlotGitModes is Linux-only: slot accounts exist only there.
func RepairSlotGitModes(context.Context, string, string) (int, error) { return 0, nil }
