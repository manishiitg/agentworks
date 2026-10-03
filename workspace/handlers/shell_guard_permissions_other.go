//go:build !linux

package handlers

// User slots are Linux accounts; other platforms retain their existing modes.
func makeSlotWriteDirectoryUsable(path, root string) error { return nil }
