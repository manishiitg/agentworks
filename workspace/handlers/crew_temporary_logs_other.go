//go:build !linux

package handlers

// Desktop project files are not subject to server temporary-log housekeeping.
func sweepCrewTemporaryLogs(string) {}
