//go:build windows

package handlers

// slotOwningGroup: slots exist only on Linux hosts.
func slotOwningGroup(dir string) string { return "" }
