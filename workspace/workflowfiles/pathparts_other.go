//go:build !windows

package workflowfiles

// windowsRefusedPathPart is only meaningful on Windows (see pathparts_windows.go).
func windowsRefusedPathPart(string) bool { return false }
