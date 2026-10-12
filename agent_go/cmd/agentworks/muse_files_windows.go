//go:build windows

package main

import (
	"errors"
	"os"
)

// Muse's native process/observation contract is currently Unix-only. Refuse
// discovery on unsupported hosts instead of weakening file/process fencing.
func openMuseObservationDirectory(string, bool) (*os.File, error) {
	return nil, errors.New("Muse discovery is unavailable on this host; attach in Muse")
}
func readMuseObservation(*os.File, string) ([]byte, error) {
	return nil, errors.New("Muse discovery is unavailable on this host")
}
func writeMuseObservation(*os.File, string, []byte) error {
	return errors.New("Muse discovery is unavailable on this host")
}
func removeMuseObservation(*os.File, string) {}
