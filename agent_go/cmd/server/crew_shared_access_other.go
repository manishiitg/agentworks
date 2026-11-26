//go:build !linux

package server

// Slot permission repair runs only on Linux hosts.
func crewFolderHasSlotAccess(string, int) bool { return false }
