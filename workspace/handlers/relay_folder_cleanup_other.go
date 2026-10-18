//go:build !linux

package handlers

import "context"

func cleanupRelaySlotRunFiles(_ context.Context, _, _ string) (bool, error) { return false, nil }
